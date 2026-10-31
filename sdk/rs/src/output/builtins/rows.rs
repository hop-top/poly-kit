//! Row/column helpers shared by the payload-shaped built-in formatters
//! (table, csv, text), so no two of them can drift apart on normalization,
//! column resolution, or cell stringification.
//!
//! Mirrors php `Projection` and py `output.projection`.

use serde_json::Value;

/// Always returns a list of rows; a single map becomes a one-row payload.
pub(super) fn normalize(data: &Value) -> Vec<&Value> {
    match data {
        Value::Array(arr) => arr.iter().collect(),
        other => vec![other],
    }
}

/// Honor the resolved column list — already `--cols` order, or the caller's
/// ColumnSpec order, whichever the dispatcher settled on. An empty list means
/// neither was supplied, so infer from the first object-shaped row; that
/// fallback preserves the payload's own key order because serde_json is built
/// with `preserve_order`.
pub(super) fn resolve_columns(rows: &[&Value], cols: &[String]) -> Vec<String> {
    if !cols.is_empty() {
        return cols.to_vec();
    }
    for row in rows {
        if let Value::Object(map) = row {
            return map.keys().cloned().collect();
        }
    }
    Vec::new()
}

pub(super) fn row_get<'a>(row: &'a Value, key: &str) -> Option<&'a Value> {
    if let Value::Object(map) = row {
        map.get(key)
    } else {
        None
    }
}

/// Render one cell. Absent and null both become the empty string, matching
/// go's zero-value rendering and py/ts's null handling.
pub(super) fn stringify(val: Option<&Value>) -> String {
    match val {
        None | Some(Value::Null) => String::new(),
        Some(Value::String(s)) => s.clone(),
        Some(Value::Bool(b)) => b.to_string(),
        Some(Value::Number(n)) => n.to_string(),
        // Arrays / objects: compact JSON keeps cells single-line.
        Some(other) => serde_json::to_string(other).unwrap_or_default(),
    }
}
