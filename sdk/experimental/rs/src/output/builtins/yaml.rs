//! YAML formatter. Mirrors py/ts/go/php yaml built-ins.

use std::io::Write;

use serde_json::Value;

use crate::output::formatter::Formatter;
use crate::output::option::{OptionSpec, OptionType, Options};

static OPTS: &[OptionSpec] = &[OptionSpec {
    name: "explicit_start",
    r#type: OptionType::Bool,
    usage: "Emit a leading '---' document marker",
    default: None,
    r#enum: &[],
}];

pub struct YamlFormatter;

impl Formatter for YamlFormatter {
    fn key(&self) -> &'static str {
        "yaml"
    }

    fn extensions(&self) -> &'static [&'static str] {
        &[".yaml", ".yml"]
    }

    fn options(&self) -> &'static [OptionSpec] {
        OPTS
    }

    fn render(
        &self,
        out: &mut dyn Write,
        data: &Value,
        _opts: &Options,
        cols: &[String],
    ) -> std::io::Result<()> {
        let projected = project(data, cols);
        let s = serde_yaml::to_string(&projected)
            .map_err(|e| std::io::Error::new(std::io::ErrorKind::InvalidData, e))?;
        out.write_all(s.as_bytes())?;
        Ok(())
    }
}

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
