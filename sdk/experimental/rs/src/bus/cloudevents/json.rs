//! Structured-mode JSON for [`CloudEvent`]. Mirrors
//! `go/runtime/bus/cloudevents_json.go`.
//!
//! Decoding works on member text, not on [`serde_json::Value`]s, the way
//! Go decodes into `map[string]json.RawMessage`: `data` keeps its exact
//! text (compacted), and extension integers are judged on their literal
//! (`1.0` and `1e3` are not integers, `-0` is, and a literal beyond
//! `i64` is out of range). `serde_json` still checks the syntax and
//! decodes strings, so escapes are handled once, by the same parser.

use std::collections::BTreeMap;

use serde::de::IgnoredAny;

use super::rules::{
    decode_base64, encode_base64, is_json_integer, parse_time, upper_time, valid_extension_name,
};
use super::{extension_label, CloudEvent, CloudEventError, Data, ExtensionValue};
use crate::bus::event::canonical_timestamp;

/// Whether `text` is one JSON document (`json.Valid`).
pub(super) fn valid(text: &str) -> bool {
    serde_json::from_str::<IgnoredAny>(text).is_ok()
}

/// The top-level members of a valid JSON document, as (unescaped name,
/// raw value text), or `None` when the document is not an object. A
/// repeated name keeps its last value, as Go's map decode does.
pub(super) fn members(doc: &str) -> Option<BTreeMap<String, &str>> {
    let b = doc.as_bytes();
    let mut i = skip_ws(b, 0);
    if b.get(i) != Some(&b'{') {
        return None;
    }
    let mut out = BTreeMap::new();
    i = skip_ws(b, i + 1);
    if b.get(i) == Some(&b'}') {
        return Some(out);
    }
    loop {
        let key_end = skip_value(b, i);
        let key: String = serde_json::from_str(&doc[i..key_end]).ok()?;
        i = skip_ws(b, key_end);
        i = skip_ws(b, i + 1); // ':'
        let end = skip_value(b, i);
        out.insert(key, &doc[i..end]);
        i = skip_ws(b, end);
        if b.get(i) != Some(&b',') {
            return Some(out);
        }
        i = skip_ws(b, i + 1);
    }
}

fn skip_ws(b: &[u8], mut i: usize) -> usize {
    while matches!(b.get(i), Some(b' ' | b'\t' | b'\n' | b'\r')) {
        i += 1;
    }
    i
}

/// The end of the value starting at `i` in valid JSON.
fn skip_value(b: &[u8], mut i: usize) -> usize {
    let mut depth = 0usize;
    let mut in_str = false;
    while i < b.len() {
        let c = b[i];
        i += 1;
        if in_str {
            match c {
                b'\\' => i += 1,
                b'"' => {
                    in_str = false;
                    if depth == 0 {
                        return i;
                    }
                }
                _ => {}
            }
            continue;
        }
        match c {
            b'"' => in_str = true,
            b'{' | b'[' => depth += 1,
            b'}' | b']' if depth > 0 => {
                depth -= 1;
                if depth == 0 {
                    return i;
                }
            }
            b',' | b'}' | b']' | b' ' | b'\t' | b'\n' | b'\r' if depth == 0 => return i - 1,
            _ => {}
        }
    }
    i
}

/// Valid JSON with insignificant whitespace removed (`json.Compact`).
pub(super) fn compact(text: &str) -> String {
    let mut out = String::with_capacity(text.len());
    let mut in_str = false;
    let mut escaped = false;
    for c in text.chars() {
        if in_str {
            out.push(c);
            if escaped {
                escaped = false;
            } else if c == '\\' {
                escaped = true;
            } else if c == '"' {
                in_str = false;
            }
        } else if !matches!(c, ' ' | '\t' | '\n' | '\r') {
            in_str = c == '"';
            out.push(c);
        }
    }
    out
}

/// A JSON string literal.
fn quote(s: &str) -> String {
    serde_json::to_string(s).expect("strings always encode")
}

/// The string attributes in the order the decoder checks them.
const STRING_ATTRS: [&str; 8] = [
    "specversion",
    "id",
    "source",
    "type",
    "subject",
    "time",
    "datacontenttype",
    "dataschema",
];

pub(super) fn encode(c: &CloudEvent) -> Result<String, CloudEventError> {
    c.validate()?;
    let mut out = String::from("{");
    let mut member = |name: &str, raw: &str| {
        if out.len() > 1 {
            out.push(',');
        }
        out.push_str(&quote(name));
        out.push(':');
        out.push_str(raw);
    };
    let time = c.instant().filter(|t| !t.is_zero()).map(|t| t.format_utc());
    for (name, v) in [
        ("specversion", c.spec_version.as_str()),
        ("id", &c.id),
        ("source", &c.source),
        ("type", &c.event_type),
        ("subject", &c.subject),
        ("time", time.as_deref().unwrap_or_default()),
        ("datacontenttype", &c.data_content_type),
        ("dataschema", &c.data_schema),
    ] {
        if !v.is_empty() {
            member(name, &quote(v));
        }
    }
    for (name, v) in &c.extensions {
        let raw = match v {
            ExtensionValue::String(s) => quote(s),
            ExtensionValue::Bool(b) => b.to_string(),
            ExtensionValue::Integer(n) => n.to_string(),
        };
        member(name, &raw);
    }
    match &c.data {
        Some(Data::Json(text)) => member("data", &compact(text)),
        Some(Data::Binary(bytes)) => member("data_base64", &quote(&encode_base64(bytes))),
        None => {}
    }
    out.push('}');
    Ok(out)
}

