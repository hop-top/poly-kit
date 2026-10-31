//! Flag-error surfaces and opt-in autocorrect. Ports Go
//! `console/cli` `errcorrect.go`, `usage_unknown.go` and
//! `autocorrect*.go`: a parse failure becomes a USAGE envelope that
//! carries the fix, never a bare parser message.

use clap::error::{ContextKind, ContextValue, ErrorKind};
use clap::Command;

use crate::output::{CliError, CODE_OK, TRANSIENCE_PERMANENT};

use super::model::{flag_kind, value_name_of, FlagKind};

/// The farthest a typed flag name may stray from a real one before kit
/// stops guessing (Go `flagErrorMaxDistance`).
const MAX_DISTANCE: usize = 2;
/// The shortest typed name turned into one confident fix.
const MIN_SUGGEST_LEN: usize = 3;
/// The edit ceiling of an applied correction (Go
/// `autocorrectMaxDistance`).
const AUTOCORRECT_MAX_DISTANCE: usize = 1;

/// The opt-in autocorrect policy.
#[derive(Clone, Copy, Debug, PartialEq, Eq)]
pub enum AutocorrectMode {
    /// Suggest, never apply: the default.
    Off,
    /// Ask on the controlling terminal; suggest-only where there is none.
    Prompt,
    /// Apply on read-only commands.
    Read,
}

impl AutocorrectMode {
    /// Parses `off|prompt|read`, case-insensitively.
    pub fn parse(raw: &str) -> Option<AutocorrectMode> {
        match raw.trim().to_ascii_lowercase().as_str() {
            "off" => Some(AutocorrectMode::Off),
            "prompt" => Some(AutocorrectMode::Prompt),
            "read" => Some(AutocorrectMode::Read),
            _ => None,
        }
    }
}

/// A parse failure kit has classified.
pub(super) struct UsageFailure {
    pub env: CliError,
    /// The mistyped long flag name, when the failure is an unknown one.
    pub typed_flag: Option<String>,
    /// The command path words the invocation resolved to.
    pub path: Vec<String>,
}

/// `run '<path> --help' for usage`.
pub(super) fn help_invocation(path: &str) -> String {
    format!("run '{path} --help' for usage")
}

/// The command path as Go's `CommandPath()`: root name, then words.
pub(super) fn command_path(root: &str, words: &[String]) -> String {
    if words.is_empty() {
        root.to_string()
    } else {
        format!("{root} {}", words.join(" "))
    }
}

/// Walks `args` (program name first) down the tree the way clap will:
/// the command words reached, and the first word that names no child of
/// a command that has children.
pub(super) fn resolve_walk(root: &Command, args: &[String]) -> (Vec<String>, Option<String>) {
    let mut cur = root;
    let mut words = Vec::new();
    let mut i = 1;
    while i < args.len() {
        let a = &args[i];
        if a == "--" {
            break;
        }
        if let Some(rest) = a.strip_prefix("--") {
            let (name, has_eq) = match rest.split_once('=') {
                Some((n, _)) => (n, true),
                None => (rest, false),
            };
            let takes = cur
                .get_arguments()
                .find(|x| x.get_long() == Some(name))
                .is_some_and(|x| flag_kind(x).takes_value());
            i += if takes && !has_eq { 2 } else { 1 };
            continue;
        }
        if let Some(rest) = a.strip_prefix('-') {
            let c = rest.chars().next();
            let takes = c.is_some_and(|c| {
                rest.chars().count() == 1
                    && cur
                        .get_arguments()
                        .find(|x| x.get_short() == Some(c))
                        .is_some_and(|x| flag_kind(x).takes_value())
            });
            i += if takes { 2 } else { 1 };
            continue;
        }
        match cur.find_subcommand(a) {
            Some(sub) => {
                words.push(sub.get_name().to_string());
                cur = sub;
            }
            None => {
                if cur.get_subcommands().next().is_some() {
                    return (words, Some(a.clone()));
                }
                break;
            }
        }
        i += 1;
    }
    (words, None)
}

/// The command reached by `words`.
pub(super) fn find_cmd<'a>(root: &'a Command, words: &[String]) -> Option<&'a Command> {
    words.iter().try_fold(root, |cur, w| {
        cur.get_subcommands().find(|c| c.get_name() == w)
    })
}

