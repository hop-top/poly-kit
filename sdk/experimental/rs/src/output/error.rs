//! Structured-error envelope. Mirrors `go/console/output/error.go`.
//!
//! When a command fails under `--format json|yaml`, the error is
//! materialized as a [`CliError`] and rendered to stderr by
//! [`render_error`]. Plaintext mode (`--format table` or unset) prints
//! `"Code: Message\nFix: ...\n"`.
//!
//! Wire keys are snake_case (`code`, `message`, `cause`,
//! `suggested_fix`, `alternatives`, `exit_code`, `transience`); empty
//! optional fields are skipped on serialize, mirroring Go's `omitempty`.

use std::fmt;
use std::io::Write;

use serde::{Deserialize, Serialize};

/// Marks a failure a retry may clear (rate limit, timeout, upstream blip).
pub const TRANSIENCE_TRANSIENT: &str = "transient";
/// Marks a failure retrying cannot clear without changing the input or
/// the environment.
pub const TRANSIENCE_PERMANENT: &str = "permanent";
/// Marks a failure kit cannot classify. Agents should treat retries as
/// best-effort and bounded.
pub const TRANSIENCE_UNKNOWN: &str = "unknown";

// Standard codes mapping the cross-tool exit codes (conventions §8.1).
pub const CODE_OK: &str = "OK"; // exit 0
pub const CODE_GENERIC: &str = "GENERIC"; // exit 1
pub const CODE_USAGE: &str = "USAGE"; // exit 2
pub const CODE_NOT_FOUND: &str = "NOT_FOUND"; // exit 3
pub const CODE_CONFLICT: &str = "CONFLICT"; // exit 4
pub const CODE_UNAUTHORIZED: &str = "UNAUTHORIZED"; // exit 5
pub const CODE_TRANSIENT: &str = "TRANSIENT"; // exit 6 — Factor-11 transient/retryable failure
pub const CODE_PROVENANCE_MISSING: &str = "PROVENANCE_MISSING"; // exit 65 — Factor-12 strict-mode refusal
pub const CODE_RATE_LIMITED: &str = "RATE_LIMITED"; // exit 64 — Factor-10 max-ops budget exceeded

/// Spec-assigned exit code for the generic failure class: the command
/// failed and no narrower code applies. Pair it with
/// [`CliError::generic`] rather than hand-rolling exit 1, so the
/// envelope carries a transience class.
pub const EXIT_GENERIC: i32 = 1;
/// Spec-assigned exit code for transient/retryable failures (Factor 11).
/// Agents branch on it before parsing stderr: exit 6 means a retry may
/// clear the failure.
pub const EXIT_TRANSIENT: i32 = 6;
/// Conventional exit code for Factor-10 rate-limit refusals.
pub const EXIT_RATE_LIMITED: i32 = 64;
/// Conventional exit code for Factor-12 strict-mode provenance refusals.
/// Lives at 65 in kit's extension band (alongside RATE_LIMITED at 64):
/// the spec reserves 0-6 for its core taxonomy and leaves >6 to
/// per-tool codes, and kit as a library stays out of the low per-tool
/// range.
pub const EXIT_PROVENANCE_MISSING: i32 = 65;

/// Returns the default transience class for one of the standard codes.
/// Unrecognized (adopter-defined) codes map to [`TRANSIENCE_UNKNOWN`];
/// adopters set `transience` (or use [`CliError::with_transience`]) to
/// classify their own codes.
pub fn transience_for_code(code: &str) -> &'static str {
    match code {
        CODE_USAGE
        | CODE_NOT_FOUND
        | CODE_CONFLICT
        | CODE_UNAUTHORIZED
        | CODE_PROVENANCE_MISSING => TRANSIENCE_PERMANENT,
        CODE_RATE_LIMITED | CODE_TRANSIENT => TRANSIENCE_TRANSIENT,
        _ => TRANSIENCE_UNKNOWN,
    }
}

/// Structured-error envelope rendered to stderr when `--format
/// json|yaml` is set.
///
/// `transience` classifies the failure for retry decisions (Factor 4):
/// transient (retry-worthy), permanent (do not retry), or unknown.
/// Constructors populate it; [`render_error`] normalizes an unset value
/// to unknown so every structured error carries a valid class on the
/// wire.
#[derive(Debug, Clone, Default, PartialEq, Eq, Serialize, Deserialize)]
pub struct CliError {
    pub code: String,
    pub message: String,
    #[serde(default, skip_serializing_if = "String::is_empty")]
    pub cause: String,
    #[serde(default, skip_serializing_if = "String::is_empty")]
    pub suggested_fix: String,
    #[serde(default, skip_serializing_if = "Vec::is_empty")]
    pub alternatives: Vec<String>,
    pub exit_code: i32,
    #[serde(default, skip_serializing_if = "String::is_empty")]
    pub transience: String,
}

impl CliError {
    /// Builds an envelope from a displayable error, mirroring Go's
    /// `WrapError`: the message is `err`'s rendering, transience
    /// defaults from the code via [`transience_for_code`]. Rust's
    /// ownership model keeps the envelope plain data, so unlike Go the
    /// source error is not retained; wrap at the call site if the chain
    /// matters.
    pub fn wrap(err: impl fmt::Display, code: impl Into<String>, exit_code: i32) -> Self {
        let code = code.into();
        let transience = transience_for_code(&code).to_string();
        CliError {
            message: err.to_string(),
            code,
            exit_code,
            transience,
            ..CliError::default()
        }
    }

