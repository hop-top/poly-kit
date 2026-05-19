//! Consent-file reader.
//!
//! The Rust SDK is READ-ONLY against the telemetry consent file. The
//! canonical location is `<XDG_CONFIG_HOME>/kit/config.yaml` at the
//! `kit.telemetry.consent` partition. A pre-refactor layout at
//! `<XDG_CONFIG_HOME>/kit/telemetry.yaml` (bare `telemetry.consent` at
//! the top level) is honored as a read-only fallback for installs that
//! have not yet been migrated.
//!
//! The Go-side `kit-consent` tool owns writes; SDKs simply load and
//! respect the decision. Any I/O or parse failure falls through to a
//! default-deny `Consent` so an upgrade can never start a telemetry
//! stream by surprise.

use serde::Deserialize;
use std::fs;
use std::path::PathBuf;

/// Materialized consent decision used by the emitter.
///
/// `allowed` is the only field the emit-gate consults; the rest is
/// metadata adopters may surface (e.g. the consent prompt UI in a CLI
/// can read `prompt_version` to detect a stale decision).
#[derive(Debug, Clone, PartialEq, Eq)]
pub struct Consent {
    /// Whether telemetry emission is allowed. False on any parse
    /// failure, missing file, missing state, or unknown state.
    pub allowed: bool,
    /// Version of the consent prompt the operator answered. Zero when
    /// the file is missing or omits the key.
    pub prompt_version: i64,
    /// Where the decision came from (e.g. "config", "env", "flag").
    /// Defaults to "config" for file-sourced decisions.
    pub decision_source: String,
    /// RFC3339 timestamp the operator decided, if present.
    pub decided_at: Option<String>,
}

impl Consent {
    /// Default-deny consent. Used for every error path.
    pub fn denied() -> Self {
        Self {
            allowed: false,
            prompt_version: 0,
            decision_source: "config".to_string(),
            decided_at: None,
        }
    }
}

#[derive(Deserialize)]
struct CanonicalWrapper {
    kit: Option<CanonicalKit>,
}

#[derive(Deserialize)]
struct CanonicalKit {
    telemetry: Option<TelemetryBlock>,
}

#[derive(Deserialize)]
struct LegacyWrapper {
    telemetry: Option<TelemetryBlock>,
}

#[derive(Deserialize)]
struct TelemetryBlock {
    consent: Option<ConsentRaw>,
}

#[derive(Deserialize)]
struct ConsentRaw {
    state: Option<String>,
    prompt_version: Option<i64>,
    decision_source: Option<String>,
    decided_at: Option<String>,
}

fn xdg_config_home() -> PathBuf {
    std::env::var_os("XDG_CONFIG_HOME")
        .map(PathBuf::from)
        .unwrap_or_else(|| dirs::home_dir().unwrap_or_default().join(".config"))
}

/// Resolve the canonical consent path:
/// `<XDG_CONFIG_HOME>/kit/config.yaml` (kit AppConfig under
/// `kit.telemetry.consent`).
pub fn consent_path() -> PathBuf {
    xdg_config_home().join("kit").join("config.yaml")
}

/// Resolve the pre-refactor consent path:
/// `<XDG_CONFIG_HOME>/kit/telemetry.yaml` (bare `telemetry.consent`).
/// Read-only fallback consumed by [`load_consent`].
pub fn legacy_consent_path() -> PathBuf {
    xdg_config_home().join("kit").join("telemetry.yaml")
}

/// Load the consent decision. Every error path collapses to
/// `Consent::denied()` so callers don't need to distinguish "file
/// missing" from "file corrupt" — both must default-deny.
///
/// Read order: canonical `config.yaml` (`kit.telemetry.consent`) is
/// preferred; the legacy `telemetry.yaml` (`telemetry.consent`) is
/// consulted only when the canonical file is absent or lacks the
/// consent block.
pub fn load_consent() -> Consent {
    if let Some(c) = read_canonical(&consent_path()) {
        return c;
    }
    if let Some(c) = read_legacy(&legacy_consent_path()) {
        return c;
    }
    Consent::denied()
}

fn read_canonical(p: &std::path::Path) -> Option<Consent> {
    let content = fs::read_to_string(p).ok()?;
    let parsed: CanonicalWrapper = serde_yaml::from_str(&content).ok()?;
    let raw = parsed.kit?.telemetry?.consent?;
    consent_from_raw(raw)
}

fn read_legacy(p: &std::path::Path) -> Option<Consent> {
    let content = fs::read_to_string(p).ok()?;
    let parsed: LegacyWrapper = serde_yaml::from_str(&content).ok()?;
    let raw = parsed.telemetry?.consent?;
    consent_from_raw(raw)
}

fn consent_from_raw(c: ConsentRaw) -> Option<Consent> {
    let state = c.state.unwrap_or_default();
    if state != "granted" && state != "denied" {
        return None;
    }
    Some(Consent {
        allowed: state == "granted",
        prompt_version: c.prompt_version.unwrap_or(0),
        decision_source: c.decision_source.unwrap_or_else(|| "config".to_string()),
        decided_at: c.decided_at,
    })
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn denied_factory_shape() {
        let c = Consent::denied();
        assert!(!c.allowed);
        assert_eq!(c.prompt_version, 0);
        assert_eq!(c.decision_source, "config");
        assert!(c.decided_at.is_none());
    }
}