fn ctx_str(e: &clap::Error, k: ContextKind) -> Option<String> {
    match e.get(k)? {
        ContextValue::String(s) => Some(s.clone()),
        ContextValue::Strings(v) => v.first().cloned(),
        _ => None,
    }
}

/// Classifies a clap parse failure as the USAGE envelope Go renders.
/// `None` for display requests (help, version).
pub(super) fn classify(
    root: &Command,
    root_name: &str,
    args: &[String],
    err: &clap::Error,
    enums: &dyn Fn(&[String], &str) -> Vec<String>,
) -> Option<UsageFailure> {
    let (words, unknown) = resolve_walk(root, args);
    let path = command_path(root_name, &words);
    let leaf = find_cmd(root, &words).unwrap_or(root);
    let usage = |msg: String| {
        let mut env = CliError::usage(msg);
        env.suggested_fix = help_invocation(&path);
        env
    };
    let mut typed = None;
    let env = match err.kind() {
        ErrorKind::DisplayHelp
        | ErrorKind::DisplayVersion
        | ErrorKind::DisplayHelpOnMissingArgumentOrSubcommand => return None,
        ErrorKind::InvalidSubcommand => {
            let word = unknown
                .or_else(|| ctx_str(err, ContextKind::InvalidSubcommand))
                .unwrap_or_default();
            let mut env = CliError::usage(format!(
                "unknown command {word:?} for {path:?}{}",
                command_suggestions(leaf, &word)
            ));
            // Go points at the root, whatever level the word fell at.
            env.suggested_fix = help_invocation(root_name);
            env
        }
        ErrorKind::UnknownArgument => {
            let raw = ctx_str(err, ContextKind::InvalidArg).unwrap_or_default();
            let raw = raw.split(['=', ' ']).next().unwrap_or("").to_string();
            match raw.strip_prefix("--") {
                Some(name) => {
                    typed = Some(name.to_string());
                    unknown_flag(leaf, &path, name)
                }
                None if raw.starts_with('-') => {
                    let mut env = CliError::usage(format!("unknown flag {raw}"));
                    env.cause = format!("no flag named {raw} on {path}");
                    env.suggested_fix = help_invocation(&path);
                    env
                }
                None => usage(format!("unexpected argument {raw:?} for {path:?}")),
            }
        }
        ErrorKind::InvalidValue | ErrorKind::ValueValidation
            if ctx_str(err, ContextKind::InvalidValue)
                .map(|v| v.is_empty())
                .unwrap_or(true) =>
        {
            missing_value(leaf, &words, &path, err, enums)
        }
        ErrorKind::InvalidValue | ErrorKind::ValueValidation => {
            let flag = ctx_str(err, ContextKind::InvalidArg).unwrap_or_default();
            let flag = flag.split(' ').next().unwrap_or("").to_string();
            let val = ctx_str(err, ContextKind::InvalidValue).unwrap_or_default();
            let detail = std::error::Error::source(err)
                .map(|s| format!(": {s}"))
                .unwrap_or_default();
            usage(format!(
                "invalid argument {val:?} for {flag:?} flag{detail}"
            ))
        }
        _ => {
            let text = err.render().to_string();
            let first = text
                .lines()
                .next()
                .unwrap_or("")
                .trim_start_matches("error: ");
            usage(first.to_string())
        }
    };
    Some(UsageFailure {
        env,
        typed_flag: typed,
        path: words,
    })
}

fn unknown_flag(leaf: &Command, path: &str, name: &str) -> CliError {
    let dashed = format!("--{name}");
    let mut env = CliError::usage(format!("unknown flag {dashed}"));
    env.cause = format!("no flag named {dashed} on {path}");
    let matches = suggest_flags(name, &flag_candidates(leaf));
    match matches.len() {
        0 => env.suggested_fix = help_invocation(path),
        1 if name.chars().count() < MIN_SUGGEST_LEN => {
            env.suggested_fix = help_invocation(path);
            env.alternatives = vec![format!("--{}", matches[0])];
        }
        1 => env.suggested_fix = format!("--{}", matches[0]),
        _ => {
            env.suggested_fix = help_invocation(path);
            env.alternatives = matches.iter().map(|m| format!("--{m}")).collect();
        }
    }
    env
}

