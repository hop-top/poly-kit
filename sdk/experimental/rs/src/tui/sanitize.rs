//! One sanitizer for every caller-supplied string a renderer interpolates.

use std::borrow::Cow;

/// Replaces every control char (C0, DEL, C1, which includes ESC, CSI
/// U+009B, CR and LF) with U+FFFD, so text cannot start an escape
/// sequence, move the cursor or rewrite earlier output. Callers pass
/// single-line text; a newline is a control char here too. Borrowed
/// when nothing needs replacing.
pub(crate) fn clean(s: &str) -> Cow<'_, str> {
    if !s.chars().any(char::is_control) {
        return Cow::Borrowed(s);
    }
    Cow::Owned(
        s.chars()
            .map(|c| if c.is_control() { '\u{FFFD}' } else { c })
            .collect(),
    )
}

#[cfg(test)]
pub(crate) const HOSTILE: [&str; 3] = ["\x1b[2J", "a\rb", "\u{9b}31m"];

#[cfg(test)]
pub(crate) fn assert_inert(out: &str) {
    for c in out.chars() {
        assert!(
            !c.is_control() || c == '\n',
            "control char {c:?} in {out:?}"
        );
    }
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn replaces_controls() {
        assert_eq!(clean("\x1b[2J"), "\u{FFFD}[2J");
        assert_eq!(clean("a\rb"), "a\u{FFFD}b");
        assert_eq!(clean("\u{9b}31m"), "\u{FFFD}31m");
        assert_eq!(clean("a\nb\x7f\0"), "a\u{FFFD}b\u{FFFD}\u{FFFD}");
    }

    #[test]
    fn plain_and_unicode_untouched_and_borrowed() {
        for s in ["", "héllo ✓ 日本", "[phase] x"] {
            assert!(matches!(clean(s), Cow::Borrowed(_)));
            assert_eq!(clean(s), s);
        }
    }
}
