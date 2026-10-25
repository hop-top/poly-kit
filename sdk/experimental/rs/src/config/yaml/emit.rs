//! Block-style emitter following yaml.v3's style choices.
//!
//! Matches yaml.v3 on what a value reads back as: tags it drops or
//! keeps, when a string must be quoted (it would resolve to another
//! type, or plain style cannot carry it), literal blocks for multi-line
//! text. Byte layout is close but not identical: no line folding of
//! long quoted strings, folded scalars are written as literal blocks,
//! aliases are written out in full.

use super::resolve::{is_base60_float, is_old_bool, resolve, STR_TAG};
use super::{Kind, Node, Style};

/// Renders `node` as a whole document with `indent` spaces per level.
pub(crate) fn document(node: &Node, indent: usize) -> String {
    let e = Emitter { indent };
    let mut out = String::new();
    let node = node.target();
    match &node.kind {
        Kind::Map(entries) if !entries.is_empty() && node.style != Style::Flow => {
            e.block_map(&mut out, entries, 0);
        }
        Kind::Seq(items) if !items.is_empty() && node.style != Style::Flow => {
            e.block_seq(&mut out, items, 0);
        }
        _ => {
            out.push_str(&e.inline(node, false));
            out.push('\n');
        }
    }
    out
}

/// Renders the text that follows `key:` for `node`, as a value whose
/// key sits at column `col`: either ` scalar` / ` {flow}` on the same
/// line, or a newline and an indented block. No trailing newline.
#[cfg(not(target_arch = "wasm32"))]
pub(crate) fn after_key(node: &Node, col: usize, indent: usize) -> String {
    let e = Emitter { indent };
    let mut out = String::new();
    e.value_after_key(&mut out, node, col);
    while out.ends_with('\n') {
        out.pop();
    }
    out
}

/// Renders a single node in flow context (for insertion into a flow
/// collection).
#[cfg(not(target_arch = "wasm32"))]
pub(crate) fn flow(node: &Node) -> String {
    Emitter { indent: 2 }.inline(node, true)
}

/// Renders a mapping key.
#[cfg(not(target_arch = "wasm32"))]
pub(crate) fn key(node: &Node) -> String {
    Emitter { indent: 2 }.scalar_text(node, Ctx::Key, 0)
}

/// Whether `node` is written as an indented block (non-empty block
/// collection) rather than inline.
pub(crate) fn is_block(node: &Node) -> bool {
    let node = node.target();
    node.style != Style::Flow
        && match &node.kind {
            Kind::Map(e) => !e.is_empty(),
            Kind::Seq(i) => !i.is_empty(),
            _ => false,
        }
}

#[derive(Clone, Copy, PartialEq, Eq)]
enum Ctx {
    Block,
    Flow,
    Key,
}

struct Emitter {
    indent: usize,
}

impl Emitter {
    fn block_map(&self, out: &mut String, entries: &[(Node, Node)], col: usize) {
        for (k, v) in entries {
            pad(out, col);
            out.push_str(&self.scalar_or_flow_key(k));
            out.push(':');
            self.value_after_key(out, v, col);
            if !out.ends_with('\n') {
                out.push('\n');
            }
        }
    }

    fn scalar_or_flow_key(&self, k: &Node) -> String {
        match &k.target().kind {
            Kind::Scalar(_) => self.scalar_text(k.target(), Ctx::Key, 0),
            _ => format!("? {}", self.inline(k, true)),
        }
    }

    fn value_after_key(&self, out: &mut String, v: &Node, col: usize) {
        let v = v.target();
        if is_block(v) {
            if let Some(tag) = self.collection_tag(v) {
                out.push(' ');
                out.push_str(&tag);
            }
            out.push('\n');
            match &v.kind {
                Kind::Map(entries) => self.block_map(out, entries, col + self.indent),
                Kind::Seq(items) => self.block_seq(out, items, col + self.indent),
                _ => unreachable!("is_block checked the kind"),
            }
            return;
        }
        let text = match &v.kind {
            Kind::Scalar(_) => self.scalar_text(v, Ctx::Block, col + self.indent),
            _ => self.inline(v, false),
        };
        if !text.is_empty() {
            out.push(' ');
            out.push_str(&text);
        }
    }

