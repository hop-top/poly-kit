//! [`Origin`] and [`Resolved`]: which precedence layer supplied a value.
//! Ports Go `resolved.go`.

use std::fmt;

/// Which layer produced a resolved value. Ordered low to high, so
/// callers may compare (`Flag > Env`).
#[derive(Clone, Copy, Debug, Default, PartialEq, Eq, PartialOrd, Ord, Hash)]
pub enum Origin {
    #[default]
    Default,
    Global,
    Project,
    Env,
    Flag,
}

impl fmt::Display for Origin {
    fn fmt(&self, f: &mut fmt::Formatter<'_>) -> fmt::Result {
        f.write_str(match self {
            Origin::Default => "default",
            Origin::Global => "global",
            Origin::Project => "project",
            Origin::Env => "env",
            Origin::Flag => "flag",
        })
    }
}

/// A value and the layer that supplied it. `detail` points at the exact
/// source (file path, env var, `--flag`).
#[derive(Clone, Debug, Default, PartialEq, Eq)]
pub struct Resolved<T> {
    pub key: String,
    pub value: T,
    pub origin: Origin,
    pub detail: String,
}

/// `value (origin)`.
pub fn format_resolved<T: fmt::Display>(r: &Resolved<T>) -> String {
    format!("{} ({})", r.value, r.origin)
}

/// Left-aligned KEY / VALUE / ORIGIN / DETAIL table, as Go renders it.
pub fn format_resolved_table(entries: &[Resolved<String>]) -> String {
    let mut max_key = "KEY".len();
    let mut max_val = "VALUE".len();
    let mut max_origin = "ORIGIN".len();
    for e in entries {
        max_key = max_key.max(e.key.len());
        max_val = max_val.max(e.value.len());
        max_origin = max_origin.max(e.origin.to_string().len());
    }
    let mut out = format!(
        "{}  {}  {}  DETAIL\n",
        pad("KEY", max_key),
        pad("VALUE", max_val),
        pad("ORIGIN", max_origin)
    );
    out.push_str(&"-".repeat(max_key + max_val + max_origin + 30));
    out.push('\n');
    for e in entries {
        out.push_str(&format!(
            "{}  {}  {}  {}\n",
            pad(&e.key, max_key),
            pad(&e.value, max_val),
            pad(&e.origin.to_string(), max_origin),
            e.detail
        ));
    }
    out
}

/// Go's `%-*s`: widths count runes.
fn pad(s: &str, width: usize) -> String {
    let n = s.chars().count();
    format!("{s}{}", " ".repeat(width.saturating_sub(n)))
}

/// `{key: {value, origin, detail}}` for JSON output.
pub fn resolved_as_json(entries: &[Resolved<String>]) -> serde_json::Value {
    let mut out = serde_json::Map::new();
    for e in entries {
        out.insert(
            e.key.clone(),
            serde_json::json!({
                "value": e.value,
                "origin": e.origin.to_string(),
                "detail": e.detail,
            }),
        );
    }
    serde_json::Value::Object(out)
}

/// A copy sorted by key (stable).
pub fn sorted_by_key(entries: &[Resolved<String>]) -> Vec<Resolved<String>> {
    let mut out = entries.to_vec();
    out.sort_by(|a, b| a.key.cmp(&b.key));
    out
}