fn missing_value(
    leaf: &Command,
    words: &[String],
    path: &str,
    err: &clap::Error,
    enums: &dyn Fn(&[String], &str) -> Vec<String>,
) -> CliError {
    let raw = ctx_str(err, ContextKind::InvalidArg).unwrap_or_default();
    let raw = raw.split(' ').next().unwrap_or("").to_string();
    let dashed = raw.clone();
    let mut env = CliError::usage(format!("missing value for {dashed}"));
    env.cause = format!("{dashed} requires a value");
    let arg = leaf.get_arguments().find(|a| {
        raw.strip_prefix("--")
            .is_some_and(|n| a.get_long() == Some(n))
            || raw
                .strip_prefix('-')
                .and_then(|s| s.chars().next())
                .is_some_and(|c| a.get_short() == Some(c))
    });
    let long = arg.and_then(|a| a.get_long()).unwrap_or("");
    let values = enums(words, long);
    if let Some(first) = values.first() {
        env.suggested_fix = format!("{dashed} {first}");
        env.alternatives = values.iter().map(|v| format!("{dashed} {v}")).collect();
    } else if let Some(a) = arg {
        let help = a.get_help().map(|h| h.to_string()).unwrap_or_default();
        let ph = value_name_of(a, flag_kind(a));
        env.suggested_fix = format!("{dashed} <{ph}>");
        if !help.is_empty() {
            env.cause = format!("{dashed} requires a value: {help}");
        }
    } else {
        env.suggested_fix = format!("{dashed} <value>");
    }
    let _ = path;
    env
}

/// The visible long flag names of `cmd`, sorted.
pub(super) fn flag_candidates(cmd: &Command) -> Vec<String> {
    let mut v: Vec<String> = cmd
        .get_arguments()
        .filter(|a| !a.is_positional() && !a.is_hide_set())
        .filter_map(|a| a.get_long().map(str::to_string))
        .collect();
    v.sort();
    v.dedup();
    v
}

fn command_suggestions(cmd: &Command, word: &str) -> String {
    let lower = word.to_lowercase();
    let mut names: Vec<&str> = Vec::new();
    for c in cmd.get_subcommands().filter(|c| !c.is_hide_set()) {
        let all = std::iter::once(c.get_name()).chain(c.get_all_aliases());
        let hit = all.into_iter().any(|n| {
            let n = n.to_lowercase();
            levenshtein(&lower, &n) <= 2 || n.starts_with(&lower)
        });
        if hit {
            names.push(c.get_name());
        }
    }
    names.sort_unstable();
    names.dedup();
    if names.is_empty() {
        return String::new();
    }
    let mut b = String::from("\n\nDid you mean this?\n");
    for n in names {
        b.push_str(&format!("\t{n}\n"));
    }
    b
}

/// Candidate flag names for a mistyped one, best first: a prefix match
/// shadows distance matches; otherwise the nearest tier within
/// [`MAX_DISTANCE`].
pub(super) fn suggest_flags(name: &str, candidates: &[String]) -> Vec<String> {
    if name.is_empty() || candidates.is_empty() {
        return Vec::new();
    }
    let lower = name.to_lowercase();
    let prefixed: Vec<String> = candidates
        .iter()
        .filter(|c| c.as_str() != name && c.to_lowercase().starts_with(&lower))
        .cloned()
        .collect();
    if !prefixed.is_empty() {
        return prefixed;
    }
    let mut near: Vec<(usize, &String)> = candidates
        .iter()
        .filter_map(|c| {
            let d = levenshtein(&lower, &c.to_lowercase());
            (d > 0 && d <= MAX_DISTANCE).then_some((d, c))
        })
        .collect();
    near.sort();
    let Some(best) = near.first().map(|n| n.0) else {
        return Vec::new();
    };
    near.into_iter()
        .take_while(|n| n.0 == best)
        .map(|n| n.1.clone())
        .collect()
}

/// The one flag an applied correction rewrites `typed` to, if any.
pub(super) fn correction_candidate(typed: &str, candidates: &[String]) -> Option<String> {
    if typed.chars().count() < MIN_SUGGEST_LEN {
        return None;
    }
    let m = suggest_flags(typed, candidates);
    if m.len() != 1 {
        return None;
    }
    let hit = &m[0];
    let (lt, lh) = (typed.to_lowercase(), hit.to_lowercase());
    (lh.starts_with(&lt) || levenshtein(&lt, &lh) <= AUTOCORRECT_MAX_DISTANCE).then(|| hit.clone())
}

