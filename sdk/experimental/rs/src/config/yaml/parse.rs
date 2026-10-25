//! Source text to [`Node`] tree, keeping byte spans for the editor.

use std::collections::HashMap;
use std::fmt;
use std::sync::Arc;

use saphyr_parser::{Event, Parser, ScalarStyle, Span, Tag};

use super::resolve::{resolve, MAP_TAG, MERGE_TAG, SEQ_TAG, STR_TAG};
use super::{Kind, Node, Style};

/// A syntax error, rendered like yaml.v3's (`yaml: line N: ...`).
#[derive(Clone, Debug, PartialEq, Eq)]
pub(crate) struct ParseError {
    pub line: usize,
    pub msg: String,
}

impl fmt::Display for ParseError {
    fn fmt(&self, f: &mut fmt::Formatter<'_>) -> fmt::Result {
        if self.line == 0 {
            write!(f, "yaml: {}", self.msg)
        } else {
            write!(f, "yaml: line {}: {}", self.line, self.msg)
        }
    }
}

impl std::error::Error for ParseError {}

enum Frame {
    Seq {
        node: Node,
        anchor: usize,
        items: Vec<Node>,
    },
    Map {
        node: Node,
        anchor: usize,
        entries: Vec<(Node, Node)>,
        key: Option<Node>,
    },
}

/// A parse failure with the byte offset the parser reported.
struct Failure {
    err: ParseError,
    at: usize,
}

/// Retry budget for [`repair`]: one per tab separator or under-indented
/// quoted line fixed.
const MAX_REPAIRS: usize = 10_000;

/// Parses the first document of `src`. `Ok(None)` for a stream with no
/// document (empty or comments only).
///
/// Three inputs libyaml (and so Go) reads but saphyr rejects are made
/// readable first, without changing any value: a leading UTF-8 BOM is
/// skipped; a tab after a `:` indicator becomes a space; continuation
/// lines of a quoted scalar indented at or left of the enclosing block
/// get leading spaces (which folding drops). Spans always point into
/// `src` as given.
pub(crate) fn parse(src: &str) -> Result<Option<Node>, ParseError> {
    let (body, base) = match src.strip_prefix('\u{feff}') {
        Some(rest) => (rest, src.len() - rest.len()),
        None => (src, 0),
    };
    let mut text = body.to_string();
    let mut inserts: Vec<(usize, usize)> = Vec::new();
    for _ in 0..=MAX_REPAIRS {
        match parse_text(&text) {
            Ok(mut root) => {
                if base > 0 || !inserts.is_empty() {
                    if let Some(r) = root.as_mut() {
                        remap(r, &|m| to_orig(m, &inserts) + base);
                    }
                }
                return Ok(root);
            }
            Err(f) => {
                if !repair(&mut text, &mut inserts, &f) {
                    return Err(f.err);
                }
            }
        }
    }
    parse_text(&text).map_err(|f| f.err)
}

/// Offset in the unrepaired text for offset `m` in the repaired one.
/// `inserts` holds `(original offset, spaces inserted there)`, sorted.
fn to_orig(m: usize, inserts: &[(usize, usize)]) -> usize {
    let mut shift = 0;
    for &(p, n) in inserts {
        let at = p + shift;
        if m >= at + n {
            shift += n;
        } else if m >= at {
            return p;
        } else {
            break;
        }
    }
    m - shift
}

/// Spans of the tree (not of alias targets, which nothing edits).
fn remap(n: &mut Node, f: &dyn Fn(usize) -> usize) {
    if let Some((s, e)) = n.span {
        n.span = Some((f(s), f(e)));
    }
    match &mut n.kind {
        Kind::Seq(items) => items.iter_mut().for_each(|i| remap(i, f)),
        Kind::Map(entries) => entries.iter_mut().for_each(|(k, v)| {
            remap(k, f);
            remap(v, f);
        }),
        Kind::Scalar(_) | Kind::Alias(_) => {}
    }
}

