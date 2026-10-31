//! `--autocorrect prompt`: ask on the controlling terminal whether to
//! apply a flag correction. Ports Go `console/cli`
//! `autocorrect_prompt.go` and the policy half of
//! `autocorrect_candidate.go`.
//!
//! The question goes to the terminal, never stdin: a mistyped flag is
//! orthogonal to what the command does with stdin, and a heredoc payload
//! must not be eaten as the answer. It goes to the terminal, not stderr,
//! too: stderr may be redirected away from the operator. The terminal
//! handle is the one every kit prompt shares, so a later confirm prompt
//! still sees what an earlier read buffered.

use super::gate::PromptSource;
use super::usage::AutocorrectMode;

/// Whether the resolved leaf may have a flag rewritten under `mode`,
/// and the prompt's default answer.
///
/// `read` mode applies on a read-only leaf and nothing else; an
/// unannotated leaf is not read-only. `prompt` mode may ask about any
/// leaf, but a bare Enter means yes only on a read-only one.
pub(super) fn autocorrect_allowed(
    side_effect: Option<&str>,
    mode: AutocorrectMode,
) -> (bool, bool) {
    let read_only = side_effect == Some("read");
    match mode {
        AutocorrectMode::Read => (read_only, false),
        AutocorrectMode::Prompt => (true, read_only),
        AutocorrectMode::Off => (false, false),
    }
}

/// Asks whether to apply the correction. Returns `(applied, asked)`;
/// `asked` is false when there was no terminal, the suggest-only
/// fallback. EOF is a decline, never the yes default.
pub(super) fn prompt_autocorrect(
    src: &PromptSource,
    typed: &str,
    corrected: &str,
    default_yes: bool,
) -> (bool, bool) {
    let hint = if default_yes { "[Y/n]" } else { "[y/N]" };
    let Some(tty) = src.get() else {
        return (false, false);
    };
    let question = format!("Did you mean --{corrected} instead of --{typed}? {hint} ");
    match tty.ask(&question).as_deref() {
        // EOF ends the question: a decline, never the yes default.
        None => (false, true),
        Some("") => (default_yes, true),
        Some("y" | "yes") => (true, true),
        Some(_) => (false, true),
    }
}

#[cfg(test)]
mod tests {
    use super::*;
    use crate::cli::gate::Prompter;
    use std::sync::{Arc, Mutex};

    const TIERS: [&str; 7] = [
        "read",
        "write",
        "write-local",
        "write-shared",
        "destructive",
        "destructive-local",
        "destructive-shared",
    ];

    #[test]
    fn allowed_table() {
        use AutocorrectMode::*;
        let rows: [(Option<&str>, AutocorrectMode, bool, bool); 14] = [
            (Some("read"), Read, true, false),
            (Some("write"), Read, false, false),
            (Some("destructive"), Read, false, false),
            (Some("interactive"), Read, false, false),
            (None, Read, false, false),
            (Some("read"), Prompt, true, true),
            (Some("write"), Prompt, true, false),
            (Some("destructive"), Prompt, true, false),
            (None, Prompt, true, false),
            (Some("read"), Off, false, false),
            (Some("destructive"), Off, false, false),
            (Some("write-local"), Read, false, false),
            (Some("write-shared"), Read, false, false),
            (Some("destructive-shared"), Read, false, false),
        ];
        for (se, mode, allowed, yes) in rows {
            assert_eq!(
                autocorrect_allowed(se, mode),
                (allowed, yes),
                "{se:?} {mode:?}"
            );
        }
    }

    #[test]
    fn read_mode_allows_exactly_the_read_tier() {
        for se in TIERS {
            assert_eq!(
                autocorrect_allowed(Some(se), AutocorrectMode::Read).0,
                se == "read",
                "{se}"
            );
        }
    }

    /// A terminal that records the questions and answers from a script.
    struct Rec {
        asked: Arc<Mutex<String>>,
        answers: Mutex<std::collections::VecDeque<String>>,
    }

    impl Prompter for Rec {
        fn ask(&self, question: &str) -> Option<String> {
            self.asked.lock().unwrap().push_str(question);
            self.answers
                .lock()
                .unwrap()
                .pop_front()
                .map(|a| a.trim().to_lowercase())
        }
    }

    fn rec(answers: &[&str]) -> (PromptSource, Arc<Mutex<String>>) {
        let asked = Arc::new(Mutex::new(String::new()));
        let p: Arc<dyn Prompter> = Arc::new(Rec {
            asked: Arc::clone(&asked),
            answers: Mutex::new(answers.iter().map(|s| s.to_string()).collect()),
        });
        (PromptSource::new(move || Some(Arc::clone(&p))), asked)
    }

    #[test]
    fn answers_and_hint() {
        // (answer, default_yes, applied, hint); None answer is EOF
        let rows: [(Option<&str>, bool, bool, &str); 8] = [
            (Some("y"), false, true, "[y/N]"),
            (Some("yes"), false, true, "[y/N]"),
            (Some("Y"), false, true, "[y/N]"),
            (Some("n"), true, false, "[Y/n]"),
            (Some("maybe"), true, false, "[Y/n]"),
            (Some(""), true, true, "[Y/n]"),
            (Some(""), false, false, "[y/N]"),
            (None, true, false, "[Y/n]"),
        ];
        for (ans, dy, want, hint) in rows {
            let script: Vec<&str> = ans.into_iter().collect();
            let (src, asked) = rec(&script);
            let (applied, was_asked) = prompt_autocorrect(&src, "nam", "name", dy);
            assert!(was_asked, "{ans:?}: asked=false with a terminal");
            assert_eq!(applied, want, "{ans:?} default_yes={dy}");
            let q = asked.lock().unwrap().clone();
            assert!(q.contains(hint), "{q:?} lacks {hint}");
            assert!(q.contains("Did you mean --name instead of --nam?"), "{q:?}");
        }
    }

    #[test]
    fn no_terminal_never_blocks() {
        let (applied, asked) = prompt_autocorrect(&PromptSource::none(), "nam", "name", true);
        assert!(!asked);
        assert!(!applied);
    }

    #[test]
    fn one_terminal_serves_the_next_prompt() {
        // Three answers scripted on one source: each prompt must consume
        // its own, in order.
        let src = PromptSource::from_answers(["y", "n", "y"]);
        assert_eq!(
            prompt_autocorrect(&src, "forc", "force", false),
            (true, true)
        );
        assert_eq!(
            prompt_autocorrect(&src, "forc", "force", false),
            (false, true)
        );
        assert_eq!(
            prompt_autocorrect(&src, "forc", "force", false),
            (true, true)
        );
    }
}
