//! Plain-text formatter — kv / lines / paragraph styles.
//!
//! Mirrors go `console/output/text.go` byte-for-byte, which py and ts also
//! reproduce exactly. No library is involved or wanted: the three styles are
//! trivial renderers over an ordered column list, and matching the existing
//! implementations exactly is the entire job.
//!
//! - `style=kv` (default): `HEADER<sep>VALUE\n` per field, blank line
//!   BETWEEN records (never trailing).
//! - `style=lines`: values tab-joined, one record per line, no header.
//! - `style=paragraph`: `Record N:\n` then `  HEADER: VALUE\n` lines, blank
//!   line BETWEEN records. N is 1-indexed.
//!
//! Options:
//!   - `style` (enum kv|lines|paragraph, default kv) — output style.
//!   - `separator` (string, default "=") — kv separator (kv style only).

use std::io::Write;

use serde_json::Value;

use crate::output::formatter::Formatter;
use crate::output::option::{OptionSpec, OptionType, OptionValue, Options};

use super::rows::{normalize, resolve_columns, row_get, stringify};

const STYLE_KV: &str = "kv";
const STYLE_LINES: &str = "lines";
const STYLE_PARAGRAPH: &str = "paragraph";

static OPTS: &[OptionSpec] = &[
    OptionSpec {
        name: "style",
        r#type: OptionType::Enum,
        usage: "output style",
        default: None, // merged at render-time
        r#enum: &[STYLE_KV, STYLE_LINES, STYLE_PARAGRAPH],
    },
    OptionSpec {
        name: "separator",
        r#type: OptionType::String,
        usage: "kv separator (kv style only)",
        default: None,
        r#enum: &[],
    },
];

pub struct TextFormatter;

impl Formatter for TextFormatter {
    fn key(&self) -> &'static str {
        "text"
    }

    fn extensions(&self) -> &'static [&'static str] {
        &[".txt"]
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
        let rows = normalize(data);

        // Zero rows emits nothing. Guarded on ROW count, never on column
        // count — `cols` is populated for row-less payloads too.
        if rows.is_empty() {
            return Ok(());
        }

        let columns = resolve_columns(&rows, cols);

        let style = opts
            .get("style")
            .and_then(OptionValue::as_str)
            .filter(|s| !s.is_empty())
            .unwrap_or(STYLE_KV);

        match style {
            STYLE_KV => {
                let sep = opts
                    .get("separator")
                    .and_then(OptionValue::as_str)
                    .filter(|s| !s.is_empty())
                    .unwrap_or("=");
                render_kv(out, &rows, &columns, sep)
            }
            STYLE_LINES => render_lines(out, &rows, &columns),
            STYLE_PARAGRAPH => render_paragraph(out, &rows, &columns),
            other => Err(std::io::Error::new(
                std::io::ErrorKind::InvalidInput,
                format!("text formatter: unknown style {other:?}"),
            )),
        }
    }
}

fn render_kv(
    out: &mut dyn Write,
    rows: &[&Value],
    columns: &[String],
    sep: &str,
) -> std::io::Result<()> {
    for (i, row) in rows.iter().enumerate() {
        if i > 0 {
            out.write_all(b"\n")?;
        }
        for c in columns {
            let line = format!("{}{}{}\n", c, sep, stringify(row_get(row, c)));
            out.write_all(line.as_bytes())?;
        }
    }
    Ok(())
}

fn render_lines(out: &mut dyn Write, rows: &[&Value], columns: &[String]) -> std::io::Result<()> {
    for row in rows {
        let cells: Vec<String> = columns.iter().map(|c| stringify(row_get(row, c))).collect();
        out.write_all(cells.join("\t").as_bytes())?;
        out.write_all(b"\n")?;
    }
    Ok(())
}

fn render_paragraph(
    out: &mut dyn Write,
    rows: &[&Value],
    columns: &[String],
) -> std::io::Result<()> {
    for (i, row) in rows.iter().enumerate() {
        if i > 0 {
            out.write_all(b"\n")?;
        }
        out.write_all(format!("Record {}:\n", i + 1).as_bytes())?;
        for c in columns {
            let line = format!("  {}: {}\n", c, stringify(row_get(row, c)));
            out.write_all(line.as_bytes())?;
        }
    }
    Ok(())
}