/// Rewrites `text` so the failure `f` does not recur, if it is one of
/// the libyaml-accepted cases; `false` otherwise.
fn repair(text: &mut String, inserts: &mut Vec<(usize, usize)>, f: &Failure) -> bool {
    let b = text.as_bytes();
    match f.err.msg.as_str() {
        // The marker sits after the whitespace run that follows `:`.
        "':' must be followed by a valid YAML whitespace" => {
            let mut i = f.at.min(b.len());
            while i > 0 && matches!(b[i - 1], b' ' | b'\t') {
                i -= 1;
            }
            if i == 0 || b[i - 1] != b':' || !b[i..f.at.min(b.len())].contains(&b'\t') {
                return false;
            }
            let end = f.at.min(b.len());
            let fixed = text[i..end].replace('\t', " ");
            text.replace_range(i..end, &fixed);
            true
        }
        // The marker is the opening quote.
        "invalid indentation in quoted scalar" => {
            let q = f.at;
            let Some(&quote) = b.get(q) else {
                return false;
            };
            if quote != b'"' && quote != b'\'' {
                return false;
            }
            let close = quoted_end(b, q);
            let line_start = b[..q]
                .iter()
                .rposition(|c| *c == b'\n')
                .map_or(0, |i| i + 1);
            let need = q - line_start + 1;
            let mut at: Vec<(usize, usize)> = Vec::new();
            for i in q..close {
                if b[i] != b'\n' {
                    continue;
                }
                let ls = i + 1;
                let ws = b[ls..].iter().take_while(|c| **c == b' ').count();
                if ws < need {
                    at.push((ls, need - ws));
                }
            }
            if at.is_empty() {
                return false;
            }
            for &(ls, n) in at.iter().rev() {
                let orig = to_orig(ls, inserts);
                match inserts.binary_search_by_key(&orig, |&(p, _)| p) {
                    Ok(k) => inserts[k].1 += n,
                    Err(k) => inserts.insert(k, (orig, n)),
                }
                text.insert_str(ls, &" ".repeat(n));
            }
            true
        }
        _ => false,
    }
}

/// End (exclusive) of the quoted scalar opening at `q`, or the text end.
fn quoted_end(b: &[u8], q: usize) -> usize {
    let quote = b[q];
    let mut i = q + 1;
    while i < b.len() {
        match (quote, b[i]) {
            (b'"', b'\\') => i += 2,
            (b'"', b'"') => return i + 1,
            (b'\'', b'\'') if b.get(i + 1) == Some(&b'\'') => i += 2,
            (b'\'', b'\'') => return i + 1,
            _ => i += 1,
        }
    }
    b.len()
}

