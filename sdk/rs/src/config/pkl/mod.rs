//! PKL config schemas: field definitions, value validation, completion,
//! and computed-field resolution. Ports the pure half of Go
//! `go/core/config/pkl`. Feature `config-pkl`.
//!
//! [`load_schema`], [`validate_value`], [`completion_keys`] and
//! [`completion_values`] read the PKL source text (no binary needed).
//! [`resolve`] evaluates the module with the `pkl` binary, as Go does.
//!
//! Not ported yet: `RunWizard`, `WizardSteps` and `NewConfigCommand`.
//! They sit on Go's `console/wizard` TUI and a cobra command, which
//! kit-rs does not have; they follow once kit-rs has a console wizard
//! and a CLI root.

mod parse;

pub use parse::parse_source;

use crate::config::{SchemaError, Value};

/// A field's type.
#[derive(Clone, Copy, Debug, Default, PartialEq, Eq)]
pub enum FieldType {
    #[default]
    String,
    Int,
    Float,
    Bool,
    Duration,
    StringEnum,
    StringList,
}

/// A constraint's kind.
#[derive(Clone, Copy, Debug, PartialEq, Eq)]
pub enum ConstraintKind {
    MinLen,
    MaxLen,
    Min,
    Max,
    Pattern,
    Between,
}

/// A constraint's operand.
#[derive(Clone, Debug, PartialEq)]
pub enum ConstraintValue {
    Int(i64),
    Float(f64),
    Str(String),
    Between(i64, i64),
}

/// One validation constraint.
#[derive(Clone, Debug, PartialEq)]
pub struct Constraint {
    pub kind: ConstraintKind,
    pub value: ConstraintValue,
}

/// A `@wizard.when` condition.
#[derive(Clone, Debug, Default, PartialEq, Eq)]
pub struct FieldCondition {
    pub expression: String,
    pub depends_on: Vec<String>,
}

/// One config field from a PKL module.
#[derive(Clone, Debug, Default, PartialEq)]
pub struct FieldDef {
    /// Dotted key path (`database.port`).
    pub path: String,
    pub field_type: FieldType,
    /// From `///` doc comments.
    pub description: String,
    pub default: Option<Value>,
    /// Non-nullable in PKL.
    pub required: bool,
    /// Union members (`"a"|"b"`).
    pub enum_values: Vec<String>,
    pub constraints: Vec<Constraint>,
    /// `@wizard.group`.
    pub group: String,
    /// `@wizard.when`.
    pub when: Option<FieldCondition>,
    /// Default is a cross-field expression (`#{...}`).
    pub computed: bool,
}

/// All fields of a PKL module.
#[derive(Clone, Debug, Default, PartialEq)]
pub struct Schema {
    pub module_name: String,
    pub fields: Vec<FieldDef>,
}

/// Reads and parses a PKL module.
#[cfg(not(target_arch = "wasm32"))]
pub fn load_schema(path: impl AsRef<std::path::Path>) -> Result<Schema, crate::config::Error> {
    let path = path.as_ref();
    let src = std::fs::read_to_string(path).map_err(|e| {
        crate::config::error::io_error("open", path, &e)
            .wrap(format!("pkl: read {}", path.display()))
    })?;
    Ok(parse_source(&src))
}

fn find_field<'a>(s: &'a Schema, key: &str) -> Option<&'a FieldDef> {
    s.fields.iter().find(|f| f.path == key)
}

/// Go `strconv.ParseFloat`: decimal spellings and inf/nan; out of range
/// fails.
fn go_float(s: &str) -> Option<f64> {
    let lower = s.to_ascii_lowercase();
    let t = lower.trim_start_matches(['+', '-']);
    if matches!(t, "inf" | "infinity" | "nan") {
        return s.parse().ok();
    }
    if !s
        .bytes()
        .all(|c| c.is_ascii_digit() || matches!(c, b'+' | b'-' | b'.' | b'e' | b'E'))
    {
        return None;
    }
    s.parse::<f64>().ok().filter(|f| f.is_finite())
}

/// Go `strconv.ParseBool`.
fn go_bool(s: &str) -> bool {
    matches!(
        s,
        "1" | "t" | "T" | "TRUE" | "true" | "True" | "0" | "f" | "F" | "FALSE" | "false" | "False"
    )
}

pub(crate) fn fmt_num(f: f64) -> String {
    crate::config::yaml::resolve::format_float(f, false)
}

