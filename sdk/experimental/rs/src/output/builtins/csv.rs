//! CSV formatter.
//!
//! Encoding is hand-rolled rather than delegated to the `csv` crate, and
//! that is deliberate. The three runtimes that already ship a csv formatter
//! do NOT agree on quoting once fields get awkward: go's `encoding/csv`,
//! python's stdlib `csv` and ts's `csv-stringify` are byte-identical on the
//! LF path, while php's `fputcsv` quotes trailing spaces and tabs that the
//! other three leave bare. Each divergence traces to its encoder's own
//! opinions, so adopting a library here would mean adopting that library's
//! opinions and then fighting them back toward the reference. Writing the
//! rule out costs less and cannot drift when an upstream default changes.
//!
//! Go is the reference runtime. Its rule, reproduced exactly below:
//! quote a field iff it contains the active delimiter, a double quote, LF,
//! or CR, **or begins with a space**. Trailing whitespace and tabs are not
//! quoted. Internal quotes are doubled per RFC 4180.
//!
//! CRLF mode also follows go: an embedded LF is rewritten to CRLF inside
//! the quoted field, and a lone CR is DROPPED. ts disagrees on both counts;
//! go wins because go is the stated reference.
//!
//! Options:
//!   - `delimiter` (string, default ",") — single-character field delimiter.
//!   - `no-header` (bool, default false) — omit the header row.
//!   - `quote-all` (bool, default false) — wrap every field in quotes.
//!   - `crlf` (bool, default false) — CRLF line endings instead of LF.

use std::io::Write;

use serde_json::Value;

use crate::output::formatter::Formatter;
use crate::output::option::{OptionSpec, OptionType, OptionValue, Options};

use super::rows::{normalize, resolve_columns, row_get, stringify};

static OPTS: &[OptionSpec] = &[
    OptionSpec {
        name: "delimiter",
        r#type: OptionType::String,
        usage: "field delimiter",
        default: None, // merged at render-time
        r#enum: &[],
    },
    OptionSpec {
        name: "no-header",
        r#type: OptionType::Bool,
        usage: "omit header row",
        default: None,
        r#enum: &[],
    },
    OptionSpec {
        name: "quote-all",
        r#type: OptionType::Bool,
        usage: "quote every field, not just those needing it",
        default: None,
        r#enum: &[],
    },
    OptionSpec {
        name: "crlf",
        r#type: OptionType::Bool,
        usage: "use CRLF line endings (default LF)",
        default: None,
        r#enum: &[],
    },
];

pub struct CsvFormatter;

impl Formatter for CsvFormatter {
    fn key(&self) -> &'static str {
        "csv"
    }

    fn extensions(&self) -> &'static [&'static str] {
        &[".csv"]
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

        // Zero rows emits nothing — not even a bare header row. Guarded on
        // ROW count, never on column count: `cols` is populated for row-less
        // payloads too, so a column-count guard would print a lone header.
        if rows.is_empty() {
            return Ok(());
        }

        let delim_str = opts
            .get("delimiter")
            .and_then(OptionValue::as_str)
            .unwrap_or(",");
        let mut delim_chars = delim_str.chars();
        let (delim, extra) = (delim_chars.next(), delim_chars.next());
        let delim = match (delim, extra) {
            (Some(c), None) => c,
            _ => {
                return Err(std::io::Error::new(
                    std::io::ErrorKind::InvalidInput,
                    "option 'delimiter': delimiter must be exactly one character",
                ))
            }
        };

        let no_header = opts
            .get("no-header")
            .and_then(OptionValue::as_bool)
            .unwrap_or(false);
        let quote_all = opts
            .get("quote-all")
            .and_then(OptionValue::as_bool)
            .unwrap_or(false);
        let crlf = opts
            .get("crlf")
            .and_then(OptionValue::as_bool)
            .unwrap_or(false);
        let eol = if crlf { "\r\n" } else { "\n" };

        let columns = resolve_columns(&rows, cols);

        if !no_header {
            write_row(out, &columns, delim, eol, crlf, quote_all)?;
        }
        for row in &rows {
            let cells: Vec<String> = columns.iter().map(|c| stringify(row_get(row, c))).collect();
            write_row(out, &cells, delim, eol, crlf, quote_all)?;
        }
        Ok(())
    }
}

fn write_row(
    out: &mut dyn Write,
    cells: &[String],
    delim: char,
    eol: &str,
    crlf: bool,
    quote_all: bool,
) -> std::io::Result<()> {
    let mut line = String::new();
    for (i, cell) in cells.iter().enumerate() {
        if i > 0 {
            line.push(delim);
        }
        line.push_str(&encode_field(cell, delim, crlf, quote_all));
    }
    line.push_str(eol);
    out.write_all(line.as_bytes())
}

/// Encode one field per go's `encoding/csv` rules.
fn encode_field(field: &str, delim: char, crlf: bool, quote_all: bool) -> String {
    if !quote_all && !needs_quotes(field, delim) {
        return field.to_string();
    }
    let mut out = String::with_capacity(field.len() + 2);
    out.push('"');
    for ch in field.chars() {
        match ch {
            // RFC 4180: an embedded quote is doubled.
            '"' => out.push_str("\"\""),
            // go rewrites an embedded LF to the active line ending, so a
            // CRLF document stays internally consistent.
            '\n' if crlf => out.push_str("\r\n"),
            // go DROPS a lone CR in CRLF mode: writing it would otherwise
            // manufacture a spurious record separator on re-read.
            '\r' if crlf => {}
            other => out.push(other),
        }
    }
    out.push('"');
    out
}

/// Go quotes a field iff it contains the delimiter, a quote, LF or CR, or
/// begins with a space. Note the asymmetry: a LEADING space forces quoting,
/// a trailing one does not, and a tab never does.
fn needs_quotes(field: &str, delim: char) -> bool {
    if field.starts_with(' ') {
        return true;
    }
    field
        .chars()
        .any(|c| c == delim || c == '"' || c == '\n' || c == '\r')
}