    fn block_seq(&self, out: &mut String, items: &[Node], col: usize) {
        for item in items {
            pad(out, col);
            out.push('-');
            let item = item.target();
            match &item.kind {
                Kind::Map(entries) if is_block(item) => {
                    out.push(' ');
                    let mut inner = String::new();
                    self.block_map(&mut inner, entries, col + 2);
                    out.push_str(inner.trim_start_matches(' '));
                }
                Kind::Seq(items) if is_block(item) => {
                    out.push(' ');
                    let mut inner = String::new();
                    self.block_seq(&mut inner, items, col + 2);
                    out.push_str(inner.trim_start_matches(' '));
                }
                Kind::Scalar(_) => {
                    let text = self.scalar_text(item, Ctx::Block, col + 2);
                    if !text.is_empty() {
                        out.push(' ');
                        out.push_str(&text);
                    }
                    out.push('\n');
                }
                _ => {
                    out.push(' ');
                    out.push_str(&self.inline(item, false));
                    out.push('\n');
                }
            }
        }
    }

    fn collection_tag(&self, node: &Node) -> Option<String> {
        node.tagged.then(|| node.tag.clone())
    }

    /// Scalars and flow/empty collections on one line.
    fn inline(&self, node: &Node, in_flow: bool) -> String {
        let node = node.target();
        let ctx = if in_flow { Ctx::Flow } else { Ctx::Block };
        let body = match &node.kind {
            Kind::Scalar(_) => return self.scalar_text(node, ctx, 0),
            Kind::Map(entries) => {
                let parts: Vec<String> = entries
                    .iter()
                    .map(|(k, v)| {
                        let key = match &k.target().kind {
                            Kind::Scalar(_) => self.scalar_text(k.target(), Ctx::Key, 0),
                            _ => format!("? {}", self.inline(k, true)),
                        };
                        let val = self.inline(v, true);
                        if val.is_empty() {
                            format!("{key}:")
                        } else {
                            format!("{key}: {val}")
                        }
                    })
                    .collect();
                format!("{{{}}}", parts.join(", "))
            }
            Kind::Seq(items) => {
                let parts: Vec<String> = items.iter().map(|i| self.inline(i, true)).collect();
                format!("[{}]", parts.join(", "))
            }
            Kind::Alias(_) => unreachable!("target() resolves aliases"),
        };
        match self.collection_tag(node) {
            Some(tag) => format!("{tag} {body}"),
            None => body,
        }
    }

    /// yaml.v3 `encoder.node` for scalars, then libyaml's style
    /// selection. `col` is the indentation for literal block content.
    fn scalar_text(&self, node: &Node, ctx: Ctx, col: usize) -> String {
        let value = node.scalar_value().unwrap_or_default();
        let mut tag = node.tag.clone();
        let mut force_quoting = false;
        if !tag.is_empty() && !node.tagged {
            let quoted = matches!(
                node.style,
                Style::Single | Style::Double | Style::Literal | Style::Folded
            );
            if tag == STR_TAG && quoted {
                tag.clear();
            } else {
                let rtag = resolve("", value).map(|r| r.0).unwrap_or_default();
                if rtag == tag {
                    tag.clear();
                } else if tag == STR_TAG {
                    tag.clear();
                    force_quoting = true;
                }
            }
        }
        if node.tagged && tag == "!" {
            tag.clear();
        }
        let mut style = match node.style {
            Style::Double => Style::Double,
            Style::Single => Style::Single,
            Style::Literal | Style::Folded => Style::Literal,
            _ if value.contains('\n') => Style::Literal,
            _ if force_quoting => Style::Double,
            _ => Style::Plain,
        };
        let a = analyze(value);
        if ctx == Ctx::Key && a.multiline {
            style = Style::Double;
        }
        if style == Style::Plain {
            let allowed = if ctx == Ctx::Flow {
                a.flow_plain
            } else {
                a.block_plain
            };
            if !allowed || (value.is_empty() && ctx != Ctx::Block) {
                style = Style::Single;
            }
        }
        if style == Style::Single && !a.single_quoted {
            style = Style::Double;
        }
        if style == Style::Literal && (!a.block || ctx != Ctx::Block) {
            style = Style::Double;
        }
        let text = match style {
            Style::Plain => value.to_string(),
            Style::Single => format!("'{}'", value.replace('\'', "''")),
            Style::Double => double_quoted(value),
            _ => self.literal(value, col),
        };
        if tag.is_empty() {
            text
        } else {
            format!("{tag} {text}")
        }
    }

