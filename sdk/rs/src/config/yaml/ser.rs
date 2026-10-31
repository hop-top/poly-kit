//! serde `Serialize` to [`Node`], with yaml.v3 `Marshal`'s tagging:
//! strings that would read back as another type are double-quoted,
//! multi-line strings use literal blocks, map keys (not struct fields)
//! are sorted in yaml.v3's natural order.

use std::cmp::Ordering;
use std::fmt;

use serde::ser::{self, Serialize};

use super::emit::string_needs_quotes;
use super::resolve::{format_float, BOOL_TAG, FLOAT_TAG, INT_TAG, STR_TAG};
use super::{Kind, Node, Style};

/// Serialization failure (a value YAML cannot carry, such as a map
/// with a non-scalar key).
#[derive(Clone, Debug, PartialEq, Eq)]
pub(crate) struct SerError(pub String);

impl fmt::Display for SerError {
    fn fmt(&self, f: &mut fmt::Formatter<'_>) -> fmt::Result {
        f.write_str(&self.0)
    }
}

impl std::error::Error for SerError {}

impl ser::Error for SerError {
    fn custom<T: fmt::Display>(msg: T) -> Self {
        SerError(msg.to_string())
    }
}

/// Encodes `v` as a node tree.
pub(crate) fn to_node<T: Serialize + ?Sized>(v: &T) -> Result<Node, SerError> {
    v.serialize(NodeSer)
}

/// A string node, styled as yaml.v3 `stringv` would.
pub(crate) fn string_node(s: &str) -> Node {
    let mut n = Node::str(s);
    if s.contains('\n') {
        n.style = Style::Literal;
    } else if string_needs_quotes(s) {
        n.style = Style::Double;
    }
    n
}

pub(crate) struct NodeSer;

fn int(v: impl ToString) -> Node {
    Node::scalar(INT_TAG, v.to_string())
}

fn variant_map(variant: &str, value: Node) -> Node {
    Node::map(vec![(string_node(variant), value)])
}

impl ser::Serializer for NodeSer {
    type Ok = Node;
    type Error = SerError;
    type SerializeSeq = SeqSer;
    type SerializeTuple = SeqSer;
    type SerializeTupleStruct = SeqSer;
    type SerializeTupleVariant = SeqSer;
    type SerializeMap = MapSer;
    type SerializeStruct = MapSer;
    type SerializeStructVariant = MapSer;

    fn serialize_bool(self, v: bool) -> Result<Node, SerError> {
        Ok(Node::scalar(BOOL_TAG, if v { "true" } else { "false" }))
    }
    fn serialize_i8(self, v: i8) -> Result<Node, SerError> {
        Ok(int(v))
    }
    fn serialize_i16(self, v: i16) -> Result<Node, SerError> {
        Ok(int(v))
    }
    fn serialize_i32(self, v: i32) -> Result<Node, SerError> {
        Ok(int(v))
    }
    fn serialize_i64(self, v: i64) -> Result<Node, SerError> {
        Ok(int(v))
    }
    fn serialize_u8(self, v: u8) -> Result<Node, SerError> {
        Ok(int(v))
    }
    fn serialize_u16(self, v: u16) -> Result<Node, SerError> {
        Ok(int(v))
    }
    fn serialize_u32(self, v: u32) -> Result<Node, SerError> {
        Ok(int(v))
    }
    fn serialize_u64(self, v: u64) -> Result<Node, SerError> {
        Ok(int(v))
    }
    fn serialize_f32(self, v: f32) -> Result<Node, SerError> {
        Ok(Node::scalar(FLOAT_TAG, format_float(f64::from(v), true)))
    }
    fn serialize_f64(self, v: f64) -> Result<Node, SerError> {
        Ok(Node::scalar(FLOAT_TAG, format_float(v, true)))
    }
    fn serialize_char(self, v: char) -> Result<Node, SerError> {
        Ok(string_node(&v.to_string()))
    }
    fn serialize_str(self, v: &str) -> Result<Node, SerError> {
        Ok(string_node(v))
    }
    fn serialize_bytes(self, v: &[u8]) -> Result<Node, SerError> {
        match std::str::from_utf8(v) {
            Ok(s) => Ok(string_node(s)),
            Err(_) => Ok(Node::seq(v.iter().map(|b| int(*b)).collect())),
        }
    }
    fn serialize_none(self) -> Result<Node, SerError> {
        let mut n = Node::null();
        n.hint.optional = true;
        Ok(n)
    }
    fn serialize_some<T: Serialize + ?Sized>(self, value: &T) -> Result<Node, SerError> {
        let mut n = value.serialize(NodeSer)?;
        n.hint.optional = true;
        Ok(n)
    }
    fn serialize_unit(self) -> Result<Node, SerError> {
        Ok(Node::null())
    }
    fn serialize_unit_struct(self, _name: &'static str) -> Result<Node, SerError> {
        Ok(Node::null())
    }
    fn serialize_unit_variant(
        self,
        _name: &'static str,
        _index: u32,
        variant: &'static str,
    ) -> Result<Node, SerError> {
        Ok(string_node(variant))
    }
    fn serialize_newtype_struct<T: Serialize + ?Sized>(
        self,
        _name: &'static str,
        value: &T,
    ) -> Result<Node, SerError> {
        value.serialize(NodeSer)
    }
    fn serialize_newtype_variant<T: Serialize + ?Sized>(
        self,
        _name: &'static str,
        _index: u32,
        variant: &'static str,
        value: &T,
    ) -> Result<Node, SerError> {
        Ok(variant_map(variant, value.serialize(NodeSer)?))
    }
    fn serialize_seq(self, len: Option<usize>) -> Result<SeqSer, SerError> {
        Ok(SeqSer {
            items: Vec::with_capacity(len.unwrap_or(0)),
            variant: None,
        })
    }
    fn serialize_tuple(self, len: usize) -> Result<SeqSer, SerError> {
        self.serialize_seq(Some(len))
    }
    fn serialize_tuple_struct(self, _name: &'static str, len: usize) -> Result<SeqSer, SerError> {
        self.serialize_seq(Some(len))
    }
    fn serialize_tuple_variant(
        self,
        _name: &'static str,
        _index: u32,
        variant: &'static str,
        len: usize,
    ) -> Result<SeqSer, SerError> {
        Ok(SeqSer {
            items: Vec::with_capacity(len),
            variant: Some(variant),
        })
    }
    fn serialize_map(self, len: Option<usize>) -> Result<MapSer, SerError> {
        Ok(MapSer {
            entries: Vec::with_capacity(len.unwrap_or(0)),
            key: None,
            strukt: false,
            variant: None,
        })
    }
    fn serialize_struct(self, _name: &'static str, len: usize) -> Result<MapSer, SerError> {
        Ok(MapSer {
            entries: Vec::with_capacity(len),
            key: None,
            strukt: true,
            variant: None,
        })
    }
    fn serialize_struct_variant(
        self,
        _name: &'static str,
        _index: u32,
        variant: &'static str,
        len: usize,
    ) -> Result<MapSer, SerError> {
        Ok(MapSer {
            entries: Vec::with_capacity(len),
            key: None,
            strukt: true,
            variant: Some(variant),
        })
    }
}

