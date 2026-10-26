//! serde access types for [`De`](super::de::De): sequences, mappings
//! (keys as their text when the target cannot tell), enums.

use serde::de::{self, Visitor};

use super::de::{terror, De, DeError};
use super::decode_support::visit;
use super::{Kind, Node};

pub(super) struct SeqAccess<'a> {
    pub(super) items: std::slice::Iter<'a, Node>,
}

impl<'de> de::SeqAccess<'de> for SeqAccess<'_> {
    type Error = DeError;
    fn next_element_seed<T: de::DeserializeSeed<'de>>(
        &mut self,
        seed: T,
    ) -> Result<Option<T::Value>, DeError> {
        match self.items.next() {
            Some(n) => {
                let _v = visit(n)?;
                seed.deserialize(De(n)).map(Some)
            }
            None => Ok(None),
        }
    }
    fn size_hint(&self) -> Option<usize> {
        Some(self.items.len())
    }
}

pub(super) struct MapAccess<'a> {
    pub(super) entries: std::vec::IntoIter<(&'a Node, &'a Node)>,
    pub(super) value: Option<&'a Node>,
}

impl<'de> de::MapAccess<'de> for MapAccess<'_> {
    type Error = DeError;
    fn next_key_seed<K: de::DeserializeSeed<'de>>(
        &mut self,
        seed: K,
    ) -> Result<Option<K::Value>, DeError> {
        match self.entries.next() {
            Some((k, v)) => {
                self.value = Some(v);
                let _v = visit(k)?;
                seed.deserialize(KeyDe(k)).map(Some)
            }
            None => Ok(None),
        }
    }
    fn next_value_seed<V: de::DeserializeSeed<'de>>(
        &mut self,
        seed: V,
    ) -> Result<V::Value, DeError> {
        let v = self
            .value
            .take()
            .ok_or_else(|| DeError("map value without key".into()))?;
        let _v = visit(v)?;
        seed.deserialize(De(v))
    }
}

/// Keys deserialize like values, except that a key requested as `any`
/// arrives as its text when the target cannot tell (string-keyed maps
/// are the norm).
struct KeyDe<'a>(&'a Node);

impl<'de> de::Deserializer<'de> for KeyDe<'_> {
    type Error = DeError;
    fn deserialize_any<V: Visitor<'de>>(self, visitor: V) -> Result<V::Value, DeError> {
        De(self.0).deserialize_any(visitor)
    }
    fn deserialize_string<V: Visitor<'de>>(self, visitor: V) -> Result<V::Value, DeError> {
        let n = self.0.target();
        match &n.kind {
            Kind::Scalar(v) => visitor.visit_string(v.clone()),
            // `? [a]` / `? {k: v}` keys into a string key.
            _ => Err(terror(n, "string")),
        }
    }
    fn deserialize_str<V: Visitor<'de>>(self, visitor: V) -> Result<V::Value, DeError> {
        self.deserialize_string(visitor)
    }
    fn deserialize_identifier<V: Visitor<'de>>(self, visitor: V) -> Result<V::Value, DeError> {
        self.deserialize_string(visitor)
    }
    serde::forward_to_deserialize_any! {
        bool i8 i16 i32 i64 i128 u8 u16 u32 u64 u128 f32 f64 char bytes byte_buf option unit
        unit_struct newtype_struct seq tuple tuple_struct map struct enum ignored_any
    }
}

pub(super) struct EnumAccess<'a> {
    pub(super) key: &'a Node,
    pub(super) value: &'a Node,
}

impl<'de> de::EnumAccess<'de> for EnumAccess<'_> {
    type Error = DeError;
    type Variant = Self;
    fn variant_seed<V: de::DeserializeSeed<'de>>(
        self,
        seed: V,
    ) -> Result<(V::Value, Self), DeError> {
        let v = seed.deserialize(KeyDe(self.key))?;
        Ok((v, self))
    }
}

impl<'de> de::VariantAccess<'de> for EnumAccess<'_> {
    type Error = DeError;
    fn unit_variant(self) -> Result<(), DeError> {
        Ok(())
    }
    fn newtype_variant_seed<T: de::DeserializeSeed<'de>>(
        self,
        seed: T,
    ) -> Result<T::Value, DeError> {
        let _v = visit(self.value)?;
        seed.deserialize(De(self.value))
    }
    fn tuple_variant<V: Visitor<'de>>(self, _len: usize, visitor: V) -> Result<V::Value, DeError> {
        de::Deserializer::deserialize_seq(De(self.value), visitor)
    }
    fn struct_variant<V: Visitor<'de>>(
        self,
        _fields: &'static [&'static str],
        visitor: V,
    ) -> Result<V::Value, DeError> {
        de::Deserializer::deserialize_map(De(self.value), visitor)
    }
}
