//! serde `Deserializer` over [`Node`] with yaml.v3's decode rules: the
//! target type picks the conversion (any scalar's text into a string,
//! ints from floats by truncation, YAML 1.1 booleans only into a bool),
//! null leaves a value at its zero, `<<` merge keys expand, duplicate
//! keys and type mismatches fail with yaml.v3's message shape.

use std::fmt;

use serde::de::{self, DeserializeOwned, IntoDeserializer, Visitor};

use super::de_access::{EnumAccess, MapAccess, SeqAccess};
use super::decode_support::check_unique;
pub(crate) use super::decode_support::{base64_decode, expand_merges};
use super::resolve::{resolve, Scalar, BINARY_TAG, MAP_TAG, SEQ_TAG};
use super::{Kind, Node};

/// Decode failure, rendered like yaml.v3's errors.
#[derive(Clone, Debug, PartialEq, Eq)]
pub(crate) struct DeError(pub String);

impl fmt::Display for DeError {
    fn fmt(&self, f: &mut fmt::Formatter<'_>) -> fmt::Result {
        f.write_str(&self.0)
    }
}

impl std::error::Error for DeError {}

impl de::Error for DeError {
    fn custom<T: fmt::Display>(msg: T) -> Self {
        DeError(msg.to_string())
    }
}

/// Decodes `node` into `T`.
pub(crate) fn from_node<T: DeserializeOwned>(node: &Node) -> Result<T, DeError> {
    T::deserialize(De(node))
}

/// yaml.v3 `terror`: `line N: cannot unmarshal !!tag `value` into T`.
fn terror(n: &Node, want: &str) -> DeError {
    let tag = if n.tag.is_empty() { "" } else { &n.tag };
    let mut value = String::new();
    if tag != SEQ_TAG && tag != MAP_TAG {
        let v = n.scalar_value().unwrap_or_default();
        if v.len() > 10 {
            let cut = (0..=7).rev().find(|i| v.is_char_boundary(*i)).unwrap_or(0);
            value = format!(" `{}...`", &v[..cut]);
        } else {
            value = format!(" `{v}`");
        }
    }
    DeError(format!(
        "yaml: unmarshal errors:\n  line {}: cannot unmarshal {tag}{value} into {want}",
        n.line
    ))
}

/// The resolved value of a scalar, as yaml.v3 `scalar()` computes it.
fn resolved(n: &Node) -> Result<Scalar, DeError> {
    let v = n.scalar_value().unwrap_or_default();
    if n.indicated_string() {
        return Ok(Scalar::Str(v.to_string()));
    }
    let tag = if n.tagged { n.tag.as_str() } else { "" };
    if tag == BINARY_TAG {
        let bytes = base64_decode(v)
            .ok_or_else(|| DeError("yaml: !!binary value contains invalid base64 data".into()))?;
        return Ok(Scalar::Str(String::from_utf8_lossy(&bytes).into_owned()));
    }
    resolve(tag, v)
        .map(|(_, s)| s)
        .map_err(|m| DeError(format!("yaml: {m}")))
}

fn is_binary(n: &Node) -> bool {
    n.tagged && n.tag == BINARY_TAG
}

#[derive(Clone, Copy)]
pub(crate) struct De<'a>(pub &'a Node);

macro_rules! de_int {
    ($method:ident, $visit:ident, $t:ty, $name:literal) => {
        fn $method<V: Visitor<'de>>(self, visitor: V) -> Result<V::Value, DeError> {
            let n = self.0.target();
            if n.is_null() {
                return visitor.$visit(0);
            }
            if !matches!(n.kind, Kind::Scalar(_)) {
                return Err(terror(n, $name));
            }
            let got: Option<$t> = match resolved(n)? {
                Scalar::Int(i) => <$t>::try_from(i).ok(),
                Scalar::Uint(u) => <$t>::try_from(u).ok(),
                Scalar::Float(f)
                    if f.is_finite()
                        && f.trunc() >= i64::MIN as f64
                        && f.trunc() <= i64::MAX as f64 =>
                {
                    <$t>::try_from(f.trunc() as i64).ok()
                }
                Scalar::Float(f) if f.is_finite() && f >= 0.0 && f.trunc() <= u64::MAX as f64 => {
                    <$t>::try_from(f.trunc() as u64).ok()
                }
                _ => None,
            };
            match got {
                Some(v) => visitor.$visit(v),
                None => Err(terror(n, $name)),
            }
        }
    };
}

