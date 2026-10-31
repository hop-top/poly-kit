//! Layer merge with yaml.v3's decode-into-existing-value semantics.
//!
//! Go's loader merges each layer by calling `yaml.Unmarshal` into the
//! same struct. That merges structs field by field, replaces each entry
//! of a map wholesale, replaces scalars and slices, and lets a null
//! clear only pointers, maps, slices and interfaces. The Rust loader
//! serializes the destination to a [`Node`] tree (whose struct and
//! `Option` hints come from the serializer), merges the layer into it
//! here, then deserializes.

use std::collections::HashMap;

use super::yaml::de::{expand_merges, DeError};
use super::yaml::{Kind, Node};

/// Merges `src` into `dst`.
pub(crate) fn merge(dst: &mut Node, src: &Node) -> Result<(), DeError> {
    let src = src.target();
    if src.is_null() {
        if nullable(dst) {
            let optional = dst.hint.optional;
            *dst = src.clone();
            dst.hint.optional = optional;
        }
        return Ok(());
    }
    let src_entries = match &src.kind {
        Kind::Map(_) => Some(expand_merges(src)?),
        _ => None,
    };
    let strukt = dst.hint.strukt;
    match (&mut dst.kind, src_entries) {
        (Kind::Map(entries), Some(src_entries)) => {
            // Key → index, so a layer with many keys merges in linear time.
            let mut index: HashMap<String, usize> = HashMap::with_capacity(entries.len());
            for (i, (k, _)) in entries.iter().enumerate() {
                if let Some(t) = k.scalar_value() {
                    index.entry(t.to_string()).or_insert(i);
                }
            }
            for (k, v) in src_entries {
                let slot = k.scalar_value().and_then(|t| index.get(t).copied());
                match slot {
                    Some(i) if strukt => merge(&mut entries[i].1, v)?,
                    Some(i) => {
                        let v = v.target();
                        let existing = &mut entries[i].1;
                        if !v.is_null() || nullable(existing) {
                            *existing = v.clone();
                        }
                    }
                    // Fields the serializer skipped (or unknown ones,
                    // which deserialization ignores like yaml.v3 does).
                    None => {
                        if let Some(t) = k.scalar_value() {
                            index.insert(t.to_string(), entries.len());
                        }
                        let v = if strukt { v } else { v.target() };
                        entries.push((k.clone(), v.clone()));
                    }
                }
            }
        }
        _ => {
            let hint = dst.hint;
            *dst = src.clone();
            dst.hint.optional = hint.optional;
        }
    }
    Ok(())
}

/// yaml.v3 `null()`: only pointers, maps, slices and interfaces take a
/// null. A null destination (an empty `Option`, an unset `any`) does too.
fn nullable(n: &Node) -> bool {
    n.hint.optional
        || n.is_null()
        || (matches!(n.kind, Kind::Map(_) | Kind::Seq(_)) && !n.hint.strukt)
}

#[cfg(test)]
mod tests {
    use std::collections::BTreeMap;

    use serde::{Deserialize, Serialize};

    use super::super::yaml::{de::from_node, parse, ser::to_node};
    use super::*;

    #[derive(Debug, Default, Serialize, Deserialize, PartialEq)]
    #[serde(default)]
    struct Inner {
        a: String,
        b: i64,
    }

    #[derive(Debug, Default, Serialize, Deserialize, PartialEq)]
    #[serde(default)]
    struct Cfg {
        name: String,
        inner: Inner,
        map: BTreeMap<String, Inner>,
        list: Vec<String>,
        opt: Option<String>,
    }

    fn apply(cfg: &Cfg, src: &str) -> Cfg {
        let mut tree = to_node(cfg).unwrap();
        merge(&mut tree, &parse(src).unwrap().unwrap()).unwrap();
        from_node(&tree).unwrap()
    }

    #[test]
    fn struct_fields_merge_maps_replace_entries() {
        let mut cfg = Cfg {
            name: "n".into(),
            list: vec!["x".into()],
            opt: Some("o".into()),
            ..Cfg::default()
        };
        cfg.inner = Inner {
            a: "keep".into(),
            b: 1,
        };
        cfg.map.insert(
            "e".into(),
            Inner {
                a: "old".into(),
                b: 9,
            },
        );
        let out = apply(&cfg, "inner: {b: 2}\nmap: {e: {a: new}}\nlist: [y]\n");
        assert_eq!(out.inner.a, "keep");
        assert_eq!(out.inner.b, 2);
        // A map entry is replaced, not merged: b drops back to zero.
        assert_eq!(
            out.map["e"],
            Inner {
                a: "new".into(),
                b: 0
            }
        );
        assert_eq!(out.list, ["y"]);
        assert_eq!(out.name, "n");
    }

    #[test]
    fn null_clears_only_nullable() {
        let cfg = Cfg {
            name: "n".into(),
            list: vec!["x".into()],
            opt: Some("o".into()),
            ..Cfg::default()
        };
        let out = apply(&cfg, "name: ~\nlist: ~\nopt: ~\n");
        assert_eq!(out.name, "n");
        assert!(out.list.is_empty());
        assert_eq!(out.opt, None);
    }
}
