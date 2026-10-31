//! Go's two text forms of a payload, so sink output is byte-equal to
//! Go's: `fmt`'s `%v` and `encoding/json`'s `Marshal`.
//!
//! A [`Value`] stands for the Go value a caller would build by hand: an
//! integer is an `int64` (or `uint64`), any other number a `float64`, an
//! object a `map[string]any`. Go prints map keys sorted, so objects are
//! sorted here whatever order the map keeps.

use std::fmt::Write;

use serde_json::{Map, Number, Value};

/// `fmt.Sprintf("%v", v)`.
pub(super) fn verb_v(v: &Value) -> String {
    let mut out = String::new();
    write_v(&mut out, v);
    out
}

fn write_v(out: &mut String, v: &Value) {
    match v {
        Value::Null => out.push_str("<nil>"),
        Value::Bool(b) => out.push_str(if *b { "true" } else { "false" }),
        Value::Number(n) => number(out, n, false),
        Value::String(s) => out.push_str(s),
        Value::Array(a) => {
            out.push('[');
            for (i, x) in a.iter().enumerate() {
                if i > 0 {
                    out.push(' ');
                }
                write_v(out, x);
            }
            out.push(']');
        }
        Value::Object(m) => {
            out.push_str("map[");
            for (i, (k, x)) in sorted(m).into_iter().enumerate() {
                if i > 0 {
                    out.push(' ');
                }
                out.push_str(k);
                out.push(':');
                write_v(out, x);
            }
            out.push(']');
        }
    }
}

/// `json.Marshal(v)`, HTML escaping included.
pub(super) fn write_json(out: &mut String, v: &Value) {
    match v {
        Value::Null => out.push_str("null"),
        Value::Bool(b) => out.push_str(if *b { "true" } else { "false" }),
        Value::Number(n) => number(out, n, true),
        Value::String(s) => json_string(out, s),
        Value::Array(a) => {
            out.push('[');
            for (i, x) in a.iter().enumerate() {
                if i > 0 {
                    out.push(',');
                }
                write_json(out, x);
            }
            out.push(']');
        }
        Value::Object(m) => {
            out.push('{');
            for (i, (k, x)) in sorted(m).into_iter().enumerate() {
                if i > 0 {
                    out.push(',');
                }
                json_string(out, k);
                out.push(':');
                write_json(out, x);
            }
            out.push('}');
        }
    }
}

fn sorted(m: &Map<String, Value>) -> Vec<(&String, &Value)> {
    let mut kv: Vec<_> = m.iter().collect();
    kv.sort_by(|a, b| a.0.cmp(b.0));
    kv
}

/// Go's JSON string: `<`, `>`, `&`, U+2028 and U+2029 escaped, control
/// characters as `\b \f \n \r \t` or `\u00XX`, everything else raw.
pub(super) fn json_string(out: &mut String, s: &str) {
    out.push('"');
    for c in s.chars() {
        match c {
            '"' => out.push_str("\\\""),
            '\\' => out.push_str("\\\\"),
            '\u{08}' => out.push_str("\\b"),
            '\u{0c}' => out.push_str("\\f"),
            '\n' => out.push_str("\\n"),
            '\r' => out.push_str("\\r"),
            '\t' => out.push_str("\\t"),
            '<' | '>' | '&' | '\u{2028}' | '\u{2029}' => {
                let _ = write!(out, "\\u{:04x}", c as u32);
            }
            c if (c as u32) < 0x20 => {
                let _ = write!(out, "\\u{:04x}", c as u32);
            }
            c => out.push(c),
        }
    }
    out.push('"');
}

fn number(out: &mut String, n: &Number, json: bool) {
    if let Some(i) = n.as_i64() {
        let _ = write!(out, "{i}");
    } else if let Some(u) = n.as_u64() {
        let _ = write!(out, "{u}");
    } else if let Some(f) = n.as_f64() {
        float(out, f, json);
    }
}