pub(crate) struct SeqSer {
    items: Vec<Node>,
    variant: Option<&'static str>,
}

impl SeqSer {
    fn finish(self) -> Node {
        let seq = Node::seq(self.items);
        match self.variant {
            Some(v) => variant_map(v, seq),
            None => seq,
        }
    }
}

macro_rules! seq_impl {
    ($trait:ident, $method:ident) => {
        impl ser::$trait for SeqSer {
            type Ok = Node;
            type Error = SerError;
            fn $method<T: Serialize + ?Sized>(&mut self, value: &T) -> Result<(), SerError> {
                self.items.push(value.serialize(NodeSer)?);
                Ok(())
            }
            fn end(self) -> Result<Node, SerError> {
                Ok(self.finish())
            }
        }
    };
}

seq_impl!(SerializeSeq, serialize_element);
seq_impl!(SerializeTuple, serialize_element);
seq_impl!(SerializeTupleStruct, serialize_field);
seq_impl!(SerializeTupleVariant, serialize_field);

pub(crate) struct MapSer {
    entries: Vec<(Node, Node)>,
    key: Option<Node>,
    strukt: bool,
    variant: Option<&'static str>,
}

impl MapSer {
    fn finish(mut self) -> Node {
        if !self.strukt {
            self.entries.sort_by(|a, b| key_cmp(&a.0, &b.0));
        }
        let mut map = Node::map(self.entries);
        map.hint.strukt = self.strukt;
        match self.variant {
            Some(v) => variant_map(v, map),
            None => map,
        }
    }
}

impl ser::SerializeMap for MapSer {
    type Ok = Node;
    type Error = SerError;
    fn serialize_key<T: Serialize + ?Sized>(&mut self, key: &T) -> Result<(), SerError> {
        let k = key.serialize(NodeSer)?;
        if !matches!(k.kind, Kind::Scalar(_)) {
            return Err(SerError("map key must be a scalar".into()));
        }
        self.key = Some(k);
        Ok(())
    }
    fn serialize_value<T: Serialize + ?Sized>(&mut self, value: &T) -> Result<(), SerError> {
        let k = self
            .key
            .take()
            .ok_or_else(|| SerError("map value without key".into()))?;
        self.entries.push((k, value.serialize(NodeSer)?));
        Ok(())
    }
    fn end(self) -> Result<Node, SerError> {
        Ok(self.finish())
    }
}

