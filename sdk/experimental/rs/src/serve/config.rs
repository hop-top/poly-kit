//! The `services.*` configuration keys and the exit-code taxonomy.

use std::collections::HashMap;
use std::time::Duration;

use crate::output::{
    CliError, CODE_GENERIC, CODE_NOT_FOUND, CODE_OK, CODE_UNAUTHORIZED, CODE_USAGE,
};

/// `services.<name>.enabled` key name.
pub const KEY_ENABLED: &str = "enabled";
/// `services.<name>.ready_timeout` key name.
pub const KEY_READY_TIMEOUT: &str = "ready_timeout";
/// `services.<name>.stop_timeout` key name.
pub const KEY_STOP_TIMEOUT: &str = "stop_timeout";
/// `services.failure_policy` key name.
pub const KEY_FAILURE_POLICY: &str = "failure_policy";
/// `services.shutdown_timeout` key name.
pub const KEY_SHUTDOWN_TIMEOUT: &str = "shutdown_timeout";

/// Default budget from start to readiness. `services.<name>.ready_timeout`.
pub const DEFAULT_READY_TIMEOUT: Duration = Duration::from_secs(30);
/// Default budget for one stop. `services.<name>.stop_timeout`.
pub const DEFAULT_STOP_TIMEOUT: Duration = Duration::from_secs(30);
/// Default total shutdown budget. `services.shutdown_timeout`.
pub const DEFAULT_SHUTDOWN_TIMEOUT: Duration = Duration::from_secs(60);

/// Parses a duration in the grammar the contract examples use — Go's:
/// one or more `<number><unit>` groups, concatenated (`1m30s`), where
/// `<number>` is an integer or a decimal and `<unit>` is one of `ns`,
/// `us`, `ms`, `s`, `m`, `h`.
///
/// Stricter than Go in the two places the contract has no use for
/// leniency: a bare number is refused (Go treats `"0"` alone as zero),
/// and a sign is refused (a negative budget has no meaning here). The
/// error names the offending token, so a usage refusal can carry it
/// verbatim.
///
/// Hand-rolled rather than pulled from a crate: clap 4 has no duration
/// value parser, and the crate that would supply one is not in the
/// tree. The grammar is a dozen lines.
pub fn parse_duration(s: &str) -> Result<Duration, String> {
    if s.is_empty() {
        return Err("duration is empty".to_string());
    }
    if s.starts_with('-') || s.starts_with('+') {
        return Err(format!("duration {s:?}: sign is not allowed"));
    }

    let mut total: u128 = 0;
    let mut rest = s;
    while !rest.is_empty() {
        let num_end = rest
            .find(|c: char| !(c.is_ascii_digit() || c == '.'))
            .unwrap_or(rest.len());
        let num = &rest[..num_end];
        rest = &rest[num_end..];

        let unit_end = rest
            .find(|c: char| c.is_ascii_digit() || c == '.')
            .unwrap_or(rest.len());
        let unit = &rest[..unit_end];
        rest = &rest[unit_end..];

        if num.is_empty() {
            return Err(format!("duration {s:?}: missing number before {unit:?}"));
        }
        if unit.is_empty() {
            return Err(format!("duration {s:?}: missing unit after {num:?}"));
        }
        let Some(scale) = unit_nanos(unit) else {
            return Err(format!("duration {s:?}: unknown unit {unit:?}"));
        };
        let nanos = scaled_nanos(num, scale)
            .ok_or_else(|| format!("duration {s:?}: bad number {num:?}"))?;
        total = total
            .checked_add(nanos)
            .ok_or_else(|| format!("duration {s:?}: overflows"))?;
    }

    let secs =
        u64::try_from(total / 1_000_000_000).map_err(|_| format!("duration {s:?}: overflows"))?;
    let nanos = (total % 1_000_000_000) as u32;
    Ok(Duration::new(secs, nanos))
}

/// Nanoseconds in one `unit`, or `None` for a spelling outside the
/// grammar.
fn unit_nanos(unit: &str) -> Option<u128> {
    Some(match unit {
        "ns" => 1,
        "us" => 1_000,
        "ms" => 1_000_000,
        "s" => 1_000_000_000,
        "m" => 60 * 1_000_000_000,
        "h" => 3_600 * 1_000_000_000,
        _ => return None,
    })
}

