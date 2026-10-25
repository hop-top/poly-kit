//! Source text to [`Node`] tree, keeping byte spans for the editor.

use std::collections::HashMap;
use std::fmt;

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

/// Parses the first document of `src`. `Ok(None)` for a stream with no
/// document (empty or comments only).
pub(crate) fn parse(src: &str) -> Result<Option<Node>, ParseError> {
    let offsets: Vec<usize> = src
        .char_indices()
        .map(|(b, _)| b)
        .chain(std::iter::once(src.len()))
        .collect();
    let byte = |m: usize| offsets.get(m).copied().unwrap_or(src.len());
    let span_of = |s: &Span| (byte(s.start.index()), byte(s.end.index()));

    let mut parser = Parser::new_from_str(src);
    let mut stack: Vec<Frame> = Vec::new();
    let mut anchors: HashMap<usize, Node> = HashMap::new();
    let mut root: Option<Node> = None;
    let mut in_doc = false;

    while let Some(next) = parser.next_event() {
        let (event, span) = next.map_err(|e| ParseError {
            line: e.marker().line(),
            msg: e.info().to_string(),
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
                node.span = Some((start, end));
                set_tag(&mut node, tag.as_deref(), line)?;
                Some((node, anchor))
            }
            Event::Alias(id) => {
                let target = anchors.get(&id).cloned().ok_or_else(|| ParseError {
                    line,
                    msg: "unknown anchor referenced".into(),
                })?;
                let tag = target.tag.clone();
                let mut node = Node::new(Kind::Alias(Box::new(target)), &tag);
                node.line = line;
                node.span = Some((start, end));
                Some((node, 0))
            }
            Event::SequenceStart(anchor, tag) => {
                let mut node = Node::new(Kind::Seq(Vec::new()), SEQ_TAG);
                node.line = line;
                node.span = Some((start, start));
                node.style = flow_style(src, start, b'[');
                set_tag(&mut node, tag.as_deref(), line)?;
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
                set_tag(&mut node, tag.as_deref(), line)?;
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
                    close_span(&mut node, first, last, end);
                    node.kind = Kind::Seq(items);
                    Some((node, anchor))
                }
                _ => return Err(internal(line)),
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
                    close_span(&mut node, first, last, end);
                    node.kind = Kind::Map(entries);
                    Some((node, anchor))
                }
                _ => return Err(internal(line)),
            },
            Event::Nothing | Event::StreamStart => None,
        };
        if let Some((node, anchor)) = done {
            if anchor != 0 {
                anchors.insert(anchor, node.clone());
            }
            match stack.last_mut() {
                None => root = Some(node),
                Some(Frame::Seq { items, .. }) => items.push(node),
                Some(Frame::Map { entries, key, .. }) => match key.take() {
                    None => {
                        reject_implicit_empty_key(src, &node)?;
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
    fn multibyte_spans_are_bytes() {
        let src = "é: ü\n";
        let root = parse(src).unwrap().unwrap();
        let v = root.get("é").unwrap();
        assert_eq!(&src[v.span.unwrap().0..v.span.unwrap().1], "ü");
    }
}
