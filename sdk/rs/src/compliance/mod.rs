//! The 12-factor AI CLI compliance check over a toolspec: a port of the
//! static pass of Go `core/compliance` (the TypeScript and Python
//! `compliance` modules ship the same). [`load_spec`] reads the toolspec
//! YAML ([`spec::ToolSpec`]), [`run_static`] grades it and returns a
//! [`Report`], [`format_report`] renders it as text or JSON.
//!
//! The report is 13 rows (the 12 factors plus Consenting Telemetry);
//! `score` counts the passes and `total` is 12, or 13 for a spec that
//! opts into telemetry. Factors 3, 9 and 10 are runtime-only and skip
//! here; factor 12 skips without `state_introspection.auth_commands`.
//! The runtime pass, which executes the binary, is not ported: Go, TS
//! and Python only.

mod checks;
mod format;
pub mod spec;
mod telemetry;

use std::path::Path;

use serde::Serialize;

pub use format::format_report;
pub use spec::{load_spec, LoadError, ToolSpec};

/// One of the factors, numbered as the spec numbers them.
#[derive(Clone, Copy, Debug, PartialEq, Eq, Hash)]
#[repr(u8)]
pub enum Factor {
    /// 1
    SelfDescribing = 1,
    /// 2
    StructuredIo = 2,
    /// 3
    StreamDiscipline = 3,
    /// 4
    ContractsErrors = 4,
    /// 5
    Preview = 5,
    /// 6
    Idempotency = 6,
    /// 7
    StateTransparency = 7,
    /// 8
    SafeDelegation = 8,
    /// 9
    ObservableOps = 9,
    /// 10
    Provenance = 10,
    /// 11
    Evolution = 11,
    /// 12
    AuthLifecycle = 12,
    /// 13: only graded for specs that opt into telemetry.
    ConsentingTelemetry = 13,
}

impl Factor {
    /// Every factor, in order.
    pub const ALL: [Factor; 13] = [
        Factor::SelfDescribing,
        Factor::StructuredIo,
        Factor::StreamDiscipline,
        Factor::ContractsErrors,
        Factor::Preview,
        Factor::Idempotency,
        Factor::StateTransparency,
        Factor::SafeDelegation,
        Factor::ObservableOps,
        Factor::Provenance,
        Factor::Evolution,
        Factor::AuthLifecycle,
        Factor::ConsentingTelemetry,
    ];

    /// The factor numbered `n` (1 to 13).
    pub fn from_number(n: u8) -> Option<Factor> {
        Factor::ALL.get(usize::from(n).checked_sub(1)?).copied()
    }

    /// The factor's number.
    pub fn number(self) -> u8 {
        self as u8
    }

    /// The contract's spelling of the factor's name.
    pub fn name(self) -> &'static str {
        match self {
            Factor::SelfDescribing => "Self-Describing",
            Factor::StructuredIo => "Structured I/O",
            Factor::StreamDiscipline => "Stream Discipline",
            Factor::ContractsErrors => "Contracts & Errors",
            Factor::Preview => "Preview",
            Factor::Idempotency => "Idempotency",
            Factor::StateTransparency => "State Transparency",
            Factor::SafeDelegation => "Safe Delegation",
            Factor::ObservableOps => "Observable Ops",
            Factor::Provenance => "Provenance",
            Factor::Evolution => "Evolution",
            Factor::AuthLifecycle => "Auth Lifecycle",
            Factor::ConsentingTelemetry => "Consenting Telemetry",
        }
    }
}

impl Serialize for Factor {
    fn serialize<S: serde::Serializer>(&self, s: S) -> Result<S::Ok, S::Error> {
        s.serialize_u8(self.number())
    }
}

/// How a factor graded.
#[derive(Clone, Copy, Debug, PartialEq, Eq, Serialize)]
#[serde(rename_all = "lowercase")]
pub enum Status {
    /// Met.
    Pass,
    /// Not met.
    Fail,
    /// Not graded here (runtime-only, or not applicable).
    Skip,
    /// Met in shape, not in substance.
    Warn,
}

impl Status {
    /// `pass`, `fail`, `skip` or `warn`.
    pub fn as_str(self) -> &'static str {
        match self {
            Status::Pass => "pass",
            Status::Fail => "fail",
            Status::Skip => "skip",
            Status::Warn => "warn",
        }
    }
}

/// The grade of one factor.
#[derive(Clone, Debug, PartialEq, Eq, Serialize)]
pub struct CheckResult {
    /// Which factor.
    pub factor: Factor,
    /// The factor's name.
    pub name: String,
    /// The grade.
    pub status: Status,
    /// What was found.
    #[serde(skip_serializing_if = "String::is_empty")]
    pub details: String,
    /// What to change, for a fail or warn.
    #[serde(skip_serializing_if = "String::is_empty")]
    pub suggestion: String,
}

impl CheckResult {
    pub(crate) fn new(
        factor: Factor,
        status: Status,
        details: impl Into<String>,
        suggestion: impl Into<String>,
    ) -> CheckResult {
        CheckResult {
            factor,
            name: factor.name().to_string(),
            status,
            details: details.into(),
            suggestion: suggestion.into(),
        }
    }
}

/// The grades of one toolspec.
#[derive(Clone, Debug, PartialEq, Eq, Serialize)]
pub struct Report {
    /// The binary graded at runtime; empty for a static pass.
    pub binary: String,
    /// The toolspec path.
    pub toolspec: String,
    /// One row per factor, in order.
    pub results: Vec<CheckResult>,
    /// How many factors passed.
    pub score: usize,
    /// The denominator: 12, or 13 when the spec opts into telemetry.
    pub total: usize,
}

/// Grades the toolspec at `path` without running a binary.
pub fn run_static(path: &Path) -> Result<Report, LoadError> {
    let spec = load_spec(path)?;
    Ok(grade(&spec, &path.to_string_lossy()))
}

/// Grades an already loaded spec; `toolspec` labels the report.
pub fn grade(spec: &ToolSpec, toolspec: &str) -> Report {
    let mut results = checks::run(spec);
    results.push(telemetry::check(spec));
    let total = if telemetry::opted_in(spec) { 13 } else { 12 };
    let score = results.iter().filter(|r| r.status == Status::Pass).count();
    Report {
        binary: String::new(),
        toolspec: toolspec.to_string(),
        results,
        score,
        total,
    }
}
