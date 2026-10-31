//! Yes/no confirmation (`tui.Confirm` in Go).
//!
//! Two layers. [`ConfirmState`] is the key-by-key state machine Go's
//! model implements, for callers that own raw-mode input. [`Confirm`] is
//! the line-based prompt: it reads an answer line from an injected reader,
//! so it needs no raw mode and no terminal, and it never blocks a
//! non-interactive run.

use super::style::Styler;
use std::io::{self, BufRead, IsTerminal, Write};

/// Key-driven state. `y`, `Y`, `enter` accept; `n`, `N`, `esc`, `q`
/// reject; any other key is ignored; once done, input is ignored.
#[derive(Debug, Clone, Copy, Default, PartialEq, Eq)]
pub struct ConfirmState {
    accepted: bool,
    done: bool,
}

impl ConfirmState {
    pub fn new() -> Self {
        Self::default()
    }

    /// Feed one key by name (`"y"`, `"enter"`, `"esc"`, ...).
    pub fn update(&mut self, key: &str) {
        if self.done {
            return;
        }
        match key {
            "y" | "Y" | "enter" => {
                self.accepted = true;
                self.done = true;
            }
            "n" | "N" | "esc" | "q" => {
                self.accepted = false;
                self.done = true;
            }
            _ => {}
        }
    }

    pub fn accepted(&self) -> bool {
        self.accepted
    }

    pub fn done(&self) -> bool {
        self.done
    }
}

/// A line-based yes/no prompt.
#[derive(Debug, Clone)]
pub struct Confirm {
    prompt: String,
    default: bool,
    styler: Styler,
}

impl Confirm {
    /// Empty answer (enter) accepts, as in Go. Use
    /// [`default_answer(false)`](Self::default_answer) before anything
    /// destructive.
    pub fn new(prompt: impl Into<String>, styler: Styler) -> Confirm {
        Confirm {
            prompt: prompt.into(),
            default: true,
            styler,
        }
    }

    /// The answer an empty line, or a non-interactive run, gives.
    pub fn default_answer(mut self, yes: bool) -> Confirm {
        self.default = yes;
        self
    }

    /// The prompt as shown before an answer: `<prompt> [y/n] `.
    pub fn render(&self) -> String {
        format!(
            "{} {} ",
            self.styler.accent_bold(&self.prompt),
            self.styler.muted("[y/n]")
        )
    }

    fn render_answer(&self, yes: bool) -> String {
        let a = if yes {
            self.styler.success("Yes")
        } else {
            self.styler.error("No")
        };
        format!("{} {}\n", self.styler.accent_bold(&self.prompt), a)
    }

    /// Ask on `out`, read the answer from `input`.
    ///
    /// Not `interactive`: nothing is written or read and the default
    /// answer comes back, so a script or CI run never hangs. Interactive:
    /// `y`, `yes` accept; `n`, `no`, `q` reject (any case); an empty line
    /// gives the default; anything else re-asks. End of input is an
    /// `UnexpectedEof` error, never a silent answer.
    pub fn ask<R: BufRead, W: Write>(
        &self,
        input: &mut R,
        out: &mut W,
        interactive: bool,
    ) -> io::Result<bool> {
        if !interactive {
            return Ok(self.default);
        }
        let mut line = String::new();
        loop {
            out.write_all(self.render().as_bytes())?;
            out.flush()?;
            line.clear();
            if input.read_line(&mut line)? == 0 {
                out.write_all(b"\n")?;
                return Err(io::ErrorKind::UnexpectedEof.into());
            }
            let answer = match line.trim().to_ascii_lowercase().as_str() {
                "y" | "yes" => true,
                "n" | "no" | "q" => false,
                "" => self.default,
                _ => continue,
            };
            out.write_all(self.render_answer(answer).as_bytes())?;
            return Ok(answer);
        }
    }

    /// [`ask`](Self::ask) on the process streams: prompt on stderr
    /// (stdout stays the data stream), answer from stdin, interactive only
    /// when both are terminals.
    pub fn ask_stdio(&self) -> io::Result<bool> {
        let interactive = io::stdin().is_terminal() && io::stderr().is_terminal();
        self.ask(
            &mut io::stdin().lock(),
            &mut io::stderr().lock(),
            interactive,
        )
    }
}

#[cfg(test)]
mod tests {
    use super::*;

    fn run(c: &Confirm, input: &str, interactive: bool) -> (io::Result<bool>, String) {
        let mut out = Vec::new();
        let r = c.ask(&mut input.as_bytes(), &mut out, interactive);
        (r, String::from_utf8(out).unwrap())
    }

    fn confirm() -> Confirm {
        Confirm::new("Delete?", Styler::plain())
    }

    #[test]
    fn state_accept_y() {
        let mut s = ConfirmState::new();
        assert!(!s.done());
        s.update("y");
        assert!(s.accepted() && s.done());
    }

    #[test]
    fn state_reject_n() {
        let mut s = ConfirmState::new();
        s.update("n");
        assert!(!s.accepted() && s.done());
    }

    #[test]
    fn state_reject_esc_and_q() {
        for k in ["esc", "q", "N"] {
            let mut s = ConfirmState::new();
            s.update(k);
            assert!(!s.accepted() && s.done(), "{k}");
        }
    }

    #[test]
    fn state_accept_enter() {
        let mut s = ConfirmState::new();
        s.update("enter");
        assert!(s.accepted() && s.done());
    }

    #[test]
    fn state_ignores_unrelated_key() {
        let mut s = ConfirmState::new();
        s.update("x");
        assert!(!s.done());
    }

    #[test]
    fn state_no_update_after_done() {
        let mut s = ConfirmState::new();
        s.update("y");
        s.update("n");
        assert!(s.accepted());
    }

    #[test]
    fn view_before_choice_shows_hint() {
        assert_eq!(confirm().render(), "Delete? [y/n] ");
    }

    #[test]
    fn ask_yes_and_no() {
        for (i, want) in [
            ("y\n", true),
            ("YES\n", true),
            ("n\n", false),
            ("no\n", false),
            ("q\n", false),
        ] {
            let (r, out) = run(&confirm(), i, true);
            assert_eq!(r.unwrap(), want, "{i:?}");
            assert!(out.ends_with(if want {
                "Delete? Yes\n"
            } else {
                "Delete? No\n"
            }));
        }
    }

    #[test]
    fn ask_empty_line_takes_default() {
        assert!(run(&confirm(), "\n", true).0.unwrap());
        assert!(!run(&confirm().default_answer(false), "\n", true).0.unwrap());
    }

    #[test]
    fn ask_reprompts_on_junk() {
        let (r, out) = run(&confirm(), "maybe\ny\n", true);
        assert!(r.unwrap());
        assert_eq!(out.matches("[y/n]").count(), 2);
    }

    #[test]
    fn ask_eof_is_error_not_answer() {
        let (r, _) = run(&confirm(), "", true);
        assert_eq!(r.unwrap_err().kind(), io::ErrorKind::UnexpectedEof);
    }

    #[test]
    fn non_interactive_never_reads_or_writes() {
        let (r, out) = run(&confirm().default_answer(false), "y\n", false);
        assert!(!r.unwrap());
        assert!(out.is_empty());
        assert!(run(&confirm(), "n\n", false).0.unwrap());
    }
}
