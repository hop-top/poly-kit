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

use super::rows::{normalize, resolve_columns, row_get, stringify};

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