impl ser::SerializeStruct for MapSer {
    type Ok = Node;
    type Error = SerError;
    fn serialize_field<T: Serialize + ?Sized>(
        &mut self,
        key: &'static str,
        value: &T,
    ) -> Result<(), SerError> {
        self.entries
            .push((Node::str(key), value.serialize(NodeSer)?));
        Ok(())
    }
    fn end(self) -> Result<Node, SerError> {
        Ok(self.finish())
    }
}

impl ser::SerializeStructVariant for MapSer {
    type Ok = Node;
    type Error = SerError;
    fn serialize_field<T: Serialize + ?Sized>(
        &mut self,
        key: &'static str,
        value: &T,
    ) -> Result<(), SerError> {
        ser::SerializeStruct::serialize_field(self, key, value)
    }
    fn end(self) -> Result<Node, SerError> {
        Ok(self.finish())
    }
}

/// yaml.v3 `keyList.Less`: numbers (and bools) before strings, numbers
/// by value, strings in natural order (digit runs compare numerically,
/// letters before other characters).
pub(crate) fn key_cmp(a: &Node, b: &Node) -> Ordering {
    let num = |n: &Node| -> Option<f64> {
        let v = n.scalar_value()?;
        match n.tag.as_str() {
            INT_TAG | FLOAT_TAG => v.parse().ok(),
            BOOL_TAG => Some(if v == "true" { 1.0 } else { 0.0 }),
            _ => None,
        }
    };
    match (num(a), num(b)) {
        (Some(x), Some(y)) => return x.partial_cmp(&y).unwrap_or(Ordering::Equal),
        (Some(_), None) => return Ordering::Less,
        (None, Some(_)) => return Ordering::Greater,
        _ => {}
    }
    let (sa, sb) = (
        a.scalar_value().unwrap_or_default(),
        b.scalar_value().unwrap_or_default(),
    );
    if a.tag != STR_TAG || b.tag != STR_TAG {
        return sa.cmp(sb);
    }
    if natural_less(sa, sb) {
        Ordering::Less
    } else if natural_less(sb, sa) {
        Ordering::Greater
    } else {
        Ordering::Equal
    }
}

fn natural_less(a: &str, b: &str) -> bool {
    let ar: Vec<char> = a.chars().collect();
    let br: Vec<char> = b.chars().collect();
    let mut digits = false;
    let mut i = 0;
    while i < ar.len() && i < br.len() {
        if ar[i] == br[i] {
            digits = ar[i].is_ascii_digit();
            i += 1;
            continue;
        }
        let al = ar[i].is_alphabetic();
        let bl = br[i].is_alphabetic();
        if al && bl {
            return ar[i] < br[i];
        }
        if al || bl {
            return if digits { al } else { bl };
        }
        let (mut an, mut bn): (i64, i64) = (0, 0);
        if ar[i] == '0' || br[i] == '0' {
            let mut j = i;
            while j > 0 && ar[j - 1].is_ascii_digit() {
                j -= 1;
                if ar[j] != '0' {
                    an = 1;
                    bn = 1;
                    break;
                }
            }
        }
        let mut ai = i;
        while ai < ar.len() && ar[ai].is_ascii_digit() {
            an = an
                .wrapping_mul(10)
                .wrapping_add(i64::from(ar[ai] as u8 - b'0'));
            ai += 1;
        }
        let mut bi = i;
        while bi < br.len() && br[bi].is_ascii_digit() {
            bn = bn
                .wrapping_mul(10)
                .wrapping_add(i64::from(br[bi] as u8 - b'0'));
            bi += 1;
        }
        if an != bn {
            return an < bn;
        }
        if ai != bi {
            return ai < bi;
        }
        return ar[i] < br[i];
    }
    ar.len() < br.len()
}

#[cfg(test)]
mod tests {
    use std::collections::HashMap;

    use super::*;

    #[derive(serde::Serialize)]
    struct S {
        name: String,
        n: Option<i32>,
        tags: Vec<String>,
    }

    #[test]
    fn struct_and_option_hints() {
        let n = to_node(&S {
            name: "0.9".into(),
            n: Some(3),
            tags: vec![],
        })
        .unwrap();
        assert!(n.hint.strukt);
        let name = n.get("name").unwrap();
        assert_eq!(name.style, Style::Double);
        assert!(n.get("n").unwrap().hint.optional);
        assert!(matches!(n.get("tags").unwrap().kind, Kind::Seq(_)));
    }

    #[test]
    fn maps_sort_naturally() {
        let m: HashMap<&str, i32> = [("a10", 1), ("a2", 2), ("b", 3), ("A", 4)]
            .into_iter()
            .collect();
        let n = to_node(&m).unwrap();
        let keys: Vec<&str> = n
            .entries()
            .unwrap()
            .iter()
            .map(|(k, _)| k.scalar_value().unwrap())
            .collect();
        assert_eq!(keys, ["A", "a2", "a10", "b"]);
    }
}