    fn literal(&self, value: &str, col: usize) -> String {
        let trailing = value.len() - value.trim_end_matches('\n').len();
        let chomp = match trailing {
            0 => "-",
            1 => "",
            _ => "+",
        };
        let indicator = if value.starts_with(' ') || value.starts_with('\n') {
            self.indent.to_string()
        } else {
            String::new()
        };
        let mut out = format!("|{indicator}{chomp}");
        let body = value.strip_suffix('\n').unwrap_or(value);
        for line in body.split('\n') {
            out.push('\n');
            if !line.is_empty() {
                pad(&mut out, col);
                out.push_str(line);
            }
        }
        out
    }
}

fn pad(out: &mut String, n: usize) {
    out.extend(std::iter::repeat_n(' ', n));
}

/// libyaml's printable set (4-byte UTF-8 is not printable there).
fn printable(c: char) -> bool {
    let u = c as u32;
    u == 0x0a
        || (0x20..=0x7e).contains(&u)
        || (0xa0..=0xd7ff).contains(&u)
        || ((0xe000..=0xfffd).contains(&u) && u != 0xfeff)
}

fn is_break(c: char) -> bool {
    matches!(c, '\r' | '\n' | '\u{85}' | '\u{2028}' | '\u{2029}')
}

struct Analysis {
    multiline: bool,
    flow_plain: bool,
    block_plain: bool,
    single_quoted: bool,
    block: bool,
}

/// libyaml `yaml_emitter_analyze_scalar`.
fn analyze(value: &str) -> Analysis {
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

fn double_quoted(value: &str) -> String {
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

/// yaml.v3 `stringv`: whether a Go string encodes plain (it resolves to
/// a string and is no YAML 1.1 lookalike); the caller's node then
/// carries `Style::Double` when not.
pub(crate) fn string_needs_quotes(s: &str) -> bool {
    let rtag = resolve("", s).map(|r| r.0).unwrap_or_default();
    !(rtag == STR_TAG && !(is_base60_float(s) || is_old_bool(s)))
}

#[cfg(test)]
mod tests {
    use super::super::parse;
    use super::*;

    fn str_node(s: &str) -> Node {
        Node::str(s)
    }

    #[test]
    fn quotes_what_would_retype() {
        let doc = Node::map(vec![
            (str_node("a"), str_node("0.9")),
            (str_node("b"), str_node("yes")),
            (str_node("c"), str_node("a: b")),
            (str_node("d"), str_node("")),
            (str_node("e"), str_node("it's")),
            (str_node("f"), str_node("x\ny\n")),
            (str_node("g"), Node::scalar("!!int", "5")),
            (str_node("h"), Node::null()),
        ]);
        let text = document(&doc, 2);
        assert_eq!(
            text,
            "a: \"0.9\"\nb: yes\nc: 'a: b'\nd: \"\"\ne: it's\nf: |\n  x\n  y\ng: 5\nh: null\n"
        );
        let back = parse(&text).unwrap().unwrap();
        assert_eq!(back.get("f").unwrap().scalar_value(), Some("x\ny\n"));
        assert_eq!(back.get("a").unwrap().tag, STR_TAG);
    }

    #[test]
    fn nests_blocks() {
        let doc = Node::map(vec![
            (
                str_node("m"),
                Node::map(vec![(str_node("k"), Node::scalar("!!int", "1"))]),
            ),
            (
                str_node("s"),
                Node::seq(vec![
                    str_node("a"),
                    Node::map(vec![
                        (str_node("x"), str_node("1x")),
                        (str_node("y"), str_node("2y")),
                    ]),
                ]),
            ),
            (str_node("e"), Node::map(vec![])),
        ]);
        assert_eq!(
            document(&doc, 2),
            "m:\n  k: 1\ns:\n  - a\n  - x: 1x\n    y: 2y\ne: {}\n"
        );
    }
}