    /// CODE_GENERIC envelope with exit code 1. The catch-all for
    /// failures no narrower code describes; permanent because retrying
    /// the same input in the same environment is not expected to help.
    /// Wrapping an arbitrary error as CODE_GENERIC via
    /// [`CliError::wrap`] still defaults to unknown.
    pub fn generic(message: impl Into<String>) -> Self {
        Self::standard(CODE_GENERIC, message, EXIT_GENERIC, TRANSIENCE_PERMANENT)
    }

    /// CODE_NOT_FOUND envelope with exit code 3.
    pub fn not_found(message: impl Into<String>) -> Self {
        Self::standard(CODE_NOT_FOUND, message, 3, TRANSIENCE_PERMANENT)
    }

    /// CODE_CONFLICT envelope with exit code 4.
    pub fn conflict(message: impl Into<String>) -> Self {
        Self::standard(CODE_CONFLICT, message, 4, TRANSIENCE_PERMANENT)
    }

    /// CODE_UNAUTHORIZED envelope with exit code 5.
    pub fn unauthorized(message: impl Into<String>) -> Self {
        Self::standard(CODE_UNAUTHORIZED, message, 5, TRANSIENCE_PERMANENT)
    }

    /// CODE_USAGE envelope with exit code 2.
    pub fn usage(message: impl Into<String>) -> Self {
        Self::standard(CODE_USAGE, message, 2, TRANSIENCE_PERMANENT)
    }

    /// CODE_TRANSIENT envelope with exit code 6 (Factor 11). Use it for
    /// failures a retry may clear: upstream timeouts, connection
    /// resets, service-unavailable responses.
    pub fn transient(message: impl Into<String>) -> Self {
        Self::standard(
            CODE_TRANSIENT,
            message,
            EXIT_TRANSIENT,
            TRANSIENCE_TRANSIENT,
        )
    }

    /// CODE_RATE_LIMITED envelope with exit code 64 (Factor 10).
    pub fn rate_limited(message: impl Into<String>) -> Self {
        Self::standard(
            CODE_RATE_LIMITED,
            message,
            EXIT_RATE_LIMITED,
            TRANSIENCE_TRANSIENT,
        )
    }

    /// CODE_PROVENANCE_MISSING envelope with exit code 65 (Factor 12).
    /// `detail` is a free-form string suitable for the `cause` slot
    /// (typically the JSON-pointer list of offending fields).
    pub fn provenance_missing(detail: impl Into<String>) -> Self {
        CliError {
            code: CODE_PROVENANCE_MISSING.to_string(),
            message: "provenance not recorded for one or more output fields".to_string(),
            cause: detail.into(),
            suggested_fix: "record provenance for synthesized/cached fields before rendering"
                .to_string(),
            exit_code: EXIT_PROVENANCE_MISSING,
            transience: TRANSIENCE_PERMANENT.to_string(),
            ..CliError::default()
        }
    }

    /// Returns the envelope with `transience` set, every other field
    /// untouched. Consuming builder — the Rust analogue of Go's
    /// copy-on-set `WithTransience` (clone first to keep the original).
    pub fn with_transience(mut self, transience: impl Into<String>) -> Self {
        self.transience = transience.into();
        self
    }

    fn standard(code: &str, message: impl Into<String>, exit_code: i32, transience: &str) -> Self {
        CliError {
            code: code.to_string(),
            message: message.into(),
            exit_code,
            transience: transience.to_string(),
            ..CliError::default()
        }
    }
}

impl fmt::Display for CliError {
    fn fmt(&self, f: &mut fmt::Formatter<'_>) -> fmt::Result {
        if self.code.is_empty() {
            write!(f, "{}", self.message)
        } else {
            write!(f, "{}: {}", self.code, self.message)
        }
    }
}

impl std::error::Error for CliError {}

/// Writes `err` to `out` in the requested format. `format == ""` or
/// `"table"` renders human-readable plain text (`"Code: Message\nFix:
/// ..."`); `json`/`yaml` render the envelope structurally. An unset
/// transience is normalized to [`TRANSIENCE_UNKNOWN`] on the wire
/// (Factor 4) without mutating `err`. The caller decides the exit code
/// from `err.exit_code` after rendering.
pub fn render_error(out: &mut dyn Write, format: &str, err: &CliError) -> std::io::Result<()> {
    let normalized;
    let err = if err.transience.is_empty() {
        normalized = err.clone().with_transience(TRANSIENCE_UNKNOWN);
        &normalized
    } else {
        err
    };
    match format {
        "json" => {
            let s = serde_json::to_string_pretty(err)
                .map_err(|e| std::io::Error::new(std::io::ErrorKind::InvalidData, e))?;
            out.write_all(s.as_bytes())?;
            out.write_all(b"\n")
        }
        "yaml" => {
            let s = serde_yaml::to_string(err)
                .map_err(|e| std::io::Error::new(std::io::ErrorKind::InvalidData, e))?;
            out.write_all(s.as_bytes())
        }
        _ => render_error_plain(out, err),
    }
}

/// Human-readable form used by `--format table` (and the default empty
/// format). Each populated field appears on its own line so the output
/// is grep-friendly.
fn render_error_plain(out: &mut dyn Write, err: &CliError) -> std::io::Result<()> {
    if err.code.is_empty() {
        writeln!(out, "{}", err.message)?;
    } else {
        writeln!(out, "{}: {}", err.code, err.message)?;
    }
    if !err.cause.is_empty() {
        writeln!(out, "Cause: {}", err.cause)?;
    }
    if !err.suggested_fix.is_empty() {
        writeln!(out, "Fix: {}", err.suggested_fix)?;
    }
    for alt in &err.alternatives {
        writeln!(out, "Alternative: {alt}")?;
    }
    Ok(())
}