/// Checks `value` for `key`: unknown key, type, then constraints, with
/// Go's error values.
pub fn validate_value(schema: &Schema, key: &str, value: &str) -> Result<(), SchemaError> {
    let Some(f) = find_field(schema, key) else {
        return Err(SchemaError::UnknownKey {
            key: key.to_string(),
            valid_keys: schema.fields.iter().map(|f| f.path.clone()).collect(),
        });
    };
    let mismatch = |expected: &str| SchemaError::TypeMismatch {
        key: key.to_string(),
        expected: expected.to_string(),
        got: value.to_string(),
    };
    let violation = |constraint: String| SchemaError::ConstraintViolation {
        key: key.to_string(),
        constraint,
        value: value.to_string(),
    };
    match f.field_type {
        FieldType::Int if value.parse::<i64>().is_err() => return Err(mismatch("integer")),
        FieldType::Float if go_float(value).is_none() => return Err(mismatch("float")),
        FieldType::Bool if !go_bool(value) => return Err(mismatch("boolean")),
        FieldType::StringEnum => {
            if f.enum_values.iter().any(|v| v == value) {
                return Ok(());
            }
            return Err(violation(format!("enum([{}])", f.enum_values.join(" "))));
        }
        _ => {}
    }
    for c in &f.constraints {
        let num = || go_float(value);
        let as_f = |v: &ConstraintValue| match v {
            ConstraintValue::Int(i) => *i as f64,
            ConstraintValue::Float(x) => *x,
            _ => 0.0,
        };
        let as_i = |v: &ConstraintValue| match v {
            ConstraintValue::Int(i) => *i,
            ConstraintValue::Float(x) => *x as i64,
            _ => 0,
        };
        let shown = |v: &ConstraintValue| match v {
            ConstraintValue::Int(i) => i.to_string(),
            ConstraintValue::Float(x) => fmt_num(*x),
            ConstraintValue::Str(s) => s.clone(),
            ConstraintValue::Between(a, b) => format!("[{a} {b}]"),
        };
        match c.kind {
            ConstraintKind::MinLen if (value.len() as i64) < as_i(&c.value) => {
                return Err(violation(format!("minLen({})", as_i(&c.value))))
            }
            ConstraintKind::MaxLen if (value.len() as i64) > as_i(&c.value) => {
                return Err(violation(format!("maxLen({})", as_i(&c.value))))
            }
            ConstraintKind::Min if num().is_some_and(|v| v < as_f(&c.value)) => {
                return Err(violation(format!("min({})", shown(&c.value))))
            }
            ConstraintKind::Max if num().is_some_and(|v| v > as_f(&c.value)) => {
                return Err(violation(format!("max({})", shown(&c.value))))
            }
            ConstraintKind::Pattern => {
                let ConstraintValue::Str(pat) = &c.value else {
                    continue;
                };
                if let Ok(re) = regex::Regex::new(pat) {
                    if !re.is_match(value) {
                        return Err(violation(format!("pattern({pat})")));
                    }
                }
            }
            ConstraintKind::Between => {
                let (lo, hi) = match c.value {
                    ConstraintValue::Between(a, b) => (a as f64, b as f64),
                    _ => (0.0, 0.0),
                };
                if num().is_some_and(|v| v < lo || v > hi) {
                    return Err(violation(format!(
                        "between({}, {})",
                        fmt_num(lo),
                        fmt_num(hi)
                    )));
                }
            }
            _ => {}
        }
    }
    Ok(())
}

/// A completion candidate.
#[derive(Clone, Debug, Default, PartialEq, Eq)]
pub struct CompletionItem {
    pub value: String,
    pub description: String,
}

fn type_label(t: FieldType) -> &'static str {
    match t {
        FieldType::String => "string",
        FieldType::Int => "integer",
        FieldType::Float => "float",
        FieldType::Bool => "boolean",
        FieldType::Duration => "duration",
        FieldType::StringEnum => "enum",
        FieldType::StringList => "string list",
    }
}

/// Every non-computed key, described by its doc comment or type.
pub fn completion_keys(schema: &Schema) -> Vec<CompletionItem> {
    schema
        .fields
        .iter()
        .filter(|f| !f.computed)
        .map(|f| CompletionItem {
            value: f.path.clone(),
            description: if f.description.is_empty() {
                type_label(f.field_type).to_string()
            } else {
                f.description.clone()
            },
        })
        .collect()
}

/// Values for `key`: enum members, or `true`/`false`; `None` otherwise.
pub fn completion_values(schema: &Schema, key: &str) -> Option<Vec<CompletionItem>> {
    let f = find_field(schema, key)?;
    let item = |v: &str| CompletionItem {
        value: v.to_string(),
        description: String::new(),
    };
    match f.field_type {
        FieldType::StringEnum => Some(f.enum_values.iter().map(|v| item(v)).collect()),
        FieldType::Bool => Some(vec![item("true"), item("false")]),
        _ => None,
    }
}

#[cfg(not(target_arch = "wasm32"))]
mod eval;
#[cfg(not(target_arch = "wasm32"))]
pub use eval::resolve;
