//! libyaml's scalar analysis (which styles a value may take) and its
//! double-quoted escaping.

/// libyaml's printable set (4-byte UTF-8 is not printable there).
pub(super) fn printable(c: char) -> bool {
    let u = c as u32;
    u == 0x0a
        || (0x20..=0x7e).contains(&u)
        || (0xa0..=0xd7ff).contains(&u)
        || ((0xe000..=0xfffd).contains(&u) && u != 0xfeff)
}

fn is_break(c: char) -> bool {
    matches!(c, '\r' | '\n' | '\u{85}' | '\u{2028}' | '\u{2029}')
}

pub(super) struct Analysis {
    pub multiline: bool,
    pub flow_plain: bool,
    pub block_plain: bool,
    pub single_quoted: bool,
    pub block: bool,
}

/// libyaml `yaml_emitter_analyze_scalar`.
pub(super) fn analyze(value: &str) -> Analysis {
    if value.is_empty() {
        return Analysis {
            multiline: false,
            flow_plain: false,
            block_plain: true,
            single_quoted: true,
            block: false,
        };
    }
    let chars: Vec<char> = value.chars().collect();
    let (mut block_ind, mut flow_ind) = (false, false);
    if value.starts_with("---") || value.starts_with("...") {
        block_ind = true;
        flow_ind = true;
    }
    let (mut line_breaks, mut special, mut tabs) = (false, false, false);
    let (mut leading_space, mut leading_break) = (false, false);
    let (mut trailing_space, mut trailing_break) = (false, false);
    let (mut break_space, mut space_break) = (false, false);
    let (mut prev_space, mut prev_break) = (false, false);
    let mut preceded_by_ws = true;
    for (i, &c) in chars.iter().enumerate() {
        let followed_by_ws = chars
            .get(i + 1)
            .is_none_or(|n| *n == ' ' || *n == '\t' || is_break(*n));
        if i == 0 {
            match c {
                '#' | ',' | '[' | ']' | '{' | '}' | '&' | '*' | '!' | '|' | '>' | '\'' | '"'
                | '%' | '@' | '`' => {
                    flow_ind = true;
                    block_ind = true;
                }
                '?' | ':' => {
                    flow_ind = true;
                    if followed_by_ws {
                        block_ind = true;
                    }
                }
                '-' if followed_by_ws => {
                    flow_ind = true;
                    block_ind = true;
                }
                _ => {}
            }
        } else {
            match c {
                ',' | '?' | '[' | ']' | '{' | '}' => flow_ind = true,
                ':' => {
                    flow_ind = true;
                    if followed_by_ws {
                        block_ind = true;
                    }
                }
                '#' if preceded_by_ws => {
                    flow_ind = true;
                    block_ind = true;
                }
                _ => {}
            }
        }
        if c == '\t' {
            tabs = true;
        } else if !printable(c) {
            special = true;
        }
        if c == ' ' {
            if i == 0 {
                leading_space = true;
            }
            if i + 1 == chars.len() {
                trailing_space = true;
            }
            if prev_break {
                break_space = true;
            }
            prev_space = true;
            prev_break = false;
        } else if is_break(c) {
            line_breaks = true;
            if i == 0 {
                leading_break = true;
            }
            if i + 1 == chars.len() {
                trailing_break = true;
            }
            if prev_space {
                space_break = true;
            }
            prev_space = false;
            prev_break = true;
        } else {
            prev_space = false;
            prev_break = false;
        }
        preceded_by_ws = c == ' ' || c == '\t' || is_break(c);
    }
    let mut a = Analysis {
        multiline: line_breaks,
        flow_plain: true,
        block_plain: true,
        single_quoted: true,
        block: true,
    };
    if leading_space || leading_break || trailing_space || trailing_break {
        a.flow_plain = false;
        a.block_plain = false;
    }
    if trailing_space {
        a.block = false;
    }
    if break_space || space_break || tabs || special {
        a.flow_plain = false;
        a.block_plain = false;
        a.single_quoted = false;
    }
    if space_break || special {
        a.block = false;
    }
    if line_breaks {
        a.flow_plain = false;
        a.block_plain = false;
    }
    if flow_ind {
        a.flow_plain = false;
    }
    if block_ind {
        a.block_plain = false;
    }
    a
}

pub(super) fn double_quoted(value: &str) -> String {
    let mut out = String::from("\"");
    for c in value.chars() {
        match c {
            '"' => out.push_str("\\\""),
            '\\' => out.push_str("\\\\"),
            '\0' => out.push_str("\\0"),
            '\u{07}' => out.push_str("\\a"),
            '\u{08}' => out.push_str("\\b"),
            '\t' => out.push_str("\\t"),
            '\n' => out.push_str("\\n"),
            '\u{0b}' => out.push_str("\\v"),
            '\u{0c}' => out.push_str("\\f"),
            '\r' => out.push_str("\\r"),
            '\u{1b}' => out.push_str("\\e"),
            '\u{85}' => out.push_str("\\N"),
            '\u{a0}' => out.push_str("\\_"),
            '\u{2028}' => out.push_str("\\L"),
            '\u{2029}' => out.push_str("\\P"),
            c if printable(c) && c != '\u{feff}' => out.push(c),
            c if (c as u32) <= 0xff => out.push_str(&format!("\\x{:02X}", c as u32)),
            c if (c as u32) <= 0xffff => out.push_str(&format!("\\u{:04X}", c as u32)),
            c => out.push_str(&format!("\\U{:08X}", c as u32)),
        }
    }
    out.push('"');
    out
}
