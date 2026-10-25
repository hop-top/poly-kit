//! String quoting for the two spec formats and the shell word that
//! starts a Usage `run` string.

use std::fmt::Write;

/// `s` as a double-quoted JS string literal. Backslash, double quote,
/// the C0 controls, DEL and the two JS line separators are escaped;
/// everything else is written as UTF-8.
pub(super) fn js_string(s: &str) -> String {
    let mut b = String::with_capacity(s.len() + 2);
    b.push('"');
    for r in s.chars() {
        match r {
            '\\' => b.push_str(r"\\"),
            '"' => b.push_str(r#"\""#),
            '\n' => b.push_str(r"\n"),
            '\r' => b.push_str(r"\r"),
            '\t' => b.push_str(r"\t"),
            '\u{2028}' | '\u{2029}' => {
                let _ = write!(b, r"\u{:04x}", r as u32);
            }
            r if (r as u32) < 0x20 || r == '\u{7f}' => {
                let _ = write!(b, r"\u{:04x}", r as u32);
            }
            r => b.push(r),
        }
    }
    b.push('"');
    b
}

/// `s` as a KDL v2 quoted string. Backslash and double quote are
/// escaped, as are the characters KDL v2 forbids or reads as a newline
/// inside a single-line string: C0 controls, DEL, NEL, the Unicode line
/// and paragraph separators, the bidi controls and the BOM. Everything
/// else is written as UTF-8.
pub(super) fn kdl_string(s: &str) -> String {
    let mut b = String::with_capacity(s.len() + 2);
    b.push('"');
    for r in s.chars() {
        match r {
            '\\' => b.push_str(r"\\"),
            '"' => b.push_str(r#"\""#),
            '\n' => b.push_str(r"\n"),
            '\r' => b.push_str(r"\r"),
            '\t' => b.push_str(r"\t"),
            '\u{8}' => b.push_str(r"\b"),
            '\u{c}' => b.push_str(r"\f"),
            r if kdl_escaped(r) => {
                let _ = write!(b, r"\u{{{:x}}}", r as u32);
            }
            r => b.push(r),
        }
    }
    b.push('"');
    b
}

fn kdl_escaped(r: char) -> bool {
    matches!(r as u32,
        0..=0x1f
        | 0x7f
        | 0x85
        | 0x200e
        | 0x200f
        | 0x202a..=0x202e
        | 0x2066..=0x2069
        | 0x2028
        | 0x2029
        | 0xfeff)
}

/// The body of a quoted literal, quotes stripped: safe inside a line
/// comment, since every line terminator in it is escaped.
pub(super) fn unquote(lit: &str) -> &str {
    &lit[1..lit.len() - 1]
}

/// `name` as one POSIX shell word: bare when it holds only characters
/// no shell treats specially, else single-quoted.
pub(super) fn shell_word(name: &str) -> String {
    let plain = !name.is_empty()
        && name
            .bytes()
            .all(|c| c.is_ascii_alphanumeric() || matches!(c, b'.' | b'_' | b'+' | b'-'));
    if plain {
        return name.to_string();
    }
    format!("'{}'", name.replace('\'', r"'\''"))
}

/// The binary name as it starts a Usage `run` string: a shell word,
/// kept out of Tera's reach in a raw block when it holds a brace.
pub(super) fn usage_bin(name: &str) -> String {
    let w = shell_word(name);
    if w.contains(['{', '}']) {
        return format!("{{% raw %}}{w}{{% endraw %}}");
    }
    w
}

#[cfg(test)]
mod tests {
    use super::super::{render_fig, render_usage, Command, Spec};
    use super::*;

    // The escapers are the part of the contract a port is likeliest to
    // get subtly wrong, so each rule is pinned on its own here as well
    // as in the shared corpus.

    #[test]
    fn js_string_rules() {
        for (input, want) in [
            ("plain é — ok", r#""plain é — ok""#),
            (r#"q" bk\"#, r#""q\" bk\\""#),
            ("nl\n cr\r tab\t", r#""nl\n cr\r tab\t""#),
            ("nul\x00 soh\x01", r#""nul\u0000 soh\u0001""#),
            ("bs\x08 ff\x0c esc\x1b", r#""bs\u0008 ff\u000c esc\u001b""#),
            ("del\x7f", r#""del\u007f""#),
            ("ls\u{2028} ps\u{2029}", "\"ls\\u2028 ps\\u2029\""),
            ("nel\u{85} bom\u{feff}", "\"nel\u{85} bom\u{feff}\""),
            ("rlo\u{202e} lri\u{2066}", "\"rlo\u{202e} lri\u{2066}\""),
            ("${x} `t` </script>", "\"${x} `t` </script>\""),
        ] {
            assert_eq!(js_string(input), want, "{input:?}");
        }
    }

    #[test]
    fn kdl_string_rules() {
        for (input, want) in [
            ("plain é — ok", r#""plain é — ok""#),
            (r#"q" bk\"#, r#""q\" bk\\""#),
            ("nl\n cr\r tab\t", r#""nl\n cr\r tab\t""#),
            ("bs\x08 ff\x0c", r#""bs\b ff\f""#),
            ("nul\x00 esc\x1b", r#""nul\u{0} esc\u{1b}""#),
            ("del\x7f nel\u{85}", r#""del\u{7f} nel\u{85}""#),
            ("lrm\u{200e} rlm\u{200f}", r#""lrm\u{200e} rlm\u{200f}""#),
            ("lre\u{202a} rlo\u{202e}", r#""lre\u{202a} rlo\u{202e}""#),
            ("lri\u{2066} pdi\u{2069}", r#""lri\u{2066} pdi\u{2069}""#),
            ("ls\u{2028} ps\u{2029}", r#""ls\u{2028} ps\u{2029}""#),
            ("bom\u{feff}", r#""bom\u{feff}""#),
            ("vt\x0b", r#""vt\u{b}""#),
            ("{} ; = # //", r#""{} ; = # //""#),
        ] {
            assert_eq!(kdl_string(input), want, "{input:?}");
        }
    }

    #[test]
    fn shell_word_rules() {
        for (input, want) in [
            ("acme", "acme"),
            ("a.b_c+d-1", "a.b_c+d-1"),
            ("x'y", r"'x'\''y'"),
            ("a b", "'a b'"),
            ("$(touch x)", "'$(touch x)'"),
            ("", "''"),
        ] {
            assert_eq!(shell_word(input), want, "{input:?}");
        }
    }

    /// The run string is a Tera template, so a binary name with a brace
    /// is kept out of Tera's reach.
    #[test]
    fn usage_bin_guards_tera() {
        assert_eq!(usage_bin("acme"), "acme");
        assert_eq!(usage_bin("a{{b}}"), r"{% raw %}'a{{b}}'{% endraw %}");
        assert_eq!(usage_bin("x'y{1}"), r"{% raw %}'x'\''y{1}'{% endraw %}");
    }

    /// The binary name in the `//` header is escaped, so a line
    /// terminator in it stays inside the comment.
    #[test]
    fn header_cannot_end_the_comment() {
        let spec = Spec {
            root: Command {
                name: "nl\nalert(1)//\u{2028}x\u{85}y".into(),
                ..Command::default()
            },
            version: String::new(),
        };
        for (out, terminators) in [
            (render_fig(&spec), &["\u{2028}"][..]),
            (render_usage(&spec), &["\u{2028}", "\u{85}"][..]),
        ] {
            let header = out.split('\n').next().unwrap();
            assert!(
                header.starts_with(r"// Generated by `nl\nalert(1)//"),
                "{header}"
            );
            for t in terminators {
                assert!(!header.contains(t), "{header:?}");
            }
        }
    }
}