/// A `float64`, shortest round-trip digits. `%v` is `strconv`'s `'g'`:
/// exponent form below 1e-4 or from 1e6 (Go's shortest `%g` cutoff).
/// JSON is `encoding/json`'s: exponent form below 1e-6 or from 1e21,
/// with `e-07` trimmed to `e-7`.
fn float(out: &mut String, f: f64, json: bool) {
    if f.is_sign_negative() {
        out.push('-');
    }
    let sci = format!("{:e}", f.abs());
    let (mant, exp) = sci.split_once('e').unwrap_or((&sci, "0"));
    let digits: String = mant.chars().filter(|c| *c != '.').collect();
    let exp: i32 = exp.parse().unwrap_or(0);
    let zero = digits == "0";
    let use_exp = if json {
        !zero && !(-6..21).contains(&exp)
    } else {
        !zero && !(-4..6).contains(&exp)
    };
    if use_exp {
        out.push_str(&digits[..1]);
        if digits.len() > 1 {
            out.push('.');
            out.push_str(&digits[1..]);
        }
        out.push('e');
        out.push(if exp < 0 { '-' } else { '+' });
        if json && exp < 0 {
            let _ = write!(out, "{}", exp.unsigned_abs());
        } else {
            let _ = write!(out, "{:02}", exp.unsigned_abs());
        }
    } else if exp < 0 {
        out.push_str("0.");
        for _ in 0..(-exp - 1) {
            out.push('0');
        }
        out.push_str(&digits);
    } else {
        let int_len = exp as usize + 1;
        if digits.len() <= int_len {
            out.push_str(&digits);
            for _ in digits.len()..int_len {
                out.push('0');
            }
        } else {
            out.push_str(&digits[..int_len]);
            out.push('.');
            out.push_str(&digits[int_len..]);
        }
    }
}

#[cfg(test)]
mod tests {
    use super::*;
    use serde_json::json;

    fn json_text(v: &Value) -> String {
        let mut s = String::new();
        write_json(&mut s, v);
        s
    }

    // Expected strings are Go 1.26 output for the same float64 values.
    #[test]
    fn floats_match_go() {
        let cases: [(f64, &str, &str); 14] = [
            (1.0, "1", "1"),
            (1.5, "1.5", "1.5"),
            (123456.0, "123456", "123456"),
            (1e6, "1e+06", "1000000"),
            (12345678.9, "1.23456789e+07", "12345678.9"),
            (1e20, "1e+20", "100000000000000000000"),
            (1e21, "1e+21", "1e+21"),
            (1e100, "1e+100", "1e+100"),
            (1e-4, "0.0001", "0.0001"),
            (1e-5, "1e-05", "0.00001"),
            (1e-6, "1e-06", "0.000001"),
            (1e-7, "1e-07", "1e-7"),
            (5e-324, "5e-324", "5e-324"),
            (-0.0, "-0", "-0"),
        ];
        for (f, v, j) in cases {
            let n = Value::from(f);
            assert_eq!(verb_v(&n), v, "%v of {f:e}");
            assert_eq!(json_text(&n), j, "json of {f:e}");
        }
        assert_eq!(verb_v(&Value::from(0.0)), "0");
    }

    #[test]
    fn composites_match_go() {
        let v = json!({"b": 1, "a": ["x", null, true, {}], "c": {"z": 1.5}});
        assert_eq!(verb_v(&v), "map[a:[x <nil> true map[]] b:1 c:map[z:1.5]]");
        assert_eq!(
            json_text(&v),
            r#"{"a":["x",null,true,{}],"b":1,"c":{"z":1.5}}"#
        );
        assert_eq!(verb_v(&json!([])), "[]");
        assert_eq!(verb_v(&json!(u64::MAX)), "18446744073709551615");
    }

    #[test]
    fn strings_match_go() {
        let s = "<a>&b\u{2028}\u{2029}\u{8}\u{c}\u{1}\u{7f}\t\n\r\"\\é🙂";
        assert_eq!(
            json_text(&json!(s)),
            "\"\\u003ca\\u003e\\u0026b\\u2028\\u2029\\b\\f\\u0001\u{7f}\\t\\n\\r\\\"\\\\é🙂\""
        );
    }
}