fn parse_text(src: &str) -> Result<Option<Node>, Failure> {
    let offsets: Vec<usize> = src
        .char_indices()
        .map(|(b, _)| b)
        .chain(std::iter::once(src.len()))
        .collect();
    let byte = |m: usize| offsets.get(m).copied().unwrap_or(src.len());
    let span_of = |s: &Span| (byte(s.start.index()), byte(s.end.index()));
    let fail = |err: ParseError| Failure { err, at: 0 };

    let mut parser = Parser::new_from_str(src);
    let mut stack: Vec<Frame> = Vec::new();
    let mut anchors: HashMap<usize, Arc<Node>> = HashMap::new();
    let mut root: Option<Node> = None;
    let mut in_doc = false;

    while let Some(next) = parser.next_event() {
        let (event, span) = next.map_err(|e| Failure {
            err: ParseError {
                line: e.marker().line(),
                msg: e.info().to_string(),
            },
            at: byte(e.marker().index()),
        })?;
        let line = span.start.line();
        let (start, end) = span_of(&span);
        let done = match event {
            Event::DocumentStart(_) => {
                in_doc = true;
                None
            }
            Event::DocumentEnd => {
                if in_doc {
                    break;
                }
                None
            }
            Event::StreamEnd => break,
            Event::Scalar(value, style, anchor, tag) => {
                let mut node = Node::new(Kind::Scalar(value.to_string()), "");
                node.style = match style {
                    ScalarStyle::Plain => Style::Plain,
                    ScalarStyle::SingleQuoted => Style::Single,
                    ScalarStyle::DoubleQuoted => Style::Double,
                    ScalarStyle::Literal => Style::Literal,
                    ScalarStyle::Folded => Style::Folded,
                };
                node.line = line;
                node.span = Some(scalar_span(src, node.style, start, end));
                set_tag(&mut node, tag.as_deref(), line).map_err(fail)?;
                Some((node, anchor))
            }
            Event::Alias(id) => {
                let target = anchors.get(&id).cloned().ok_or_else(|| {
                    fail(ParseError {
                        line,
                        msg: "unknown anchor referenced".into(),
                    })
                })?;
                let tag = target.tag.clone();
                let mut node = Node::new(Kind::Alias(target), &tag);
                node.line = line;
                node.span = Some((start, end));
                Some((node, 0))
            }
            Event::SequenceStart(anchor, tag) => {
                let mut node = Node::new(Kind::Seq(Vec::new()), SEQ_TAG);
                node.line = line;
                node.span = Some((start, start));
                node.style = flow_style(src, start, b'[');
                set_tag(&mut node, tag.as_deref(), line).map_err(fail)?;
                stack.push(Frame::Seq {
                    node,
                    anchor,
                    items: Vec::new(),
                });
                None
            }
            Event::MappingStart(anchor, tag) => {
                let mut node = Node::new(Kind::Map(Vec::new()), MAP_TAG);
                node.line = line;
                node.span = Some((start, start));
                node.style = flow_style(src, start, b'{');
                set_tag(&mut node, tag.as_deref(), line).map_err(fail)?;
                stack.push(Frame::Map {
                    node,
                    anchor,
                    entries: Vec::new(),
                    key: None,
                });
                None
            }
            Event::SequenceEnd => match stack.pop() {
                Some(Frame::Seq {
                    mut node,
                    anchor,
                    items,
                }) => {
                    let last = items.last().and_then(|n| n.span).map(|s| s.1);
                    let first = items.first().and_then(|n| n.span).map(|s| s.0);
                    close_span(&mut node, first, last, flow_close(src, start, end));
                    node.kind = Kind::Seq(items);
                    Some((node, anchor))
                }
                _ => return Err(fail(internal(line))),
            },
            Event::MappingEnd => match stack.pop() {
                Some(Frame::Map {
                    mut node,
                    anchor,
                    entries,
                    ..
                }) => {
                    let last = entries.last().and_then(|(_, v)| v.span).map(|s| s.1);
                    let first = entries.first().and_then(|(k, _)| k.span).map(|s| s.0);
                    close_span(&mut node, first, last, flow_close(src, start, end));
                    node.kind = Kind::Map(entries);
                    Some((node, anchor))
                }
                _ => return Err(fail(internal(line))),
            },
            Event::Nothing | Event::StreamStart => None,
        };
        if let Some((node, anchor)) = done {
            if anchor != 0 {
                anchors.insert(anchor, Arc::new(node.clone()));
            }
            match stack.last_mut() {
                None => root = Some(node),
                Some(Frame::Seq { items, .. }) => items.push(node),
                Some(Frame::Map { entries, key, .. }) => match key.take() {
                    None => {
                        reject_implicit_empty_key(src, &node).map_err(fail)?;
                        *key = Some(node)
                    }
                    Some(k) => entries.push((k, node)),
                },
            }
        }
    }
    Ok(root)
}

/// libyaml has no implicit empty keys (`: v` at the start of an entry);
/// YAML 1.2 parsers accept them. Reject like yaml.v3 does. An explicit
/// `? ` key stays valid.
fn reject_implicit_empty_key(src: &str, key: &Node) -> Result<(), ParseError> {
    let Some((start, end)) = key.span else {
        return Ok(());
    };
    let empty_plain = key.style == Style::Plain && key.scalar_value() == Some("");
    if !empty_plain || start != end || src.as_bytes().get(start) != Some(&b':') {
        return Ok(());
    }
    let explicit = src[..start]
        .trim_end_matches([' ', '\t', '\r', '\n'])
        .ends_with('?');
    if explicit {
        return Ok(());
    }
    Err(ParseError {
        line: 0,
        msg: "did not find expected key".into(),
    })
}

fn internal(line: usize) -> ParseError {
    ParseError {
        line,
        msg: "unbalanced collection events".into(),
    }
}

/// A flow collection's end event spans from its bracket to the line
/// end (taking in a trailing comment); end it after the bracket.
fn flow_close(src: &str, start: usize, end: usize) -> usize {
    match src.as_bytes().get(start) {
        Some(b']' | b'}') => start + 1,
        _ => end,
    }
}

fn flow_style(src: &str, start: usize, open: u8) -> Style {
    if src.as_bytes().get(start) == Some(&open) {
        Style::Flow
    } else {
        Style::Plain
    }
}

