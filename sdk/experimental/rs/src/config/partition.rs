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
pub trait ReloadFields {
    fn reload_fields() -> Vec<Field>;
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
