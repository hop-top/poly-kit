//! Rust runner for the cross-language column-ordering conformance harness.
//!
//! Reads `fixtures/ordering.json`, renders every case in every listed format
//! through the in-tree SDK, then RE-PARSES its own rendered bytes to observe
//! the column sequence the formatter actually serialized. Emits one JSON
//! object per case/format to `KIT_CROSS_LANG_ORDER_OUT`.
//!
//! Re-parsing rather than reporting the input is the point: it is the only
//! way to observe serialized key ORDER. Nothing here sorts keys — and this
//! crate enables serde_json's `preserve_order` so that re-parsing a rendered
//! JSON document yields the emitted order rather than an alphabetical one.
//!
//! The Rust SDK ships table/json/yaml only — csv and text land in a follow-up
//! phase — so extended-format cases report "unsupported" rather than passing
//! silently.

use std::collections::BTreeMap;
use std::fs;
use std::path::PathBuf;

use hop_top_kit::output::{default_registry, parse_options, ColumnSpec};
use serde_json::{json, Map, Value};

/// Column sequence from a whitespace-delimited header line.
///
/// The Rust SDK renders tables through comfy-table, which pads cells; the
/// padding is irrelevant to the contract, so we split on whitespace runs.
fn seq_from_table(text: &str) -> (Vec<String>, bool) {
    let lines: Vec<&str> = text.lines().filter(|l| !l.trim().is_empty()).collect();
    let Some(first) = lines.first() else {
        return (Vec::new(), true);
    };
    let cols: Vec<String> = first
        .split_whitespace()
        // comfy-table draws box-drawing borders in some styles; keep only
        // cells that carry an actual name.
        .filter(|s| s.chars().any(|c| c.is_alphanumeric()))
        .map(str::to_string)
        .collect();
    if cols.is_empty() {
        return (Vec::new(), true);
    }
    (cols, false)
}

fn seq_from_json(text: &str) -> (Vec<String>, bool) {
    if text.trim().is_empty() {
        return (Vec::new(), true);
    }
    let doc: Value = match serde_json::from_str(text) {
        Ok(v) => v,
        Err(_) => return (Vec::new(), true),
    };
    let obj: &Map<String, Value> = match &doc {
        Value::Array(items) => match items.first() {
            Some(Value::Object(m)) => m,
            _ => return (Vec::new(), true),
        },
        Value::Object(m) => m,
        _ => return (Vec::new(), true),
    };
    // With preserve_order the Map is an IndexMap, so keys() is emission order.
    (obj.keys().cloned().collect(), false)
}

/// Minimal ordered YAML key reader. We scrape the emitted text rather than
/// round-tripping through a YAML parser: the only thing under test is the
/// ORDER of the first record's mapping keys, and reading the bytes keeps the
/// observation closest to what the formatter actually wrote.
fn seq_from_yaml(text: &str) -> (Vec<String>, bool) {
    let lines: Vec<&str> = text.lines().filter(|l| !l.trim().is_empty()).collect();
    if lines.is_empty() {
        return (Vec::new(), true);
    }
    if lines.len() == 1 && (lines[0].trim() == "[]" || lines[0].trim() == "---") {
        return (Vec::new(), true);
    }
    let mut keys: Vec<String> = Vec::new();
    let mut base_indent: Option<usize> = None;
    for raw in lines {
        if raw.trim() == "---" {
            continue;
        }
        let mut indent = raw.len() - raw.trim_start().len();
        let mut ln = raw.trim_start();
        if let Some(rest) = ln.strip_prefix("- ") {
            if !keys.is_empty() {
                break;
            }
            indent += 2;
            ln = rest;
        } else if ln == "-" {
            if !keys.is_empty() {
                break;
            }
            continue;
        }
        let Some(colon) = ln.find(':') else { continue };
        let name = ln[..colon].trim();
        if name.is_empty()
            || !name
                .chars()
                .all(|c| c.is_alphanumeric() || c == '_' || c == '-' || c == '.')
        {
            continue;
        }
        let base = *base_indent.get_or_insert(indent);
        if indent != base {
            continue; // nested mapping, not a column
        }
        keys.push(name.to_string());
    }
    let empty = keys.is_empty();
    (keys, empty)
}

