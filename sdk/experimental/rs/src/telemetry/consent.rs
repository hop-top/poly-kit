//! Consent-file reader.
//!
//! The Rust SDK is READ-ONLY against the telemetry consent file. The
//! canonical location is `<XDG_CONFIG_HOME>/kit/config.yaml` at the
//! `kit.telemetry.consent` partition; a pre-refactor layout at
//! `<XDG_CONFIG_HOME>/kit/telemetry.yaml` (bare `telemetry.consent`) is
//! read too, and the two are combined deny-wins, as Go does.
//!
//! Writes belong to Go's consent store and, in this crate, to
//! `consent::FileStore` (feature `consent-file`), which mirror every
//! telemetry decision into both records. Any I/O or parse failure is a
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

/// Resolve the canonical consent path:
/// `<XDG_CONFIG_HOME>/kit/config.yaml` (kit AppConfig under
/// `kit.telemetry.consent`), the config home Go resolves
/// ([`crate::xdg::config_home`]).
pub fn consent_path() -> PathBuf {
    crate::xdg::config_home().join("kit").join("config.yaml")
}

/// Resolve the pre-refactor consent path:
/// `<XDG_CONFIG_HOME>/kit/telemetry.yaml` (bare `telemetry.consent`).
pub fn legacy_consent_path() -> PathBuf {
    crate::xdg::config_home().join("kit").join("telemetry.yaml")
}

/// Where this loader looked before it resolved through `xdg`
/// (`~/.config/kit/`, macOS and Windows with no `XDG_CONFIG_HOME`).
fn old_location_paths() -> Option<(PathBuf, PathBuf)> {
    let kit = crate::xdg::legacy_config_home()?.join("kit");
    Some((kit.join("config.yaml"), kit.join("telemetry.yaml")))
}

/// Load the consent decision. Every error path collapses to
/// `Consent::denied()` so callers don't need to distinguish "file
/// missing" from "file corrupt" — both must default-deny.
///
/// Both records are read and combined deny-wins, as Go's consent store
/// does: canonical `config.yaml` (`kit.telemetry.consent`) and the legacy
/// `telemetry.yaml` (`telemetry.consent`). Any deny is the answer (the
/// canonical one first), whatever the timestamps; allow needs one allow
/// and no deny. A file that exists but cannot be read or parsed fails
/// closed.
///
/// Where kit-rs used to look (`~/.config/kit/` on macOS and Windows with
/// no `XDG_CONFIG_HOME`) is read too, after the current location, and
/// joins the same deny-wins combination.
pub fn load_consent() -> Consent {
    let mut paths = vec![(consent_path(), legacy_consent_path())];
    paths.extend(old_location_paths());
    let mut records = Vec::new();
    for (canonical, legacy) in &paths {
        let (Ok(c), Ok(l)) = (
            read_record(canonical, canonical_block),
            read_record(legacy, legacy_block),
        ) else {
            return Consent::denied();
        };
        records.extend([c, l]);
    }
    let pick = |allowed: bool| records.iter().flatten().find(|c| c.allowed == allowed);
    pick(false)
        .or_else(|| pick(true))
        .cloned()
        .unwrap_or_else(Consent::denied)
}

/// Unreadable: the file exists but does not read or parse.
struct Unreadable;

/// One file's record: `Ok(None)` when the file, the block or a valid
/// state is missing.
fn read_record<W: serde::de::DeserializeOwned>(
    p: &std::path::Path,
    block: fn(W) -> Option<ConsentRaw>,
) -> Result<Option<Consent>, Unreadable> {
    let content = match fs::read_to_string(p) {
        Ok(c) => c,
        Err(e) if e.kind() == std::io::ErrorKind::NotFound => return Ok(None),
        Err(_) => return Err(Unreadable),
    };
    if content.trim().is_empty() {
        return Ok(None);
    }
    let parsed: W = serde_norway::from_str(&content).map_err(|_| Unreadable)?;
    Ok(block(parsed).and_then(consent_from_raw))
}

fn canonical_block(w: CanonicalWrapper) -> Option<ConsentRaw> {
    w.kit?.telemetry?.consent
}

fn legacy_block(w: LegacyWrapper) -> Option<ConsentRaw> {
    w.telemetry?.consent
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
