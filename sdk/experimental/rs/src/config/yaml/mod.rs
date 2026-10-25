//! A yaml.v3-shaped document model.
//!
//! Go's config package works on `yaml.Node` trees and decodes through
//! yaml.v3. This module gives the Rust port the same footing: a
//! [`Node`] tree whose scalars keep their source text, resolved tag and
//! style ([`parse`]), yaml.v3's scalar resolution ([`resolve`]), a
//! block-style emitter ([`emit`]), and serde bridges that decode and
//! encode with yaml.v3's rules ([`de`], [`ser`]).

pub(crate) mod de;
mod de_access;
mod decode_support;
pub(crate) mod emit;
mod parse;
pub(crate) mod resolve;
pub(crate) mod ser;
mod style;
mod timestamp;

pub(crate) use parse::{parse, ParseError};

/// Go's `yaml.Marshal` then `yaml.Unmarshal`: emits `v` and parses the
/// text back, so decode errors carry the line numbers Go reports.
pub(crate) fn roundtrip<T: serde::Serialize + ?Sized>(v: &T) -> Result<Node, crate::config::Error> {
    let node = ser::to_node(v)?;
    let text = emit::document(&node, 4);
    Ok(parse(&text)?.unwrap_or_else(Node::null))
}

use resolve::{MAP_TAG, MERGE_TAG, NULL_TAG, SEQ_TAG, STR_TAG};

/// Presentation style, as yaml.v3's `Style` bits.
#[derive(Clone, Copy, Debug, PartialEq, Eq, Default)]
pub(crate) enum Style {
    #[default]
    Plain,
    Single,
    Double,
    Literal,
    Folded,
    /// Flow collection (`{}` / `[]`).
    Flow,
}

/// Hints the serializer leaves for the merge step: whether a mapping
/// came from a struct (merged field by field, like yaml.v3 decoding into
/// a struct) and whether a value sits behind an `Option` (null clears it,
/// like a Go pointer).
#[derive(Clone, Copy, Debug, Default, PartialEq, Eq)]
pub(crate) struct Hint {
    pub strukt: bool,
    pub optional: bool,
}

#[derive(Clone, Debug, PartialEq)]
pub(crate) enum Kind {
    Scalar(String),
    Seq(Vec<Node>),
    Map(Vec<(Node, Node)>),
    /// An alias, sharing the anchored node (aliases stay references, so
    /// a document's size in memory is linear in its text).
    Alias(std::sync::Arc<Node>),
}

/// One node. `tag` is the short tag: explicit when `tagged`, otherwise
/// the resolved one (`!!str` for quoted scalars).
#[derive(Clone, Debug, PartialEq)]
pub(crate) struct Node {
    pub kind: Kind,
    pub tag: String,
    pub tagged: bool,
    pub style: Style,
    /// 1-based source line; 0 for synthetic nodes.
    pub line: usize,
    /// Byte range in the source, when parsed.
    pub span: Option<(usize, usize)>,
    pub hint: Hint,
}

impl Node {
    pub(crate) fn new(kind: Kind, tag: &str) -> Node {
        Node {
            kind,
            tag: tag.to_string(),
            tagged: false,
            style: Style::Plain,
            line: 0,
            span: None,
            hint: Hint::default(),
        }
    }

    pub(crate) fn scalar(tag: &str, value: impl Into<String>) -> Node {
        Node::new(Kind::Scalar(value.into()), tag)
    }

    pub(crate) fn str(value: impl Into<String>) -> Node {
        Node::scalar(STR_TAG, value)
    }

    pub(crate) fn null() -> Node {
        Node::scalar(NULL_TAG, "null")
    }

    pub(crate) fn map(entries: Vec<(Node, Node)>) -> Node {
        Node::new(Kind::Map(entries), MAP_TAG)
    }

    pub(crate) fn seq(items: Vec<Node>) -> Node {
        Node::new(Kind::Seq(items), SEQ_TAG)
    }

    pub(crate) fn scalar_value(&self) -> Option<&str> {
        match &self.kind {
            Kind::Scalar(v) => Some(v),
            _ => None,
        }
    }

    /// The node an alias points at, or the node itself.
    pub(crate) fn target(&self) -> &Node {
        match &self.kind {
            Kind::Alias(t) => t.target(),
            _ => self,
        }
    }

    /// yaml.v3 `indicatedString`.
    pub(crate) fn indicated_string(&self) -> bool {
        matches!(self.kind, Kind::Scalar(_))
            && (self.tag == STR_TAG
                || ((self.tag.is_empty() || self.tag == "!")
                    && matches!(
                        self.style,
                        Style::Single | Style::Double | Style::Literal | Style::Folded
                    )))
    }

    /// A null scalar (`~`, `null`, empty plain).
    pub(crate) fn is_null(&self) -> bool {
        matches!(self.target().kind, Kind::Scalar(_)) && self.target().tag == NULL_TAG
    }

    /// yaml.v3 `isMerge`: a `<<` key.
    pub(crate) fn is_merge_key(&self) -> bool {
        matches!(&self.kind, Kind::Scalar(v) if v == "<<")
            && (self.tag.is_empty() || self.tag == "!" || self.tag == MERGE_TAG)
    }

    /// Mapping entries.
    #[cfg(test)]
    pub(crate) fn entries(&self) -> Option<&Vec<(Node, Node)>> {
        match &self.kind {
            Kind::Map(e) => Some(e),
            _ => None,
        }
    }

    /// Value for key `k` in a mapping (first match, by key text).
    #[cfg(test)]
    pub(crate) fn get(&self, k: &str) -> Option<&Node> {
        self.entries()?
            .iter()
            .find(|(key, _)| key.scalar_value() == Some(k))
            .map(|(_, v)| v)
    }
}