/// Rewrites the flag token naming `typed` in `args`, up to `--`.
pub(super) fn correct_args(args: &[String], typed: &str, fixed: &str) -> Option<Vec<String>> {
    let mut out = args.to_vec();
    let mut changed = false;
    for a in out.iter_mut() {
        if a == "--" {
            break;
        }
        if *a == format!("--{typed}") {
            *a = format!("--{fixed}");
            changed = true;
        } else if let Some(rest) = a.strip_prefix(&format!("--{typed}=")) {
            *a = format!("--{fixed}={rest}");
            changed = true;
        }
    }
    changed.then_some(out)
}

/// The record of an applied rewrite: an OK envelope, so a caller parses
/// it with the decoder it uses for the suggest-only one.
pub(super) fn correction_notice(typed: &str, fixed: &str) -> CliError {
    CliError {
        code: CODE_OK.into(),
        message: format!("applied flag correction: --{typed} -> --{fixed}"),
        corrected_from: format!("--{typed}"),
        suggested_fix: format!("--{fixed}"),
        exit_code: 0,
        transience: TRANSIENCE_PERMANENT.into(),
        ..CliError::default()
    }
}

/// Edit distance, rune-indexed.
pub(super) fn levenshtein(a: &str, b: &str) -> usize {
    let (a, b): (Vec<char>, Vec<char>) = (a.chars().collect(), b.chars().collect());
    let (a, b) = if a.len() < b.len() { (b, a) } else { (a, b) };
    if b.is_empty() {
        return a.len();
    }
    let mut prev: Vec<usize> = (0..=b.len()).collect();
    let mut curr = vec![0; b.len() + 1];
    for i in 1..=a.len() {
        curr[0] = i;
        for j in 1..=b.len() {
            let cost = usize::from(a[i - 1] != b[j - 1]);
            curr[j] = (prev[j] + 1).min(curr[j - 1] + 1).min(prev[j - 1] + cost);
        }
        std::mem::swap(&mut prev, &mut curr);
    }
    prev[b.len()]
}

/// Whether a kind takes a value, re-exported for the walk.
#[allow(dead_code)]
pub(super) fn kind_takes_value(k: FlagKind) -> bool {
    k.takes_value()
}

#[cfg(test)]
mod tests {
    use super::*;

    fn names(v: &[&str]) -> Vec<String> {
        v.iter().map(|s| s.to_string()).collect()
    }

    #[test]
    fn a_prefix_match_shadows_distance_matches() {
        // "verbos" is 1 edit from "verbose" and 2 from "verbs"; the
        // prefix hit alone is offered.
        let got = suggest_flags("verbos", &names(&["verbs", "verbose", "verboten"]));
        assert_eq!(got, ["verbose"]);
    }

    #[test]
    fn the_nearest_distance_tier_wins() {
        let got = suggest_flags("forma", &names(&["format", "formal", "foo"]));
        // Both prefix-match first.
        assert_eq!(got, ["format", "formal"]);
        let got = suggest_flags("fromat", &names(&["format", "forms", "fooooo"]));
        assert_eq!(got, ["format"], "distance 2 beats nothing, 1 is not worse");
        let tie = suggest_flags("xat", &names(&["cat", "hat", "chat"]));
        assert_eq!(tie, ["cat", "hat"], "ties keep every best, in order");
    }

    #[test]
    fn beyond_the_ceiling_nothing_is_offered() {
        assert!(suggest_flags("zzzzzz", &names(&["format", "quiet"])).is_empty());
        assert!(suggest_flags("", &names(&["format"])).is_empty());
        assert!(suggest_flags("format", &[]).is_empty());
    }

    #[test]
    fn matching_ignores_case_and_skips_the_exact_name() {
        assert_eq!(suggest_flags("FORMA", &names(&["format"])), ["format"]);
        assert!(suggest_flags("format", &names(&["format"])).is_empty());
    }

    #[test]
    fn autocorrect_applies_only_within_one_edit() {
        let c = names(&["format", "quiet"]);
        assert_eq!(correction_candidate("fromat", &c), None, "two edits");
        assert_eq!(correction_candidate("formt", &c).as_deref(), Some("format"));
        assert_eq!(correction_candidate("zzz", &c), None);
    }

    #[test]
    fn levenshtein_counts_runes() {
        assert_eq!(levenshtein("kitten", "sitting"), 3);
        assert_eq!(levenshtein("", "abc"), 3);
        assert_eq!(levenshtein("héllo", "hello"), 1);
    }
}
