//! Plain ASCII table formatter — the default --format.
//!
//! Mirrors the cli-php TableFormatter: header line + space-separated
//! body, no borders, no color, pipe-/grep-friendly. Adopters wanting
//! richer tables (borders, UTF-8 box-drawing, theming) override the
//! 'table' key in the Registry with their own Formatter.
//!
//! Renderer: built on `comfy-table::Preset::NOTHING` so column widths
//! are computed for us and we don't reinvent padding math.
//!
//! Options:
//!   - `header` (bool, default true) — emit the header row.

use std::io::Write;

use comfy_table::presets::NOTHING;
use comfy_table::{Cell, ContentArrangement, Row, Table};
use serde_json::Value;

use crate::output::formatter::Formatter;
use crate::output::option::{OptionSpec, OptionType, OptionValue, Options};

static OPTS: &[OptionSpec] = &[OptionSpec {
    name: "header",
    r#type: OptionType::Bool,
    usage: "Emit a header row (default: true)",
    default: None, // merged at render-time
    r#enum: &[],
}];

pub struct TableFormatter;

impl Formatter for TableFormatter {
    fn key(&self) -> &'static str {
        "table"
    }

    fn extensions(&self) -> &'static [&'static str] {
        &[]
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
        let header = opts
            .get("header")
            .and_then(OptionValue::as_bool)
            .unwrap_or(true);
        let rows = normalize(data);

        // Zero rows emits nothing — not even a bare header row. Emptiness is
        // decided by ROW count, never by header count: `cols` is non-empty
        // whenever the caller supplied a ColumnSpec list, so guarding on the
        // column count would print a lone header for an empty payload.
        if rows.is_empty() {
            return Ok(());
        }

        let columns = resolve_columns(&rows, cols);

        let mut table = Table::new();
        table
            .load_preset(NOTHING)
            .set_content_arrangement(ContentArrangement::Disabled);

        if header && !columns.is_empty() {
            table.set_header(columns.iter().map(Cell::new).collect::<Vec<_>>());
        }

        for row in &rows {
            let cells: Vec<Cell> = columns
                .iter()
                .map(|c| Cell::new(stringify(row_get(row, c))))
                .collect();
            table.add_row(Row::from(cells));
        }

        writeln!(out, "{}", table)?;
        Ok(())
    }
}

/// Always returns a list of rows; a single map becomes a one-row table.
fn normalize(data: &Value) -> Vec<&Value> {
    match data {
        Value::Array(arr) => arr.iter().collect(),
        other => vec![other],
    }
}

/// Honor the resolved column list — already `--cols` order, or the
/// caller's ColumnSpec order, whichever the dispatcher settled on.
/// An empty list means neither was supplied, so infer from the first
/// object-shaped row; that fallback preserves the payload's own key
/// order because serde_json is built with `preserve_order`.
fn resolve_columns(rows: &[&Value], cols: &[String]) -> Vec<String> {
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

fn row_get<'a>(row: &'a Value, key: &str) -> Option<&'a Value> {
    if let Value::Object(map) = row {
        map.get(key)
    } else {
        None
    }
}

fn stringify(val: Option<&Value>) -> String {
    match val {
        None | Some(Value::Null) => String::new(),
        Some(Value::String(s)) => s.clone(),
        Some(Value::Bool(b)) => b.to_string(),
        Some(Value::Number(n)) => n.to_string(),
        // Arrays / objects: compact JSON keeps cells single-line.
        Some(other) => serde_json::to_string(other).unwrap_or_default(),
    }
}