impl<'de> de::Deserializer<'de> for De<'_> {
    type Error = DeError;

    fn deserialize_any<V: Visitor<'de>>(self, visitor: V) -> Result<V::Value, DeError> {
        let n = self.0.target();
        match &n.kind {
            Kind::Scalar(_) => match resolved(n)? {
                Scalar::Null => visitor.visit_unit(),
                Scalar::Bool(b) => visitor.visit_bool(b),
                Scalar::Int(i) => visitor.visit_i64(i),
                Scalar::Uint(u) => visitor.visit_u64(u),
                Scalar::Float(f) => visitor.visit_f64(f),
                Scalar::Str(s) | Scalar::Timestamp(s) => visitor.visit_string(s),
                Scalar::Merge => visitor.visit_str("<<"),
            },
            Kind::Seq(items) => visitor.visit_seq(SeqAccess {
                items: items.iter(),
            }),
            Kind::Map(_) => {
                check_unique(n)?;
                visitor.visit_map(MapAccess {
                    entries: expand_merges(n)?.into_iter(),
                    value: None,
                })
            }
            Kind::Alias(_) => unreachable!("target() resolves aliases"),
        }
    }

    fn deserialize_bool<V: Visitor<'de>>(self, visitor: V) -> Result<V::Value, DeError> {
        let n = self.0.target();
        if n.is_null() {
            return visitor.visit_bool(false);
        }
        if !matches!(n.kind, Kind::Scalar(_)) {
            return Err(terror(n, "bool"));
        }
        match resolved(n)? {
            Scalar::Bool(b) => visitor.visit_bool(b),
            Scalar::Str(s) => match s.as_str() {
                "y" | "Y" | "yes" | "Yes" | "YES" | "on" | "On" | "ON" => visitor.visit_bool(true),
                "n" | "N" | "no" | "No" | "NO" | "off" | "Off" | "OFF" => visitor.visit_bool(false),
                _ => Err(terror(n, "bool")),
            },
            _ => Err(terror(n, "bool")),
        }
    }

    de_int!(deserialize_i8, visit_i8, i8, "int8");
    de_int!(deserialize_i16, visit_i16, i16, "int16");
    de_int!(deserialize_i32, visit_i32, i32, "int32");
    de_int!(deserialize_i64, visit_i64, i64, "int");
    de_int!(deserialize_u8, visit_u8, u8, "uint8");
    de_int!(deserialize_u16, visit_u16, u16, "uint16");
    de_int!(deserialize_u32, visit_u32, u32, "uint32");
    de_int!(deserialize_u64, visit_u64, u64, "uint");

    fn deserialize_f32<V: Visitor<'de>>(self, visitor: V) -> Result<V::Value, DeError> {
        self.float(visitor, "float32")
    }

    fn deserialize_f64<V: Visitor<'de>>(self, visitor: V) -> Result<V::Value, DeError> {
        self.float(visitor, "float64")
    }

    fn deserialize_char<V: Visitor<'de>>(self, visitor: V) -> Result<V::Value, DeError> {
        self.deserialize_string(visitor)
    }

    fn deserialize_str<V: Visitor<'de>>(self, visitor: V) -> Result<V::Value, DeError> {
        self.deserialize_string(visitor)
    }

    fn deserialize_string<V: Visitor<'de>>(self, visitor: V) -> Result<V::Value, DeError> {
        let n = self.0.target();
        if n.is_null() {
            return visitor.visit_string(String::new());
        }
        match &n.kind {
            Kind::Scalar(v) if is_binary(n) => match resolved(n)? {
                Scalar::Str(s) => visitor.visit_string(s),
                _ => visitor.visit_string(v.clone()),
            },
            Kind::Scalar(v) => {
                if n.tagged {
                    resolved(n)?;
                }
                visitor.visit_string(v.clone())
            }
            _ => Err(terror(n, "string")),
        }
    }

    fn deserialize_bytes<V: Visitor<'de>>(self, visitor: V) -> Result<V::Value, DeError> {
        let n = self.0.target();
        match &n.kind {
            Kind::Scalar(v) if is_binary(n) => {
                let b = base64_decode(v).ok_or_else(|| {
                    DeError("yaml: !!binary value contains invalid base64 data".into())
                })?;
                visitor.visit_byte_buf(b)
            }
            Kind::Scalar(v) if !n.is_null() => visitor.visit_byte_buf(v.clone().into_bytes()),
            _ => self.deserialize_seq(visitor),
        }
    }

    fn deserialize_byte_buf<V: Visitor<'de>>(self, visitor: V) -> Result<V::Value, DeError> {
        self.deserialize_bytes(visitor)
    }

    fn deserialize_option<V: Visitor<'de>>(self, visitor: V) -> Result<V::Value, DeError> {
        if self.0.is_null() {
            visitor.visit_none()
        } else {
            visitor.visit_some(self)
        }
    }

    fn deserialize_unit<V: Visitor<'de>>(self, visitor: V) -> Result<V::Value, DeError> {
        if self.0.is_null() {
            visitor.visit_unit()
        } else {
            Err(terror(self.0.target(), "unit"))
        }
    }

    fn deserialize_unit_struct<V: Visitor<'de>>(
        self,
        _name: &'static str,
        visitor: V,
    ) -> Result<V::Value, DeError> {
        self.deserialize_unit(visitor)
    }

    fn deserialize_newtype_struct<V: Visitor<'de>>(
        self,
        _name: &'static str,
        visitor: V,
    ) -> Result<V::Value, DeError> {
        visitor.visit_newtype_struct(self)
    }

    fn deserialize_seq<V: Visitor<'de>>(self, visitor: V) -> Result<V::Value, DeError> {
        let n = self.0.target();
        match &n.kind {
            Kind::Seq(items) => visitor.visit_seq(SeqAccess {
                items: items.iter(),
            }),
            _ if n.is_null() => visitor.visit_seq(SeqAccess { items: [].iter() }),
            _ => Err(terror(n, "[]")),
        }
    }

    fn deserialize_tuple<V: Visitor<'de>>(
        self,
        _len: usize,
        visitor: V,
    ) -> Result<V::Value, DeError> {
        self.deserialize_seq(visitor)
    }

    fn deserialize_tuple_struct<V: Visitor<'de>>(
        self,
        _name: &'static str,
        _len: usize,
        visitor: V,
    ) -> Result<V::Value, DeError> {
        self.deserialize_seq(visitor)
    }

    fn deserialize_map<V: Visitor<'de>>(self, visitor: V) -> Result<V::Value, DeError> {
        self.mapping(visitor, "map")
    }

    fn deserialize_struct<V: Visitor<'de>>(
        self,
        name: &'static str,
        _fields: &'static [&'static str],
        visitor: V,
    ) -> Result<V::Value, DeError> {
        self.mapping(visitor, name)
    }

    fn deserialize_enum<V: Visitor<'de>>(
        self,
        name: &'static str,
        _variants: &'static [&'static str],
        visitor: V,
    ) -> Result<V::Value, DeError> {
        let n = self.0.target();
        match &n.kind {
            Kind::Scalar(v) => visitor.visit_enum(v.clone().into_deserializer()),
            Kind::Map(entries) if entries.len() == 1 => visitor.visit_enum(EnumAccess {
                key: &entries[0].0,
                value: &entries[0].1,
            }),
            _ => Err(terror(n, name)),
        }
    }

    fn deserialize_identifier<V: Visitor<'de>>(self, visitor: V) -> Result<V::Value, DeError> {
        self.deserialize_string(visitor)
    }

    fn deserialize_ignored_any<V: Visitor<'de>>(self, visitor: V) -> Result<V::Value, DeError> {
        visitor.visit_unit()
    }
}