/// Block collections end where their last child ends; flow ones at the
/// closing bracket.
fn close_span(node: &mut Node, first: Option<usize>, last: Option<usize>, end_event: usize) {
    let (start, _) = node.span.unwrap_or((0, 0));
    if node.style == Style::Flow {
        node.span = Some((start, end_event.max(start)));
        return;
    }
    let start = first.map_or(start, |f| f.min(start));
    node.span = Some((start, last.unwrap_or(start)));
}

/// The parser's scalar spans run to the line end for quoted scalars
/// (taking in a trailing comment) and cover only the content of block
/// scalars. Narrow quoted ones to the closing quote; widen block ones
/// to their `|`/`>` indicator and drop trailing line breaks.
fn scalar_span(src: &str, style: Style, start: usize, end: usize) -> (usize, usize) {
    let b = src.as_bytes();
    match style {
        Style::Double | Style::Single
            if b.get(start).is_some_and(|c| *c == b'"' || *c == b'\'') =>
        {
            let mut i = start + 1;
            while i < b.len() {
                match (style, b[i]) {
                    (Style::Double, b'\\') => i += 2,
                    (Style::Double, b'"') => return (start, i + 1),
                    (Style::Single, b'\'') if b.get(i + 1) == Some(&b'\'') => i += 2,
                    (Style::Single, b'\'') => return (start, i + 1),
                    _ => i += 1,
                }
            }
            (start, end)
        }
        Style::Literal | Style::Folded => {
            let ind = src[..start].rfind(['|', '>']).unwrap_or(start);
            let body = &src[start.max(ind)..end.max(start)];
            (ind, start.max(ind) + body.trim_end().len())
        }
        _ => (start, end),
    }
}

fn tag_text(tag: &Tag) -> String {
    if tag.handle == "tag:yaml.org,2002:" || tag.handle == "!!" {
        return format!("!!{}", tag.suffix);
    }
    format!("{}{}", tag.handle, tag.suffix)
}

/// yaml.v3 `parser.node`: an explicit tag (other than `!`) is kept and
/// marks the node tagged; quoted and block scalars default to `!!str`;
/// plain scalars resolve.
fn set_tag(node: &mut Node, tag: Option<&Tag>, line: usize) -> Result<(), ParseError> {
    let explicit = tag.map(tag_text).unwrap_or_default();
    if !explicit.is_empty() && explicit != "!" {
        node.tag = super::resolve::short_tag(&explicit);
        node.tagged = true;
        return Ok(());
    }
    if let Kind::Scalar(v) = &node.kind {
        node.tag = if node.style == Style::Plain && v == "<<" {
            MERGE_TAG.to_string()
        } else if node.style == Style::Plain {
            resolve("", v).map_err(|msg| ParseError { line, msg })?.0
        } else {
            STR_TAG.to_string()
        };
    }
    Ok(())
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn parses_tags_styles_and_spans() {
        let src = "a: 1\nb: 'x'\nc:\n  d: [1, 2]\ne: !!str 5\n";
        let root = parse(src).unwrap().unwrap();
        let a = root.get("a").unwrap();
        assert_eq!(a.tag, "!!int");
        assert_eq!(&src[a.span.unwrap().0..a.span.unwrap().1], "1");
        let b = root.get("b").unwrap();
        assert_eq!((b.tag.as_str(), b.style), ("!!str", Style::Single));
        let d = root.get("c").unwrap().get("d").unwrap();
        assert_eq!(d.style, Style::Flow);
        assert_eq!(&src[d.span.unwrap().0..d.span.unwrap().1], "[1, 2]");
        let c = root.get("c").unwrap();
        assert_eq!(&src[c.span.unwrap().0..c.span.unwrap().1], "d: [1, 2]");
        let e = root.get("e").unwrap();
        assert!(e.tagged);
        assert_eq!(e.tag, "!!str");
    }

    #[test]
    fn aliases_copy_the_anchor() {
        let root = parse("a: &x {k: v}\nb: *x\n").unwrap().unwrap();
        let b = root.get("b").unwrap();
        assert!(matches!(b.kind, Kind::Alias(_)));
        assert_eq!(b.target().get("k").unwrap().scalar_value(), Some("v"));
    }

    #[test]
    fn implicit_empty_key_rejected_like_libyaml() {
        assert_eq!(
            parse(":\n  - oops").unwrap_err().to_string(),
            "yaml: did not find expected key"
        );
        assert!(parse("? \n: v\n").is_ok());
    }

    #[test]
    fn empty_and_errors() {
        assert!(parse("").unwrap().is_none());
        assert!(parse("# only\n").unwrap().is_none());
        let err = parse("a: b: c\n").unwrap_err();
        assert!(err.to_string().starts_with("yaml: line 1: "), "{err}");
    }

    #[test]
    fn quoted_and_block_spans() {
        let src = "a: \"x\\\"y\" # c\nb: 'it''s' # d\nc: |\n  l1\n  l2\nz: 1\n";
        let root = parse(src).unwrap().unwrap();
        let text = |k: &str| {
            let (s, e) = root.get(k).unwrap().span.unwrap();
            &src[s..e]
        };
        assert_eq!(text("a"), "\"x\\\"y\"");
        assert_eq!(text("b"), "'it''s'");
        assert_eq!(text("c"), "|\n  l1\n  l2");
    }

    #[test]
    fn multibyte_spans_are_bytes() {
        let src = "é: ü\n";
        let root = parse(src).unwrap().unwrap();
        let v = root.get("é").unwrap();
        assert_eq!(&src[v.span.unwrap().0..v.span.unwrap().1], "ü");
    }
}

