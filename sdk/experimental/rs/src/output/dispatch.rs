//! Resolves output flags from a clap `ArgMatches` and renders data,
//! honoring --format, --format-opt, --cols/--columns, --template,
//! --format-help, and --output/-o per the rules wired by
//! [`super::flags::register_output_flags`].
//!
//! Mirrors py `output.dispatch`, ts `dispatch`, go `output.Dispatch`, php `Dispatcher::dispatch`.

use std::io::Write;
use std::path::Path;
use std::sync::Arc;

use clap::ArgMatches;
use serde_json::Value;
use thiserror::Error;

use super::column::ColumnSpec;
use super::option::{parse_options, ParseError};
use super::registry::{default_registry, Registry};

const STDOUT_SENTINEL: &str = "-";
const DEFAULT_FORMAT: &str = "table";

/// Optional inputs to [`dispatch`].
#[derive(Default)]
pub struct DispatchOptions<'a> {
    /// ColumnSpec list for projection + `--cols` validation.
    pub columns: Option<&'a [ColumnSpec]>,
    /// Override registry (default: process-wide [`default_registry`]).
    pub registry: Option<Arc<Registry>>,
}

#[derive(Debug, Error)]
pub enum DispatchError {
    #[error("output path '{0}' is a directory")]
    OutputIsDir(String),
    #[error("output: cannot open '{path}' for writing: {source}")]
    OpenOutput {
        path: String,
        #[source]
        source: std::io::Error,
    },
    #[error("--format={format} conflicts with --output extension (.{ext} → {ext_key})")]
    FormatExtMismatch {
        format: String,
        ext: String,
        ext_key: &'static str,
    },
    #[error("--template and --cols are mutually exclusive")]
    TemplateAndCols,
    #[error("unknown column '{col}' (valid: {valid})")]
    UnknownCol { col: String, valid: String },
    #[error("unknown output format '{format}' (valid: {valid})")]
    UnknownFormat { format: String, valid: String },
    #[error("{0}")]
    Parse(#[from] ParseError),
    #[error("{0}")]
    Io(#[from] std::io::Error),
}

/// Render `data` through the active formatter resolved from `matches`'s
/// flags. See module doc for resolution order.
pub fn dispatch(
    matches: &ArgMatches,
    writer: &mut dyn Write,
    data: &Value,
    opts: DispatchOptions<'_>,
) -> Result<(), DispatchError> {
    let registry = opts.registry.unwrap_or_else(default_registry);

    // 1. Resolve writer (caller-provided + optional --output redirect).
    let output_path = matches
        .get_one::<String>("output")
        .map(String::as_str)
        .unwrap_or("");
    let mut owned_file;
    let active_writer: &mut dyn Write = if output_path.is_empty() || output_path == STDOUT_SENTINEL
    {
        writer
    } else {
        if Path::new(output_path).is_dir() {
            return Err(DispatchError::OutputIsDir(output_path.to_string()));
        }
        owned_file = std::fs::File::create(output_path).map_err(|e| DispatchError::OpenOutput {
            path: output_path.to_string(),
            source: e,
        })?;
        &mut owned_file
    };

    // 2. --format-help short-circuit.
    if matches
        .try_contains_id("format-help")
        .map(|b| b && matches.value_source("format-help").is_some())
        .unwrap_or(false)
    {
        let scope = matches
            .get_one::<String>("format-help")
            .map(String::as_str)
            .unwrap_or("");
        render_format_help(active_writer, &registry, scope)?;
        return Ok(());
    }

    // 3 + 4. Format resolution.
    let format = resolve_format(matches, &registry, output_path)?;

    // 5. --template escape hatch.
    let template = matches
        .get_one::<String>("template")
        .map(String::as_str)
        .unwrap_or("");
    let cols = resolve_cols(matches);
    if !template.is_empty() {
        if !cols.is_empty() {
            return Err(DispatchError::TemplateAndCols);
        }
        return render_template(active_writer, template, data);
    }

    // 6. Formatter render.
    let formatter = registry
        .lookup(&format)
        .ok_or_else(|| DispatchError::UnknownFormat {
            format: format.clone(),
            valid: registry.keys().join(", "),
        })?;

    let pairs: Vec<String> = matches
        .get_many::<String>("format-opt")
        .map(|it| it.cloned().collect())
        .unwrap_or_default();
    let parsed_opts = parse_options(pairs, formatter.options())?;

    if !cols.is_empty() {
        if let Some(schema) = opts.columns {
            validate_cols(&cols, schema)?;
        }
    }

    formatter.render(active_writer, data, &parsed_opts, &cols)?;
    Ok(())
}

fn resolve_format(
    matches: &ArgMatches,
    registry: &Registry,
    output_path: &str,
) -> Result<String, DispatchError> {
    let explicit = matches
        .value_source("format")
        .map(|src| src == clap::parser::ValueSource::CommandLine)
        .unwrap_or(false);
    let format = matches
        .get_one::<String>("format")
        .cloned()
        .unwrap_or_default();

    let mut ext_key: &'static str = "";
    if !output_path.is_empty() && output_path != STDOUT_SENTINEL {
        if let Some(ext) = Path::new(output_path)
            .extension()
            .and_then(|s| s.to_str())
        {
            let map = registry.extension_map();
            if let Some(k) = map.get(&ext.to_ascii_lowercase()) {
                ext_key = k;
            }
        }
    }

    if explicit {
        if !ext_key.is_empty() && ext_key != format {
            return Err(DispatchError::FormatExtMismatch {
                format,
                ext: Path::new(output_path)
                    .extension()
                    .and_then(|s| s.to_str())
                    .unwrap_or("")
                    .to_string(),
                ext_key,
            });
        }
        return Ok(format);
    }
    if !ext_key.is_empty() {
        return Ok(ext_key.to_string());
    }
    if format.is_empty() {
        Ok(DEFAULT_FORMAT.to_string())
    } else {
        Ok(format)
    }
}

fn resolve_cols(matches: &ArgMatches) -> Vec<String> {
    let mut out = Vec::new();
    for id in ["cols", "columns"] {
        if let Some(values) = matches.get_many::<String>(id) {
            for v in values {
                for piece in v.split(',') {
                    let p = piece.trim();
                    if !p.is_empty() {
                        out.push(p.to_string());
                    }
                }
            }
        }
    }
    out
}

fn validate_cols(cols: &[String], schema: &[ColumnSpec]) -> Result<(), DispatchError> {
    let headers: Vec<&str> = schema.iter().map(|c| c.header.as_str()).collect();
    for c in cols {
        if !headers.iter().any(|h| *h == c.as_str()) {
            return Err(DispatchError::UnknownCol {
                col: c.clone(),
                valid: headers.join(", "),
            });
        }
    }
    Ok(())
}

fn render_template(
    out: &mut dyn Write,
    template: &str,
    data: &Value,
) -> Result<(), DispatchError> {
    let rows: Vec<&Value> = match data {
        Value::Array(arr) => arr.iter().collect(),
        other => vec![other],
    };
    for row in rows {
        let line = substitute(template, row);
        writeln!(out, "{}", line)?;
    }
    Ok(())
}

/// Minimal `{key}` substitution. Mirrors the deliberately-tiny renderer
/// in php Dispatcher; full template-engine parity (eta / Jinja-style) is
/// a Phase-3 follow-up.
fn substitute(template: &str, row: &Value) -> String {
    let mut out = String::with_capacity(template.len());
    let bytes = template.as_bytes();
    let mut i = 0;
    while i < bytes.len() {
        if bytes[i] == b'{' {
            if let Some(close) = template[i + 1..].find('}') {
                let key = &template[i + 1..i + 1 + close];
                if !key.is_empty()
                    && key
                        .chars()
                        .all(|c| c.is_ascii_alphanumeric() || c == '_' || c == '.' || c == '-')
                {
                    if let Some(v) = row.get(key) {
                        match v {
                            Value::String(s) => out.push_str(s),
                            other => out.push_str(&other.to_string()),
                        }
                    }
                    i += 1 + close + 1;
                    continue;
                }
            }
        }
        out.push(bytes[i] as char);
        i += 1;
    }
    out
}

fn render_format_help(
    out: &mut dyn Write,
    registry: &Registry,
    scope: &str,
) -> Result<(), DispatchError> {
    if scope.is_empty() {
        writeln!(out, "Available output formats:")?;
        for f in registry.formatters() {
            writeln!(out, "  {:<8}  ({})", f.key(), f.extensions().join(", "))?;
        }
        return Ok(());
    }
    let Some(f) = registry.lookup(scope) else {
        return Err(DispatchError::UnknownFormat {
            format: scope.to_string(),
            valid: registry.keys().join(", "),
        });
    };
    writeln!(out, "Format: {} ({})", f.key(), f.extensions().join(", "))?;
    for o in f.options() {
        let ty = match o.r#type {
            crate::output::OptionType::String => "string",
            crate::output::OptionType::Int => "int",
            crate::output::OptionType::Bool => "bool",
            crate::output::OptionType::Enum => "enum",
        };
        writeln!(out, "  --format-opt {}=<{}>  {}", o.name, ty, o.usage)?;
    }
    Ok(())
}