/// `num` (integer or decimal, ASCII digits and at most one `.`) times
/// `scale` nanoseconds, in integer arithmetic so `1ns` is exactly one
/// nanosecond and `0.1s` exactly a hundred million. A fraction finer
/// than a nanosecond truncates.
fn scaled_nanos(num: &str, scale: u128) -> Option<u128> {
    let (int, frac) = match num.split_once('.') {
        Some((i, f)) => (i, f),
        None => (num, ""),
    };
    if int.is_empty() && frac.is_empty() {
        return None;
    }
    if frac.contains('.') {
        return None;
    }
    let mut total: u128 = 0;
    if !int.is_empty() {
        total = int.parse::<u128>().ok()?.checked_mul(scale)?;
    }
    if !frac.is_empty() {
        let digits = u32::try_from(frac.len()).ok()?;
        let divisor = 10u128.checked_pow(digits)?;
        let f = frac.parse::<u128>().ok()?.checked_mul(scale)? / divisor;
        total = total.checked_add(f)?;
    }
    Some(total)
}

/// The resolved `services.<name>` block for one service. Only the
/// lifecycle keys are modeled; service-specific keys live in the same
/// block and are read by the service itself.
#[derive(Clone, Debug, PartialEq, Eq)]
pub struct ServiceConfig {
    /// `services.<name>.enabled`. Decides whether the supervisor form
    /// starts this service. Defaults to `false`: a service that starts
    /// listening because a dependency upgrade added it to the registry
    /// is an unrequested open port.
    pub enabled: bool,
    /// `services.<name>.ready_timeout`.
    pub ready_timeout: Duration,
    /// `services.<name>.stop_timeout`.
    pub stop_timeout: Duration,
}

impl Default for ServiceConfig {
    fn default() -> Self {
        ServiceConfig {
            enabled: false,
            ready_timeout: DEFAULT_READY_TIMEOUT,
            stop_timeout: DEFAULT_STOP_TIMEOUT,
        }
    }
}

impl ServiceConfig {
    /// A configured-and-enabled block with the default budgets.
    pub fn enabled() -> Self {
        ServiceConfig {
            enabled: true,
            ..ServiceConfig::default()
        }
    }

    /// A configured-but-disabled block with the default budgets.
    pub fn disabled() -> Self {
        ServiceConfig::default()
    }

    /// Sets `ready_timeout`, consuming and returning the block.
    pub fn with_ready_timeout(mut self, d: Duration) -> Self {
        self.ready_timeout = d;
        self
    }

    /// Sets `stop_timeout`, consuming and returning the block.
    pub fn with_stop_timeout(mut self, d: Duration) -> Self {
        self.stop_timeout = d;
        self
    }
}

/// Resolved `services.<name>` blocks, keyed by identifier. A service
/// with no entry is not configured, and the supervisor form skips it.
pub type ServiceConfigs = HashMap<String, ServiceConfig>;

/// The supervisor's answer to "one service failed while others run".
#[derive(Clone, Copy, Debug, Default, PartialEq, Eq)]
pub enum FailurePolicy {
    /// One failure brings every service down. `services.failure_policy`
    /// default.
    #[default]
    FailFast,
    /// One failure leaves the rest running.
    Isolate,
}

impl FailurePolicy {
    /// The wire spelling: `fail-fast` or `isolate`.
    pub fn as_str(&self) -> &'static str {
        match self {
            FailurePolicy::FailFast => "fail-fast",
            FailurePolicy::Isolate => "isolate",
        }
    }

    /// Parses the wire spelling; `None` for anything else.
    pub fn parse(s: &str) -> Option<Self> {
        match s {
            "fail-fast" => Some(FailurePolicy::FailFast),
            "isolate" => Some(FailurePolicy::Isolate),
            _ => None,
        }
    }
}

/// The supervisor-scoped half of the `services` block.
#[derive(Clone, Copy, Debug, PartialEq, Eq)]
pub struct SupervisorConfig {
    pub failure_policy: FailurePolicy,
    pub shutdown_timeout: Duration,
}

impl Default for SupervisorConfig {
    fn default() -> Self {
        SupervisorConfig {
            failure_policy: FailurePolicy::default(),
            shutdown_timeout: DEFAULT_SHUTDOWN_TIMEOUT,
        }
    }
}

/// The kinds of ending a serve run can have.
#[derive(Clone, Copy, Debug, PartialEq, Eq, Hash)]
pub enum LifecycleOutcome {
    CleanStop,
    InvalidSelection,
    ConfigInvalid,
    NoServices,
    UnknownService,
    PolicyDenied,
    StartFailed,
    RuntimeCrash,
    ShutdownTimeout,
}