#[cfg(test)]
mod repair_tests {
    use super::*;

    fn text_of<'a>(src: &'a str, n: &Node) -> &'a str {
        let (s, e) = n.span.unwrap();
        &src[s..e]
    }

    #[test]
    fn bom_is_skipped_and_spans_stay_in_source() {
        let src = "\u{feff}name: x\n";
        let root = parse(src).unwrap().unwrap();
        let v = root.get("name").unwrap();
        assert_eq!(v.scalar_value(), Some("x"));
        assert_eq!(text_of(src, v), "x");
    }

    #[test]
    fn tab_after_colon_reads_like_libyaml() {
        let src = "name:\tx\na:\n  b:\t\ty # c\n";
        let root = parse(src).unwrap().unwrap();
        assert_eq!(root.get("name").unwrap().scalar_value(), Some("x"));
        let y = root.get("a").unwrap().get("b").unwrap();
        assert_eq!((y.scalar_value(), text_of(src, y)), (Some("y"), "y"));
        // A tab inside a quoted value is content, not a separator.
        let root = parse("k: \"a:\tb\"\n").unwrap().unwrap();
        assert_eq!(root.get("k").unwrap().scalar_value(), Some("a:\tb"));
    }

    #[test]
    fn quoted_continuation_at_column_zero() {
        let src = "name: \"x\ny\"\nz: 'p\n\nq' # c\nn:\n  k: \"u\n v\"\nend: 1\n";
        let root = parse(src).unwrap().unwrap();
        let name = root.get("name").unwrap();
        assert_eq!(name.scalar_value(), Some("x y"));
        assert_eq!(text_of(src, name), "\"x\ny\"");
        let z = root.get("z").unwrap();
        assert_eq!(z.scalar_value(), Some("p\nq"));
        assert_eq!(text_of(src, z), "'p\n\nq'");
        let k = root.get("n").unwrap().get("k").unwrap();
        assert_eq!(k.scalar_value(), Some("u v"));
        let end = root.get("end").unwrap();
        assert_eq!(text_of(src, end), "1");
    }

    #[test]
    fn flow_span_ends_at_bracket() {
        let src = "t: [a, b] # c\nm: {a: 1}   # d\n";
        let root = parse(src).unwrap().unwrap();
        assert_eq!(text_of(src, root.get("t").unwrap()), "[a, b]");
        assert_eq!(text_of(src, root.get("m").unwrap()), "{a: 1}");
    }

    #[test]
    fn to_orig_maps_inserted_runs() {
        let ins = [(4, 2), (10, 1)];
        assert_eq!(to_orig(3, &ins), 3);
        assert_eq!(to_orig(4, &ins), 4);
        assert_eq!(to_orig(5, &ins), 4);
        assert_eq!(to_orig(6, &ins), 4);
        assert_eq!(to_orig(12, &ins), 10);
        assert_eq!(to_orig(13, &ins), 10);
        assert_eq!(to_orig(14, &ins), 11);
    }
}
