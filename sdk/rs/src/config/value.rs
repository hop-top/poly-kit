//! [`Value`]: the Rust counterpart of the `any` values Go's config
//! package hands out (`Get`, override maps, migration maps).

use std::collections::BTreeMap;
use std::fmt;

use serde::de::{self, Deserialize, Deserializer, MapAccess, SeqAccess, Visitor};
use serde::ser::{Serialize, SerializeMap, SerializeSeq, Serializer};

use super::yaml::resolve::format_float;

/// A dynamically typed config value.
///
/// Mirrors what yaml.v3 decodes into `any`: `Int` is Go's `int`, `Uint`
/// an integer above `i64::MAX` (Go's `uint64`). Mappings are keyed by
/// the key's text; Go would build `map[any]any` for non-string keys.
/// Timestamps stay text (Go decodes them to `time.Time`).
#[derive(Clone, Debug, PartialEq, Default)]
pub enum Value {
    #[default]
    Null,
    Bool(bool),
    Int(i64),
    Uint(u64),
    Float(f64),
    String(String),
    Seq(Vec<Value>),
    Map(Map),
}

/// A mapping value.
pub type Map = BTreeMap<String, Value>;

impl Value {
    /// The value as a string slice, when it is a string.
    pub fn as_str(&self) -> Option<&str> {
        match self {
            Value::String(s) => Some(s),
            _ => None,
        }
    }

    /// The mapping, when the value is one.
    pub fn as_map(&self) -> Option<&Map> {
        match self {
            Value::Map(m) => Some(m),
            _ => None,
        }
    }

    /// Follows a dotted path through nested mappings.
    pub fn lookup(&self, dotted: &str) -> Option<&Value> {
        let mut cur = self;
        for seg in dotted.split('.') {
            cur = cur.as_map()?.get(seg)?;
        }
        Some(cur)
    }
}

/// Go's `%v` rendering: `<nil>`, `true`, `1.5`, `[a b]`,
/// `map[a:1 b:2]` (keys sorted).
impl fmt::Display for Value {
    fn fmt(&self, f: &mut fmt::Formatter<'_>) -> fmt::Result {
        match self {
            Value::Null => f.write_str("<nil>"),
            Value::Bool(b) => write!(f, "{b}"),
            Value::Int(i) => write!(f, "{i}"),
            Value::Uint(u) => write!(f, "{u}"),
            Value::Float(x) => f.write_str(&format_float(*x, false)),
            Value::String(s) => f.write_str(s),
            Value::Seq(items) => {
                f.write_str("[")?;
                for (i, v) in items.iter().enumerate() {
                    if i > 0 {
                        f.write_str(" ")?;
                    }
                    write!(f, "{v}")?;
                }
                f.write_str("]")
            }
            Value::Map(m) => {
                f.write_str("map[")?;
                for (i, (k, v)) in m.iter().enumerate() {
                    if i > 0 {
                        f.write_str(" ")?;
                    }
                    write!(f, "{k}:{v}")?;
                }
                f.write_str("]")
            }
        }
    }
}

impl From<&str> for Value {
    fn from(s: &str) -> Self {
        Value::String(s.to_string())
    }
}

impl From<String> for Value {
    fn from(s: String) -> Self {
        Value::String(s)
    }
}

impl From<bool> for Value {
    fn from(b: bool) -> Self {
        Value::Bool(b)
    }
}

impl From<i64> for Value {
    fn from(i: i64) -> Self {
        Value::Int(i)
    }
}

impl From<f64> for Value {
    fn from(x: f64) -> Self {
        Value::Float(x)
    }
}

impl From<Map> for Value {
    fn from(m: Map) -> Self {
        Value::Map(m)
    }
}

impl Serialize for Value {
    fn serialize<S: Serializer>(&self, s: S) -> Result<S::Ok, S::Error> {
        match self {
            Value::Null => s.serialize_unit(),
            Value::Bool(b) => s.serialize_bool(*b),
            Value::Int(i) => s.serialize_i64(*i),
            Value::Uint(u) => s.serialize_u64(*u),
            Value::Float(x) => s.serialize_f64(*x),
            Value::String(v) => s.serialize_str(v),
            Value::Seq(items) => {
                let mut seq = s.serialize_seq(Some(items.len()))?;
                for v in items {
                    seq.serialize_element(v)?;
                }
                seq.end()
            }
            Value::Map(m) => {
                let mut map = s.serialize_map(Some(m.len()))?;
                for (k, v) in m {
                    map.serialize_entry(k, v)?;
                }
                map.end()
            }
        }
    }
}

impl<'de> Deserialize<'de> for Value {
    fn deserialize<D: Deserializer<'de>>(d: D) -> Result<Self, D::Error> {
        d.deserialize_any(ValueVisitor)
    }
}

struct ValueVisitor;

impl<'de> Visitor<'de> for ValueVisitor {
    type Value = Value;

