//! Field mutability for hot reload. Ports Go `partition.go`.
//!
//! Go reads `reload:"true"` struct tags by reflection. Rust has no
//! runtime reflection, so a config type describes its fields through
//! [`ReloadFields`] (a derive macro may generate this later). The rules
//! are Go's: fields are immutable unless marked reloadable; a reloadable
//! struct field makes every leaf beneath it reloadable; an unmarked
//! struct field recurses so leaves opt in one by one; an inlined struct
//! (Go's untagged embed) contributes its leaves at the parent level;
//! lists and maps are leaves compared whole.

use serde::Serialize;

use super::Value;

/// One field of a config struct, by its serialized (YAML) name.
#[derive(Clone, Debug, PartialEq, Eq)]
pub struct Field {
    name: String,
    reload: bool,
    shape: Shape,
}

#[derive(Clone, Debug, PartialEq, Eq)]
enum Shape {
    Leaf,
    Nested(Vec<Field>),
    Inline(Vec<Field>),
}

impl Field {
    /// A scalar, list or map field.
    pub fn leaf(name: impl Into<String>) -> Field {
        Field {
            name: name.into(),
            reload: false,
            shape: Shape::Leaf,
        }
    }

    /// A struct field with its own fields.
    pub fn nested(name: impl Into<String>, fields: Vec<Field>) -> Field {
        Field {
            name: name.into(),
            reload: false,
            shape: Shape::Nested(fields),
        }
    }

    /// A struct field of a type that implements [`ReloadFields`].
    pub fn of<T: ReloadFields>(name: impl Into<String>) -> Field {
        Field::nested(name, T::reload_fields())
    }

    /// A struct flattened into its parent (`#[serde(flatten)]`; Go's
    /// untagged embed).
    pub fn inline(fields: Vec<Field>) -> Field {
        Field {
            name: String::new(),
            reload: false,
            shape: Shape::Inline(fields),
        }
    }

    /// Marks the field hot-reloadable (Go's `reload:"true"`).
    pub fn reloadable(mut self) -> Field {
        self.reload = true;
        self
    }
}

/// Describes a config type's fields for [`partition`].
///
/// Names are the serialized (serde) names, written by hand, so they can
/// drift from the struct: a renamed or `#[serde(rename)]`d field that is
/// still listed under its old name makes every reload fail with
/// `config.diffPaths: path ... not resolvable`, and a field missing from
/// the list is never compared, so a change to it is neither vetoed nor
/// reported. Pin the list in a test with
/// [`check_reload_fields`](super::check_reload_fields).
pub trait ReloadFields {
    fn reload_fields() -> Vec<Field>;
}

#[cfg(not(target_arch = "wasm32"))]
/// Names a level of `fields` contributes (inline fields flattened).
fn level_names<'a>(fields: &'a [Field], out: &mut Vec<&'a str>) {
    for f in fields {
        match &f.shape {
            Shape::Inline(sub) => level_names(sub, out),
            _ => out.push(&f.name),
        }
    }
}

#[cfg(not(target_arch = "wasm32"))]
fn join(prefix: &str, name: &str) -> String {
    if prefix.is_empty() {
        name.to_string()
    } else {
        format!("{prefix}.{name}")
    }
}

#[cfg(not(target_arch = "wasm32"))]
/// Differences between `fields` and the serialized value `v` of the
/// level they describe. A null level (an empty `Option`) is skipped.
pub(crate) fn mismatches(fields: &[Field], v: &Value, prefix: &str, out: &mut Vec<String>) {
    let Value::Map(m) = v else {
        return;
    };
    let mut names = Vec::new();
    level_names(fields, &mut names);
    listed(fields, m, prefix, out);
    for k in m.keys() {
        if !names.contains(&k.as_str()) {
            out.push(format!("{}: serialized but not listed", join(prefix, k)));
        }
    }
}

#[cfg(not(target_arch = "wasm32"))]
fn listed(fields: &[Field], m: &super::Map, prefix: &str, out: &mut Vec<String>) {
    for f in fields {
        let path = join(prefix, &f.name);
        match &f.shape {
            Shape::Inline(sub) => listed(sub, m, prefix, out),
            Shape::Leaf if !m.contains_key(&f.name) => {
                out.push(format!("{path}: listed but not serialized"))
            }
            Shape::Leaf => {}
            Shape::Nested(sub) => match m.get(&f.name) {
                None => out.push(format!("{path}: listed but not serialized")),
                Some(child) => mismatches(sub, child, &path, out),
            },
        }
    }
}

fn walk(
    fields: &[Field],
    prefix: &str,
    parent_mut: bool,
    m: &mut Vec<String>,
    i: &mut Vec<String>,
) {
    for f in fields {
        let here = parent_mut || f.reload;
        let path = if prefix.is_empty() {
            f.name.clone()
        } else {
            format!("{prefix}.{}", f.name)
        };
        match &f.shape {
            Shape::Leaf => {
                if here {
                    m.push(path)
                } else {
                    i.push(path)
                }
            }
            Shape::Nested(sub) => walk(sub, &path, here, m, i),
            Shape::Inline(sub) => walk(sub, prefix, here, m, i),
        }
    }
}

/// The dotted leaf paths of `T`, split into (reloadable, immutable),
/// each sorted.
pub fn partition<T: ReloadFields>() -> (Vec<String>, Vec<String>) {
    let (mut m, mut i) = (Vec::new(), Vec::new());
    walk(&T::reload_fields(), "", false, &mut m, &mut i);
    m.sort();
    i.sort();
    (m, i)
}

/// Before and after values at one path, as published in reload events.
#[derive(Clone, Debug, PartialEq, Serialize)]
pub struct FieldDiff {
    pub from: Value,
    pub to: Value,
}
