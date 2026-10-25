//! [`parse_scalar`]: raw CLI argument to a typed value. Ports Go
//! `parse_scalar.go`.

use super::Value;

/// Converts a raw command-line argument into a typed value for
/// [`set_value`](super::set_value), so `config set threshold 0.9` writes
/// `threshold: 0.9` rather than `threshold: "0.9"`.
///
/// Inference is deliberately narrow (int, float, bool, null) and never
/// lossy:
///
/// - `"123"` → `Int`; `"0.9"`, `"1e3"`, `".inf"`, `".nan"` → `Float`
/// - `"true"`, `"false"` (exact lowercase) → `Bool`
/// - `"null"`, `"Null"`, `"NULL"`, `"~"` → `Null`
/// - YAML 1.1 lookalikes (`yes`, `on`, ...), hex/octal/binary spellings,
///   underscores, dates, integers out of `i64` range, `"1e400"`, and
///   anything with surrounding whitespace stay strings.
pub fn parse_scalar(s: &str) -> Value {
    match s {
        "" => return Value::String(String::new()),
        "true" => return Value::Bool(true),
        "false" => return Value::Bool(false),
        "null" | "Null" | "NULL" | "~" => return Value::Null,
        ".inf" | ".Inf" | ".INF" | "+.inf" | "+.Inf" | "+.INF" => {
            return Value::Float(f64::INFINITY)
        }
        "-.inf" | "-.Inf" | "-.INF" => return Value::Float(f64::NEG_INFINITY),
        ".nan" | ".NaN" | ".NAN" => return Value::Float(f64::NAN),
        _ => {}
    }
    if !decimal_numeric(s) {
        return Value::String(s.to_string());
    }
    if let Ok(i) = s.parse::<i64>() {
        return Value::Int(i);
    }
    // Float only for tokens spelled as floats: an integer too large for
    // i64 must not round through f64.
    if s.contains(['.', 'e', 'E']) {
        if let Ok(f) = s.parse::<f64>() {
            if f.is_finite() {
                return Value::Float(f);
            }
        }
    }
    Value::String(s.to_string())
}

/// Characters of a plain decimal integer or float literal only. A gate,
/// not a validator: the number parsers still decide.
fn decimal_numeric(s: &str) -> bool {
    s.chars()
        .all(|c| c.is_ascii_digit() || matches!(c, '+' | '-' | '.' | 'e' | 'E'))
}
