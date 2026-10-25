//! Go-compatible quoting for error text.
//!
//! Go formats the offending input with `%q` (`strconv.Quote` for strings,
//! `strconv.QuoteRune` for runes). Rust's `{:?}` agrees for printable
//! text but spells control and non-printable characters differently
//! (`\0`, `\u{7f}`), so error text built with it would drift from the
//! shared vectors.
//!
//! "Printable" follows Go's `unicode.IsPrint`. Rust's standard library has
//! no general-category tables, so this treats as non-printable: control
//! characters, whitespace other than the ASCII space, the common format
//! characters (`Cf`), private-use code points and noncharacters.
//! Unassigned code points are not detected.

use std::fmt::Write;

/// Quotes `s` like Go's `%q` on a string.
pub(crate) fn quote(s: &str) -> String {
    let mut out = String::with_capacity(s.len() + 2);
    out.push('"');
    for c in s.chars() {
        escape(&mut out, c, '"');
    }
    out.push('"');
    out
}

/// Quotes `c` like Go's `%q` on a rune.
pub(crate) fn quote_char(c: char) -> String {
    let mut out = String::from("'");
    escape(&mut out, c, '\'');
    out.push('\'');
    out
}

fn escape(out: &mut String, c: char, quote: char) {
    match c {
        c if c == quote || c == '\\' => {
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

fn is_print(c: char) -> bool {
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
        let cases = [
            ("plain", "\"plain\""),
            ("a\"b\\c", "\"a\\\"b\\\\c\""),
            ("'", "\"'\""),
            ("\u{7}\u{8}\u{c}\n\r\t\u{b}", "\"\\a\\b\\f\\n\\r\\t\\v\""),
            ("\u{0}\u{1f}\u{7f}", "\"\\x00\\x1f\\x7f\""),
            ("\u{a0}\u{200b}\u{2028}", "\"\\u00a0\\u200b\\u2028\""),
            ("\u{e000}\u{feff}", "\"\\ue000\\ufeff\""),
            ("\u{10ffff}", "\"\\U0010ffff\""),
            ("é日🙂 ", "\"é日🙂 \""),
        ];
        for (input, want) in cases {
            assert_eq!(quote(input), want, "{input:?}");
        }
        assert_eq!(quote_char('x'), "'x'");
        assert_eq!(quote_char('\''), "'\\''");
        assert_eq!(quote_char('"'), "'\"'");
        assert_eq!(quote_char('\u{0}'), "'\\x00'");
    }
}