impl LifecycleOutcome {
    /// The stable identifier carried in the supervisor `stopped`
    /// event's `reason`.
    pub fn as_str(&self) -> &'static str {
        match self {
            LifecycleOutcome::CleanStop => "clean-stop",
            LifecycleOutcome::InvalidSelection => "invalid-selection",
            LifecycleOutcome::ConfigInvalid => "config-invalid",
            LifecycleOutcome::NoServices => "no-services",
            LifecycleOutcome::UnknownService => "unknown-service",
            LifecycleOutcome::PolicyDenied => "policy-denied",
            LifecycleOutcome::StartFailed => "start-failed",
            LifecycleOutcome::RuntimeCrash => "runtime-crash",
            LifecycleOutcome::ShutdownTimeout => "shutdown-timeout",
        }
    }

    /// The `CODE_*` string for the rendered error envelope.
    ///
    /// `StartFailed` and `RuntimeCrash` share `GENERIC`/1 deliberately:
    /// they differ in *when*, not in what an operator does next, and
    /// the distinguishing detail belongs in the message and the failed
    /// event rather than in a second numeric code.
    pub fn code(&self) -> &'static str {
        match self {
            LifecycleOutcome::CleanStop => CODE_OK,
            LifecycleOutcome::InvalidSelection
            | LifecycleOutcome::ConfigInvalid
            | LifecycleOutcome::NoServices => CODE_USAGE,
            LifecycleOutcome::UnknownService => CODE_NOT_FOUND,
            LifecycleOutcome::PolicyDenied => CODE_UNAUTHORIZED,
            LifecycleOutcome::StartFailed
            | LifecycleOutcome::RuntimeCrash
            | LifecycleOutcome::ShutdownTimeout => CODE_GENERIC,
        }
    }

    /// The process exit code, from the contract's exit-behavior table.
    pub fn exit_code(&self) -> i32 {
        match self {
            LifecycleOutcome::CleanStop => 0,
            LifecycleOutcome::InvalidSelection
            | LifecycleOutcome::ConfigInvalid
            | LifecycleOutcome::NoServices => 2,
            LifecycleOutcome::UnknownService => 3,
            LifecycleOutcome::PolicyDenied => 5,
            LifecycleOutcome::StartFailed
            | LifecycleOutcome::RuntimeCrash
            | LifecycleOutcome::ShutdownTimeout => 1,
        }
    }

    /// Whether this outcome exits non-zero.
    pub fn is_failure(&self) -> bool {
        self.exit_code() != 0
    }
}

/// The outcome the process should exit on given everything observed.
///
/// "Worst" is severity, not exit-code magnitude: any failure beats a
/// clean stop, and among failures the first observed wins, because it
/// is the one that explains the rest. Under `isolate` a process may
/// survive several failures, and the exit code must reflect the worst
/// outcome across the whole run rather than the last one.
pub fn worst_outcome(observed: &[LifecycleOutcome]) -> LifecycleOutcome {
    let mut worst = LifecycleOutcome::CleanStop;
    for o in observed {
        if o.is_failure() && !worst.is_failure() {
            worst = *o;
        }
    }
    worst
}

/// Renders `outcome` as the error envelope the command layer returns,
/// carrying the contract's code and exit code.
///
/// A failure wrapping a kit transient error keeps exit 6 unchanged, so
/// an agent's retry branch behaves the same whichever language the tool
/// it is driving was written in — see [`propagate_transient`].
pub fn failure_error(outcome: LifecycleOutcome, failed: &HashMap<String, String>) -> CliError {
    let mut names: Vec<&String> = failed.keys().collect();
    names.sort();
    let mut msg = match outcome {
        LifecycleOutcome::StartFailed => "service failed to start".to_string(),
        LifecycleOutcome::ShutdownTimeout => "shutdown budget exceeded".to_string(),
        _ => "service failed".to_string(),
    };
    for (i, name) in names.iter().enumerate() {
        let sep = if i == 0 { ": " } else { "; " };
        msg.push_str(sep);
        msg.push_str(name);
        msg.push_str(": ");
        msg.push_str(&failed[*name]);
    }
    let mut err = CliError::generic(msg);
    err.code = outcome.code().to_string();
    err.exit_code = outcome.exit_code();
    propagate_transient(err, failed)
}

/// Keeps `TRANSIENT`/exit 6 when every recorded failure was transient.
///
/// The contract's TRANSIENT propagation rule: a serve failure wrapping
/// a transient error keeps exit 6 rather than being flattened to the
/// generic 1, so a retry wrapper still sees the retryable class.
/// Conservative on purpose — one permanent failure among several makes
/// the run permanent, because retrying would not clear it.
fn propagate_transient(err: CliError, failed: &HashMap<String, String>) -> CliError {
    use crate::output::{CODE_TRANSIENT, EXIT_TRANSIENT, TRANSIENCE_TRANSIENT};
    if err.exit_code != 1 || failed.is_empty() {
        return err;
    }
    if !failed
        .values()
        .all(|m| m.starts_with(CODE_TRANSIENT) || m.contains("TRANSIENT:"))
    {
        return err;
    }
    let mut err = err;
    err.code = CODE_TRANSIENT.to_string();
    err.exit_code = EXIT_TRANSIENT;
    err.transience = TRANSIENCE_TRANSIENT.to_string();
    err
}
