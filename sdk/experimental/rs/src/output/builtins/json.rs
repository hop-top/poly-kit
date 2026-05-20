//! JSON formatter. Mirrors py/ts/go/php json built-ins.
//!
//! Options:
//!   - `indent` (int, default 2) — number of spaces per indent level; 0 = compact

use std::io::Write;

use serde_json::Value;

use crate::output::formatter::Formatter;
use crate::output::option::{OptionSpec, OptionType, OptionValue, Options};

static OPTS: &[OptionSpec] = &[OptionSpec {
    name: "indent",
    r#type: OptionType::Int,
    usage: "Indent width in spaces (0 = compact)",
    default: None, // Some(OptionValue::Int(2)) not const-constructible; merged in render().
    r#enum: &[],
}];

pub struct JsonFormatter;

impl Formatter for JsonFormatter {
    fn key(&self) -> &'static str {
        "json"
    }

    fn extensions(&self) -> &'static [&'static str] {
        &[".json"]
    }

    fn options(&self) -> &'static [OptionSpec] {
        OPTS
    }

    fn render(
        &self,
        out: &mut dyn Write,
        data: &Value,
        opts: &Options,
        cols: &[String],
    ) -> std::io::Result<()> {
        let indent = opts
            .get("indent")
            .and_then(OptionValue::as_int)
            .unwrap_or(2);
        let projected = project(data, cols);
        if indent <= 0 {
            serde_json::to_writer(&mut *out, &projected)
                .map_err(|e| std::io::Error::new(std::io::ErrorKind::InvalidData, e))?;
        } else {
            let pad = " ".repeat(indent as usize);
            let formatter = serde_json::ser::PrettyFormatter::with_indent(pad.as_bytes());
            let mut ser = serde_json::Serializer::with_formatter(&mut *out, formatter);
            use serde::Serialize;
            projected
                .serialize(&mut ser)
                .map_err(|e| std::io::Error::new(std::io::ErrorKind::InvalidData, e))?;
        }
        out.write_all(b"\n")?;
        Ok(())
    }
}

/// Project `data` against `cols`. Empty `cols` returns data unchanged.
/// Single-row payloads project keys directly; list payloads project each row.
fn project(data: &Value, cols: &[String]) -> Value {
    if cols.is_empty() {
        return data.clone();
    }
    match data {
        Value::Array(rows) => Value::Array(rows.iter().map(|row| project_row(row, cols)).collect()),
        other => project_row(other, cols),
    }
}

fn project_row(row: &Value, cols: &[String]) -> Value {
    let Value::Object(map) = row else {
        return row.clone();
    };
    let mut out = serde_json::Map::new();
    for c in cols {
        if let Some(v) = map.get(c) {
            out.insert(c.clone(), v.clone());
        }
    }
    Value::Object(out)
}
