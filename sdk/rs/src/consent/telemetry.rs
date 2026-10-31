//! Telemetry consent as the `telemetry` capability: the legacy decision
//! record and the resolver precedence a telemetry check runs.

use std::sync::Arc;
use std::time::SystemTime;

use super::grant::{DecisionSource, Duration, Effect, Grant, CAP_TELEMETRY};

/// Env lookup: the value, or `""` when unset.
pub type Env = Arc<dyn Fn(&str) -> String + Send + Sync>;

/// The process environment.
pub fn os_env() -> Env {
    Arc::new(|k| std::env::var(k).unwrap_or_default())
}

/// The persisted telemetry decision (`kit.telemetry.consent`): `state`
/// is `granted`, `denied` or anything else (no decision).
#[derive(Clone, Debug, PartialEq, Eq)]
pub struct TelemetryDecision {
    pub state: String,
    pub decided_at: Option<SystemTime>,
    pub prompt_version: i64,
    pub decision_source: DecisionSource,
}

pub(crate) const GRANTED: &str = "granted";
pub(crate) const DENIED: &str = "denied";

impl TelemetryDecision {
    /// No decision.
    pub fn unknown() -> Self {
        TelemetryDecision {
            state: "unknown".into(),
            decided_at: None,
            prompt_version: 0,
            decision_source: DecisionSource::Unknown(String::new()),
        }
    }

    fn of(state: &str, source: DecisionSource) -> Self {
        TelemetryDecision {
            state: state.into(),
            decision_source: source,
            ..TelemetryDecision::unknown()
        }
    }

    /// The telemetry grant this record is, if it decides.
    pub fn grant(&self) -> Option<Grant> {
        let decision = match self.state.as_str() {
            GRANTED => Effect::Allow,
            DENIED => Effect::Deny,
            _ => return None,
        };
        Some(Grant {
            decided_at: self.decided_at,
            prompt_version: self.prompt_version,
            ..Grant::new(
                CAP_TELEMETRY,
                "",
                decision,
                Duration::Always,
                self.decision_source.clone(),
            )
        })
    }

    /// The record of a telemetry grant.
    pub fn from_grant(g: &Grant) -> Self {
        TelemetryDecision {
            state: if g.decision == Effect::Allow {
                GRANTED
            } else {
                DENIED
            }
            .into(),
            decided_at: g.decided_at,
            prompt_version: g.prompt_version,
            decision_source: g.decision_source.clone(),
        }
    }
}

/// Records of one decision kept in several places, combined: any deny
/// is the answer (the first given), whatever the timestamps; else the
/// first allow; else no decision.
pub fn deny_wins(ds: &[TelemetryDecision]) -> TelemetryDecision {
    ds.iter()
        .find(|d| d.state == DENIED)
        .or_else(|| ds.iter().find(|d| d.state == GRANTED))
        .cloned()
        .unwrap_or_else(TelemetryDecision::unknown)
}

/// Go's `DoNotTrackEnabled`: any non-empty value but `0` or `false`.
pub fn do_not_track(env: &Env) -> bool {
    let v = env("DO_NOT_TRACK").trim().to_lowercase();
    !v.is_empty() && v != "0" && v != "false"
}

/// The resolver precedence without flags: `<APP>_TELEMETRY_MODE=off` or
/// `KIT_TELEMETRY_MODE=off`, then `DO_NOT_TRACK` (denied, env), then
/// `KIT_TELEMETRY_CONSENT=granted|denied` (env; other values count as
/// unset), then the persisted record verbatim, then denied (config).
pub(crate) fn resolve(
    env: &Env,
    app_prefix: &str,
    persisted: &TelemetryDecision,
) -> TelemetryDecision {
    let off = |k: &str| env(k).eq_ignore_ascii_case("off");
    if (!app_prefix.is_empty() && off(&format!("{}_TELEMETRY_MODE", app_prefix.to_uppercase())))
        || off("KIT_TELEMETRY_MODE")
        || do_not_track(env)
    {
        return TelemetryDecision::of(DENIED, DecisionSource::Env);
    }
    if let s @ (GRANTED | DENIED) = env("KIT_TELEMETRY_CONSENT").as_str() {
        return TelemetryDecision::of(s, DecisionSource::Env);
    }
    if persisted.state == GRANTED || persisted.state == DENIED {
        return persisted.clone();
    }
    TelemetryDecision::of(DENIED, DecisionSource::Config)
}