impl De<'_> {
    fn float<'de, V: Visitor<'de>>(self, visitor: V, name: &str) -> Result<V::Value, DeError> {
        let n = self.0.target();
        if n.is_null() {
            return visitor.visit_f64(0.0);
        }
        if !matches!(n.kind, Kind::Scalar(_)) {
            return Err(terror(n, name));
        }
        match resolved(n)? {
            Scalar::Int(i) => visitor.visit_f64(i as f64),
            Scalar::Uint(u) => visitor.visit_f64(u as f64),
            Scalar::Float(f) => visitor.visit_f64(f),
            _ => Err(terror(n, name)),
        }
    }

    fn mapping<'de, V: Visitor<'de>>(self, visitor: V, name: &str) -> Result<V::Value, DeError> {
        let n = self.0.target();
        match &n.kind {
            Kind::Map(_) => {
                check_unique(n)?;
                visitor.visit_map(MapAccess {
                    entries: expand_merges(n)?.into_iter(),
                    value: None,
                })
            }
            _ if n.is_null() => visitor.visit_map(MapAccess {
                entries: Vec::new().into_iter(),
                value: None,
            }),
            _ => Err(terror(n, name)),
        }
    }
}

#[cfg(test)]
mod tests {
    use std::collections::BTreeMap;