    fn expecting(&self, f: &mut fmt::Formatter<'_>) -> fmt::Result {
        f.write_str("any YAML value")
    }
    fn visit_unit<E>(self) -> Result<Value, E> {
        Ok(Value::Null)
    }
    fn visit_none<E>(self) -> Result<Value, E> {
        Ok(Value::Null)
    }
    fn visit_some<D: Deserializer<'de>>(self, d: D) -> Result<Value, D::Error> {
        Value::deserialize(d)
    }
    fn visit_bool<E>(self, b: bool) -> Result<Value, E> {
        Ok(Value::Bool(b))
    }
    fn visit_i64<E>(self, i: i64) -> Result<Value, E> {
        Ok(Value::Int(i))
    }
    fn visit_u64<E>(self, u: u64) -> Result<Value, E> {
        Ok(i64::try_from(u).map_or(Value::Uint(u), Value::Int))
    }
    fn visit_f64<E>(self, x: f64) -> Result<Value, E> {
        Ok(Value::Float(x))
    }
    fn visit_str<E>(self, s: &str) -> Result<Value, E> {
        Ok(Value::String(s.to_string()))
    }
    fn visit_string<E>(self, s: String) -> Result<Value, E> {
        Ok(Value::String(s))
    }
    fn visit_seq<A: SeqAccess<'de>>(self, mut a: A) -> Result<Value, A::Error> {
        let mut out = Vec::new();
        while let Some(v) = a.next_element()? {
            out.push(v);
        }
        Ok(Value::Seq(out))
    }
    fn visit_map<A: MapAccess<'de>>(self, mut a: A) -> Result<Value, A::Error> {
        let mut out = Map::new();
        while let Some(k) = a.next_key::<Key>()? {
            out.insert(k.0, a.next_value()?);
        }
        Ok(Value::Map(out))
    }
}

/// A mapping key of any scalar type, kept as its text.
struct Key(String);

impl<'de> Deserialize<'de> for Key {
    fn deserialize<D: Deserializer<'de>>(d: D) -> Result<Self, D::Error> {
        struct V;
        impl<'de> Visitor<'de> for V {
            type Value = Key;
            fn expecting(&self, f: &mut fmt::Formatter<'_>) -> fmt::Result {
                f.write_str("a scalar key")
            }
            fn visit_str<E>(self, s: &str) -> Result<Key, E> {
                Ok(Key(s.to_string()))
            }
            fn visit_string<E>(self, s: String) -> Result<Key, E> {
                Ok(Key(s))
            }
            fn visit_bool<E>(self, b: bool) -> Result<Key, E> {
                Ok(Key(b.to_string()))
            }
            fn visit_i64<E>(self, i: i64) -> Result<Key, E> {
                Ok(Key(i.to_string()))
            }
            fn visit_u64<E>(self, u: u64) -> Result<Key, E> {
                Ok(Key(u.to_string()))
            }
            fn visit_f64<E>(self, x: f64) -> Result<Key, E> {
                Ok(Key(format_float(x, false)))
            }
            fn visit_unit<E>(self) -> Result<Key, E> {
                Ok(Key("null".into()))
            }
            fn visit_seq<A: SeqAccess<'de>>(self, _: A) -> Result<Key, A::Error> {
                Err(de::Error::custom("invalid map key: sequence"))
            }
            fn visit_map<A: MapAccess<'de>>(self, _: A) -> Result<Key, A::Error> {
                Err(de::Error::custom("invalid map key: mapping"))
            }
        }
        d.deserialize_string(V)
    }
}

#[cfg(test)]
mod tests {
    use super::super::yaml::{de::from_node, parse};
    use super::*;

    #[test]
    fn decodes_like_go_any() {
        let src = "i: 1\nbig: 9223372036854775808\nf: 0.5\nb: true\nn: ~\ns: hi\nl: [1, x]\nm: {k: v}\nd: 2024-01-02\n";
        let v: Value = from_node(&parse(src).unwrap().unwrap()).unwrap();
        let m = v.as_map().unwrap();
        assert_eq!(m["i"], Value::Int(1));
        assert_eq!(m["big"], Value::Uint(9223372036854775808));
        assert_eq!(m["f"], Value::Float(0.5));
        assert_eq!(m["b"], Value::Bool(true));
        assert_eq!(m["n"], Value::Null);
        assert_eq!(m["l"], Value::Seq(vec![Value::Int(1), "x".into()]));
        assert_eq!(v.lookup("m.k"), Some(&Value::from("v")));
        assert_eq!(m["d"], Value::from("2024-01-02"));
    }

    #[test]
    fn displays_like_go_v() {
        let v: Value = from_node(&parse("{b: [1, 2.5], a: ~}").unwrap().unwrap()).unwrap();
        assert_eq!(v.to_string(), "map[a:<nil> b:[1 2.5]]");
    }
}
