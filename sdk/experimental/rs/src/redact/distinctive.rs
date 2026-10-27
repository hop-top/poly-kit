//! Distinctive rules: every match starts with a literal of at least
//! three characters (`ghp_`, `xoxb-`, `AKIA`, `sk-ant-`). Redaction
//! trusts them without the keyword gate or the entropy floor.
//!
//! The walk mirrors `go/core/redact/distinctive.go` on this crate's
//! parse: literals count their characters, a class of at most four
//! characters (a case-fold orbit, a factored `gh[su]_`) counts as one,
//! an alternation
//! contributes its shortest branch and ends the prefix unless every
//! branch is literal entirely, a repetition with a minimum of one
//! contributes its operand's prefix and ends it, empty-width assertions
//! are skipped, anything else ends it. `contracts/redact-v1/rules.json`
//! pins the Go result for every default rule.

use regex_syntax::hir::{Class, Hir, HirKind};

const MIN_DISTINCTIVE: usize = 3;

/// Classifies a translated (`regex` crate syntax) pattern.
pub(crate) fn is_distinctive(translated: &str) -> bool {
    regex_syntax::Parser::new()
        .parse(translated)
        .map(|h| literal_prefix(&h).0 >= MIN_DISTINCTIVE)
        .unwrap_or(false)
}

/// The mandatory literal prefix length in characters, and whether the
/// node is that literal entirely.
fn literal_prefix(h: &Hir) -> (usize, bool) {
    match h.kind() {
        HirKind::Empty | HirKind::Look(_) => (0, true),
        HirKind::Literal(l) => (String::from_utf8_lossy(&l.0).chars().count(), true),
        HirKind::Class(c) if small_class(c) => (1, true),
        HirKind::Class(_) => (0, false),
        HirKind::Capture(c) => literal_prefix(&c.sub),
        HirKind::Concat(subs) => {
            let mut total = 0;
            for sub in subs {
                let (n, whole) = literal_prefix(sub);
                total += n;
                if !whole {
                    return (total, false);
                }
            }
            (total, true)
        }
        HirKind::Alternation(subs) => {
            let parts: Vec<_> = subs.iter().map(literal_prefix).collect();
            let least = parts.iter().map(|p| p.0).min().unwrap_or(0);
            (least, parts.iter().all(|p| p.1))
        }
        HirKind::Repetition(r) if r.min >= 1 => (literal_prefix(&r.sub).0, false),
        HirKind::Repetition(_) => (0, false),
    }
}

/// Whether the class holds at most four characters.
fn small_class(c: &Class) -> bool {
    let width: u32 = match c {
        Class::Unicode(u) => u
            .ranges()
            .iter()
            .map(|r| r.end() as u32 - r.start() as u32 + 1)
            .sum(),
        Class::Bytes(b) => b
            .ranges()
            .iter()
            .map(|r| u32::from(r.end() - r.start()) + 1)
            .sum(),
    };
    (1..=4).contains(&width)
}