    use serde::Deserialize;

    use super::super::parse;
    use super::*;

    #[derive(Debug, Deserialize, Default, PartialEq)]
    #[serde(default)]
    struct S {
        name: String,
        port: i64,
        on: bool,
        ratio: f64,
        tags: Vec<String>,
        sub: Option<Box<S>>,
    }

    fn de<T: DeserializeOwned>(src: &str) -> Result<T, DeError> {
        from_node(&parse(src).unwrap().unwrap())
    }

    #[test]
    fn decodes_by_target_type() {
        let s: S = de("name: 8080\nport: 1.9\non: yes\nratio: 3\ntags: [a, 1]\n").unwrap();
        assert_eq!(s.name, "8080");
        assert_eq!(s.port, 1);
        assert!(s.on);
        assert_eq!(s.ratio, 3.0);
        assert_eq!(s.tags, ["a", "1"]);
    }

    #[test]
    fn type_errors_match_yaml_v3() {
        let err = de::<S>("port: abc\n").unwrap_err();
        assert_eq!(
            err.0,
            "yaml: unmarshal errors:\n  line 1: cannot unmarshal !!str `abc` into int"
        );
        let err = de::<S>("port: abcdefghijklmnop\n").unwrap_err();
        assert!(err.0.ends_with("!!str `abcdefg...` into int"), "{err}");
        let err = de::<S>("tags: x\n").unwrap_err();
        assert!(
            err.0.contains("cannot unmarshal !!str `x` into []"),
            "{err}"
        );
        let err = de::<S>("a: 1\na: 2\n").unwrap_err();
        assert!(
            err.0
                .contains("line 2: mapping key \"a\" already defined at line 1"),
            "{err}"
        );
        let err = de::<S>("port: !!int abc\n").unwrap_err();
        assert_eq!(err.0, "yaml: cannot decode !!str `abc` as a !!int");
    }

    #[test]
    fn merges_expand() {
        let m: BTreeMap<String, BTreeMap<String, i64>> =
            de("base: &b {x: 1, y: 2}\nd:\n  <<: *b\n  y: 3\n").unwrap();
        assert_eq!(m["d"]["x"], 1);
        assert_eq!(m["d"]["y"], 3);
    }

    #[test]
    fn base64() {
        assert_eq!(base64_decode("aGVsbG8=").unwrap(), b"hello");
        assert!(base64_decode("aGVsbG8").is_none());
    }
}
