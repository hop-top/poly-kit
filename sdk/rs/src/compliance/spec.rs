//! The toolspec YAML: the schema Go `toolspec` and the TypeScript and
//! Python loaders read. Every field is optional; an absent one reads as
//! empty, which is what the checks grade.

use std::path::Path;

use serde::de::{Deserializer, Error as _};
use serde::Deserialize;

/// Why a toolspec did not load.
#[derive(Debug, thiserror::Error)]
pub enum LoadError {
    /// The file could not be read.
    #[error("read toolspec: {0}")]
    Read(#[from] std::io::Error),
    /// The YAML did not parse into the toolspec schema.
    #[error("parse toolspec: {0}")]
    Parse(#[from] serde_norway::Error),
}

/// Reads and parses the toolspec at `path`.
pub fn load_spec(path: &Path) -> Result<ToolSpec, LoadError> {
    let raw = std::fs::read_to_string(path)?;
    Ok(serde_norway::from_str(&raw)?)
}

/// A scalar read as the string it spells: `2` and `"2"` are one value,
/// as a YAML string field decodes them.
fn scalar<'de, D: Deserializer<'de>>(d: D) -> Result<String, D::Error> {
    #[derive(Deserialize)]
    #[serde(untagged)]
    enum Scalar {
        Null(()),
        Str(String),
        Bool(bool),
        Int(i64),
        Float(f64),
    }
    match Scalar::deserialize(d).map_err(|_| D::Error::custom("expected a scalar"))? {
        Scalar::Null(()) => Ok(String::new()),
        Scalar::Str(s) => Ok(s),
        Scalar::Bool(b) => Ok(b.to_string()),
        Scalar::Int(n) => Ok(n.to_string()),
        Scalar::Float(f) => Ok(f.to_string()),
    }
}

/// The root of a toolspec.
#[derive(Clone, Debug, Default, Deserialize)]
#[serde(default)]
pub struct ToolSpec {
    /// The tool's name.
    pub name: String,
    /// The spec's schema version (factor 11).
    #[serde(deserialize_with = "scalar")]
    pub schema_version: String,
    /// The command tree.
    pub commands: Vec<Command>,
    /// Tool-wide flags.
    pub flags: Vec<Flag>,
    /// Known failures and their fixes.
    pub error_patterns: Vec<ErrorPattern>,
    /// Multi-step recipes.
    pub workflows: Vec<Workflow>,
    /// Where state and auth can be inspected.
    pub state_introspection: Option<StateIntrospection>,
    /// The telemetry opt-in block (factor 13).
    pub telemetry: Option<Telemetry>,
}

/// A command and its subcommands.
#[derive(Clone, Debug, Default, Deserialize)]
#[serde(default)]
pub struct Command {
    /// The command's name.
    pub name: String,
    /// One-line summary.
    pub short: String,
    /// Other names.
    pub aliases: Vec<String>,
    /// Its flags.
    pub flags: Vec<Flag>,
    /// Its positional arguments.
    pub args: Vec<Arg>,
    /// Exit-code symbols it may produce.
    pub exit_codes: Vec<String>,
    /// Subcommands.
    pub children: Vec<Command>,
    /// Side effects and idempotency.
    pub contract: Option<Contract>,
    /// Risk level.
    pub safety: Option<Safety>,
    /// Ways to preview it, e.g. `--dry-run`.
    pub preview_modes: Vec<String>,
    /// The shape of its output.
    pub output_schema: Option<OutputSchema>,
    /// Whether it is deprecated.
    pub deprecated: bool,
    /// The version it was deprecated in.
    pub deprecated_since: String,
    /// What replaces it.
    pub replaced_by: String,
    /// Commands to suggest next.
    pub suggested_next: Vec<String>,
}

/// A command's contract.
#[derive(Clone, Debug, Default, Deserialize)]
#[serde(default)]
pub struct Contract {
    /// Whether a repeat is safe; `None` when not declared.
    pub idempotent: Option<bool>,
    /// What it changes; non-empty marks the command mutating.
    pub side_effects: Vec<String>,
    /// Whether a failed call may be retried.
    pub retryable: bool,
    /// What must hold first.
    pub pre_conditions: Vec<String>,
}

/// A command's safety metadata.
#[derive(Clone, Debug, Default, Deserialize)]
#[serde(default)]
pub struct Safety {
    /// `safe`, `caution` or `dangerous`.
    pub level: String,
    /// Whether it asks before running.
    pub requires_confirmation: bool,
    /// Permissions it needs.
    pub permissions: Vec<String>,
}

/// A command's output description.
#[derive(Clone, Debug, Default, Deserialize)]
#[serde(default)]
pub struct OutputSchema {
    /// The format, e.g. `json`.
    pub format: String,
    /// Field names.
    pub fields: Vec<String>,
    /// An example.
    pub example: String,
}

/// A flag.
#[derive(Clone, Debug, Default, Deserialize)]
#[serde(default)]
pub struct Flag {
    /// The long name.
    pub name: String,
    /// The short name.
    pub short: String,
    /// The value type.
    #[serde(rename = "type")]
    pub kind: String,
    /// What it does.
    pub description: String,
    /// Whether it must be given.
    pub required: bool,
    /// Whether it is deprecated.
    pub deprecated: bool,
    /// What replaces it.
    pub replaced_by: String,
    /// Its default, as spelled.
    #[serde(deserialize_with = "scalar")]
    pub default: String,
    /// Its closed set of values.
    pub choices: Vec<String>,
}

/// A positional argument.
#[derive(Clone, Debug, Default, Deserialize)]
#[serde(default)]
pub struct Arg {
    /// Its name.
    pub name: String,
    /// Whether it must be given.
    pub required: bool,
    /// Whether it takes every remaining operand.
    pub variadic: bool,
}

/// A known failure and its fix.
#[derive(Clone, Debug, Default, Deserialize)]
#[serde(default)]
pub struct ErrorPattern {
    /// What the message matches.
    pub pattern: String,
    /// What to do.
    pub fix: String,
    /// Why it happens.
    pub cause: String,
    /// More fixes.
    pub fixes: Vec<String>,
}

/// A multi-step recipe.
#[derive(Clone, Debug, Default, Deserialize)]
#[serde(default)]
pub struct Workflow {
    /// Its name.
    pub name: String,
    /// The commands, in order.
    pub steps: Vec<String>,
}

/// Where state and auth can be inspected.
#[derive(Clone, Debug, Default, Deserialize)]
#[serde(default)]
pub struct StateIntrospection {
    /// Commands that show configuration (factor 7).
    pub config_commands: Vec<String>,
    /// Environment variables read.
    pub env_vars: Vec<String>,
    /// Commands that manage auth (factor 12).
    pub auth_commands: Vec<String>,
}

/// The `telemetry:` block.
#[derive(Clone, Debug, Default, Deserialize)]
#[serde(default)]
pub struct Telemetry {
    /// Whether the tool opts into telemetry.
    pub enabled: bool,
    /// Event categories.
    pub categories: Vec<String>,
    /// Where events go.
    pub sinks: Vec<String>,
    /// The consent command, e.g. `mytool telemetry`.
    pub consent_command: String,
    /// The subcommands it exposes.
    pub consent_subcommands: Vec<String>,
    /// Environment variables that switch telemetry off.
    pub kill_switch_envs: Vec<String>,
    /// The consent prompt's version.
    pub prompt_version: String,
    /// The redaction rule set.
    pub redact_rules: String,
}