fn main() {
    let here = PathBuf::from(env!("CARGO_MANIFEST_DIR"));
    // runners/rs -> cross-lang
    let cross_lang = here.parent().unwrap().parent().unwrap();
    let fixture = cross_lang.join("fixtures").join("ordering.json");
    let doc: Value =
        serde_json::from_str(&fs::read_to_string(&fixture).expect("read ordering.json"))
            .expect("parse ordering.json");

    let out_path = std::env::var("KIT_CROSS_LANG_ORDER_OUT").expect("KIT_CROSS_LANG_ORDER_OUT");

    let portable: Vec<String> = doc["portable_formats"]
        .as_array()
        .unwrap()
        .iter()
        .map(|v| v.as_str().unwrap().to_string())
        .collect();
    let extended: Vec<String> = doc["extended_formats"]
        .as_array()
        .unwrap()
        .iter()
        .map(|v| v.as_str().unwrap().to_string())
        .collect();

    let registry = default_registry();
    let mut records: Vec<BTreeMap<String, Value>> = Vec::new();

    for case in doc["cases"].as_array().unwrap() {
        let name = case["name"].as_str().unwrap();
        let formats = if case["formats"].as_str().unwrap() == "portable" {
            &portable
        } else {
            &extended
        };
        let columns: Option<Vec<ColumnSpec>> = case["spec"].as_array().map(|specs| {
            specs
                .iter()
                .map(|s| {
                    let n = s.as_str().unwrap();
                    // header == key universally (contract rule 3).
                    ColumnSpec::new(n, n, 5)
                })
                .collect()
        });
        let user_cols: Vec<String> = case["cols"]
            .as_array()
            .unwrap()
            .iter()
            .map(|v| v.as_str().unwrap().to_string())
            .collect();
        let rows = &case["rows"];

        for fmt in formats {
            let Some(formatter) = registry.lookup(fmt) else {
                let mut rec = BTreeMap::new();
                rec.insert("case".into(), json!(name));
                rec.insert("format".into(), json!(fmt));
                rec.insert("status".into(), json!("unsupported"));
                records.push(rec);
                continue;
            };

            // Mirror the dispatch-layer precedence rule: --cols wins verbatim
            // (rule 2), else the ColumnSpec list order (rule 1), else empty,
            // which formatters read as "fall back to payload key order".
            // resolve_effective_cols is private to the SDK, so the rule is
            // restated here; the CASES are what verify the SDK agrees.
            let cols: Vec<String> = if !user_cols.is_empty() {
                user_cols.clone()
            } else {
                match &columns {
                    Some(specs) => specs.iter().map(|c| c.header.clone()).collect(),
                    None => Vec::new(),
                }
            };

            let opts = parse_options(Vec::<String>::new(), &formatter.options())
                .expect("parse_options with no pairs");
            let mut buf: Vec<u8> = Vec::new();
            formatter
                .render(&mut buf, rows, &opts, &cols)
                .expect("render");
            let rendered = String::from_utf8(buf).expect("utf8 output");

            let (sequence, empty) = match fmt.as_str() {
                "table" => seq_from_table(&rendered),
                "json" => seq_from_json(&rendered),
                "yaml" => seq_from_yaml(&rendered),
                other => panic!("no extractor for format {other}"),
            };

            let mut rec = BTreeMap::new();
            rec.insert("case".into(), json!(name));
            rec.insert("format".into(), json!(fmt));
            rec.insert("status".into(), json!("ok"));
            rec.insert("sequence".into(), json!(sequence));
            rec.insert("empty".into(), json!(empty));
            records.push(rec);
        }
    }

    // Contract rule 3: a header != key ColumnSpec must not round-trip. Rust
    // enforces with an assert! in ColumnSpec::new, which panics rather than
    // returning an error, so the attempt is made inside catch_unwind. The
    // panic hook is muted for the duration: this panic is the PASSING result,
    // and printing its message would make a green run look like a crash.
    let prev_hook = std::panic::take_hook();
    std::panic::set_hook(Box::new(|_| {}));
    let rejected = std::panic::catch_unwind(|| {
        let _ = ColumnSpec::new("Name", "name", 5);
    })
    .is_err();
    std::panic::set_hook(prev_hook);
    let mut rec = BTreeMap::new();
    rec.insert("case".into(), json!("header-key-enforced"));
    rec.insert("format".into(), json!("-"));
    rec.insert("status".into(), json!("ok"));
    rec.insert("rejected".into(), json!(rejected));
    records.push(rec);

    let mut body = String::new();
    for rec in &records {
        body.push_str(&serde_json::to_string(rec).expect("serialize record"));
        body.push('\n');
    }
    fs::write(&out_path, body).expect("write observations");
}
