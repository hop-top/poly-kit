//! Go `%q` quoting for error text, so messages match the Go port.
//!
//! Same rules as the bus module's helper (which is private to it):
//! control characters, `"`, `\` and non-printable runes escape the Go
//! way. "Printable" approximates Go's `unicode.IsPrint` without
//! general-category tables; unassigned code points are not detected.

use std::fmt::Write;

/// Quotes `s` like Go's `%q` on a string.
pub(crate) fn quote(s: &str) -> String {
    let mut out = String::with_capacity(s.len() + 2);
    out.push('"');
    for c in s.chars() {
        match c {
            '"' | '\\' => {
                out.push('\\');
                out.push(c);
            }
            '\u{07}' => out.push_str("\\a"),
            '\u{08}' => out.push_str("\\b"),
            '\u{0c}' => out.push_str("\\f"),
            '\n' => out.push_str("\\n"),
            '\r' => out.push_str("\\r"),
            '\t' => out.push_str("\\t"),
            '\u{0b}' => out.push_str("\\v"),
            c if (c as u32) < 0x20 || c as u32 == 0x7f => {
                let _ = write!(out, "\\x{:02x}", c as u32);
            }
            c if is_print(c) => out.push(c),
            c if (c as u32) < 0x10000 => {
                let _ = write!(out, "\\u{:04x}", c as u32);
            }
            c => {
                let _ = write!(out, "\\U{:08x}", c as u32);
            }
        }
    }
    out.push('"');
    out
}

/// Approximates Go's `unicode.IsPrint`.
pub(crate) fn is_print(c: char) -> bool {
    if c == ' ' {
        return true;
    }
    if c.is_control() || c.is_whitespace() {
        return false;
    }
    !matches!(
        c as u32,
        0x00ad
            | 0x0600..=0x0605
            | 0x061c
            | 0x06dd
            | 0x070f
            | 0x180e
            | 0x200b..=0x200f
            | 0x202a..=0x202e
            | 0x2060..=0x2064
            | 0x2066..=0x206f
            | 0xd800..=0xf8ff
            | 0xfdd0..=0xfdef
            | 0xfeff
            | 0xfff9..=0xfffb
            | 0xfffe..=0xffff
            | 0xf0000..=0x10ffff
    ) && (c as u32 & 0xfffe) != 0xfffe
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn matches_go_quote() {
        assert_eq!(quote("plain"), "\"plain\"");
        assert_eq!(quote("a\"b\\c"), "\"a\\\"b\\\\c\"");
        assert_eq!(quote("\u{0}\n\t"), "\"\\x00\\n\\t\"");
        assert_eq!(quote("\u{a0}"), "\"\\u00a0\"");
        assert_eq!(quote("é日🙂 "), "\"é日🙂 \"");
    }
}
