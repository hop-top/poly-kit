//! Mode enum + env precedence.
//!
//! Mirrors `go/runtime/telemetry/mode.go` semantics: three tiers (off,
//! anon, full), <APP>_TELEMETRY_MODE wins over KIT_TELEMETRY_MODE.
//! Unknown tokens map to `Mode::Off + false` so callers can distinguish
//! "operator typoed" from "operator opted out". The SDK does NOT carry
//! the Go atomic-global / one-shot env-read machinery — each
//! `resolve_mode` call reads the environment fresh; Rust adopters who
//! need stickiness should cache the result themselves.

use serde::{Deserialize, Serialize};
use std::env;

/// Telemetry emission tier.
///
/// - `Off`: default; emit is a no-op.
/// - `Anon`: anonymous payload (installation_id + command_path +
///   exit_code + duration_ms + occurred_at + kit_version + sdk).
/// - `Full`: `Anon` plus args + flags, both AFTER redaction.
#[derive(Copy, Clone, Debug, Eq, PartialEq, Serialize, Deserialize)]
#[serde(rename_all = "lowercase")]
pub enum Mode {
    Off,
    Anon,
    Full,
}

/// Parse a Mode token (case-insensitive, leading/trailing whitespace
/// tolerated). Empty/missing input parses as `(Off, true)` so an unset
/// env var doesn't masquerade as an error. Unknown tokens return
/// `(Off, false)` so callers can detect typos.
pub fn parse_mode(s: Option<&str>) -> (Mode, bool) {
    match s {
        None => (Mode::Off, true),
        Some(raw) => {
            let lower = raw.trim().to_lowercase();
            match lower.as_str() {
                "" | "off" => (Mode::Off, true),
                "anon" => (Mode::Anon, true),
                "full" => (Mode::Full, true),
                _ => (Mode::Off, false),
            }
        }
    }
}

/// Read the `KIT_APP_PREFIX` env var, trimmed + upper-cased. Empty
/// string when unset or whitespace-only.
fn resolve_app_prefix() -> String {
    env::var("KIT_APP_PREFIX")
        .unwrap_or_default()
        .trim()
        .to_uppercase()
}

/// Resolve the active Mode from environment in precedence order:
///   1. `<APP>_TELEMETRY_MODE` (if `KIT_APP_PREFIX` is set)
///   2. `KIT_TELEMETRY_MODE`
///   3. `Mode::Off` (default)
///
/// Invalid tokens at any level fall through to the next source; if all
/// sources are unset/invalid the result is `Mode::Off`. This mirrors
/// the Go-side `readEnvMode` precedence (mode.go) without the atomic
/// global — Rust adopters typically resolve once at startup.
pub fn resolve_mode() -> Mode {
    let app = resolve_app_prefix();
    if !app.is_empty() {
        if let Ok(v) = env::var(format!("{}_TELEMETRY_MODE", app)) {
            let (m, ok) = parse_mode(Some(&v));
            if ok {
                return m;
            }
        }
    }
    if let Ok(v) = env::var("KIT_TELEMETRY_MODE") {
        let (m, ok) = parse_mode(Some(&v));
        if ok {
            return m;
        }
    }
    Mode::Off
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn parse_mode_known_tokens() {
        assert_eq!(parse_mode(Some("off")), (Mode::Off, true));
        assert_eq!(parse_mode(Some("OFF")), (Mode::Off, true));
        assert_eq!(parse_mode(Some("anon")), (Mode::Anon, true));
        assert_eq!(parse_mode(Some("ANON")), (Mode::Anon, true));
        assert_eq!(parse_mode(Some(" full ")), (Mode::Full, true));
        assert_eq!(parse_mode(Some("Full")), (Mode::Full, true));
    }

    #[test]
    fn parse_mode_empty_or_missing() {
        assert_eq!(parse_mode(None), (Mode::Off, true));
        assert_eq!(parse_mode(Some("")), (Mode::Off, true));
        assert_eq!(parse_mode(Some("   ")), (Mode::Off, true));
    }

    #[test]
    fn parse_mode_unknown_returns_off_and_false() {
        assert_eq!(parse_mode(Some("garbage")), (Mode::Off, false));
        assert_eq!(parse_mode(Some("anonymous")), (Mode::Off, false));
        assert_eq!(parse_mode(Some("on")), (Mode::Off, false));
    }

    // resolve_mode is exercised via tests/mode.rs — those tests run in
    // their own process so env-var manipulation can't race the api_test
    // tokio runtime. std::env::set_var is unsafe under threading.
}