pub(super) fn decode(input: &[u8]) -> Result<CloudEvent, CloudEventError> {
    // Go's decoder reads invalid UTF-8 in strings as U+FFFD; vectors
    // avoid it, so this only keeps the input a `str`.
    let doc = String::from_utf8_lossy(input);
    if !valid(&doc) {
        return Err(CloudEventError::document("invalid JSON"));
    }
    let mut members =
        members(&doc).ok_or_else(|| CloudEventError::document("not a JSON object"))?;
    members.retain(|_, raw| *raw != "null");

    let mut out = CloudEvent::default();
    for name in STRING_ATTRS {
        let Some(raw) = members.remove(name) else {
            continue;
        };
        let s: String = serde_json::from_str(raw)
            .map_err(|_| CloudEventError::new(name, "must be a string"))?;
        if s.is_empty() && matches!(name, "subject" | "datacontenttype" | "dataschema" | "time") {
            return Err(CloudEventError::new(name, "must not be empty"));
        }
        let slot = match name {
            "specversion" => &mut out.spec_version,
            "id" => &mut out.id,
            "source" => &mut out.source,
            "type" => &mut out.event_type,
            "subject" => &mut out.subject,
            "datacontenttype" => &mut out.data_content_type,
            "dataschema" => &mut out.data_schema,
            _ => {
                let invalid = || CloudEventError::new("time", "invalid RFC 3339 timestamp");
                parse_time(&s).ok_or_else(invalid)?;
                out.time = canonical_timestamp(&upper_time(&s)).map_err(|_| invalid())?;
                continue;
            }
        };
        *slot = s;
    }

    let data = members.remove("data");
    let b64 = members.remove("data_base64");
    match (data, b64) {
        (Some(_), Some(_)) => {
            return Err(CloudEventError::new(
                "data",
                "data and data_base64 are mutually exclusive",
            ))
        }
        (Some(raw), None) => out.data = Some(Data::Json(compact(raw))),
        (None, Some(raw)) => {
            let s: String = serde_json::from_str(raw)
                .map_err(|_| CloudEventError::new("data_base64", "must be a string"))?;
            let bytes = decode_base64(&s)
                .ok_or_else(|| CloudEventError::new("data_base64", "invalid base64"))?;
            out.data = Some(Data::Binary(bytes));
        }
        (None, None) => {}
    }

    for (name, raw) in members {
        if !valid_extension_name(&name) {
            return Err(CloudEventError::new(
                &extension_label(&name),
                "invalid extension name",
            ));
        }
        let value = decode_extension(&name, raw)?;
        out.extensions.insert(name, value);
    }

    out.validate()?;
    Ok(out)
}

/// One JSON extension value as a string, bool or `i64`, judged on its
/// literal as Go's `json.Number` is.
fn decode_extension(name: &str, raw: &str) -> Result<ExtensionValue, CloudEventError> {
    let unsupported = || CloudEventError::new(name, "unsupported extension value type");
    match raw.as_bytes().first() {
        Some(b'"') => Ok(ExtensionValue::String(
            serde_json::from_str(raw).map_err(|_| unsupported())?,
        )),
        Some(b't') => Ok(ExtensionValue::Bool(true)),
        Some(b'f') => Ok(ExtensionValue::Bool(false)),
        Some(b'-' | b'0'..=b'9') if is_json_integer(raw) => raw
            .parse::<i64>()
            .ok()
            .filter(|n| i32::try_from(*n).is_ok())
            .map(ExtensionValue::Integer)
            .ok_or_else(|| CloudEventError::new(name, "integer out of range")),
        _ => Err(unsupported()),
    }
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn scans_members() {
        let doc = r#" { "a" : [1, {"x": "}"}] , "b\"" :"s" , "c":-1.5e3,"a":true } "#;
        let m = members(doc).unwrap();
        assert_eq!(m["a"], "true");
        assert_eq!(m["b\""], r#""s""#);
        assert_eq!(m["c"], "-1.5e3");
        assert_eq!(members("{}").unwrap().len(), 0);
        assert!(members("[1]").is_none());
        assert!(members(" null ").is_none());
    }

    #[test]
    fn compacts() {
        assert_eq!(
            compact(" { \"a b\" : [ 1 , \"x\\\" y\" ] } "),
            r#"{"a b":[1,"x\" y"]}"#
        );
    }
}
