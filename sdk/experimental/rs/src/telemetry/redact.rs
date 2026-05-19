//! Best-effort PII + token-prefix redactor.
//!
//! Mirrors the SDK-side redactor contract from ADR-0038 §3:
//!
//! - Email addresses → `<redacted:email>`.
//! - IPv4 dotted-quad → `<redacted:ipv4>`.
//! - IPv6 colon-separated → `<redacted:ipv6>`.
//! - Bearer-style token prefixes (`sk-…`, `ghp_…`/`gho_…`/`ghu_…`/
//!   `ghs_…`/`ghr_…`, `xoxb-…`) → `<redacted:token>`.
//! - The current user's `$HOME` path prefix → the literal string
//!   `"$HOME"`.
//!
//! This is NOT parity with `go/core/redact`. The Go redactor is
//! stateful, policy-driven, and config-aware; this implementation is a
//! cheap defense-in-depth pass that runs unconditionally after any
//! caller-supplied custom redactor. Adopters with stricter needs:
//!
//! 1. Pass `ClientOptions::redactor` (see `client.rs`). The custom
//!    callback runs FIRST; this default redactor runs after so a
//!    permissive callback can't smuggle PII past the wire.
//! 2. Route SDK events through a Go-side collector that re-emits via
//!    `go/core/redact` (the recommended path for compliance-sensitive
//!    contexts — see ADR-0038 §3).
//!
//! The regex set is intentionally narrow: the cross-language contract
//! test (`hops/main/sdk/tests/cross-lang/telemetry/`) asserts byte
//! parity of the deterministic `"<redacted:kind>"` placeholders across
//! py/ts/rs/php, so the regex bodies MUST stay in lock-step with the
//! sibling SDK redactors.

use regex::Regex;
use serde_json::Value;
use std::sync::OnceLock;

/// IPv6 pattern, PHP-parity. Covers full form (8 groups) AND compressed
/// `::` form (1-7 groups + double colon). The Rust `regex` crate has no
/// lookaround, so we emulate the PHP `(?<![0-9a-fA-F:])(...)(?![0-9a-fA-F:])`
/// anchors in `redact_ipv6_with_anchors` by inspecting the bytes
/// adjacent to each match. May over-match on hex-heavy strings that
/// resemble IPv6 — accepted trade-off (over-redact > leak).
fn ipv6_regex() -> &'static Regex {
    static R: OnceLock<Regex> = OnceLock::new();
    R.get_or_init(|| {
        Regex::new(
            r"(?:(?:[0-9a-fA-F]{1,4}:){1,7}(?::[0-9a-fA-F]{1,4})+|(?:[0-9a-fA-F]{1,4}:){7}[0-9a-fA-F]{1,4})",
        )
        .unwrap()
    })
}

/// True when the byte at `s[i]` would extend an IPv6 token (hex digit
/// or colon). Used to emulate PHP's `(?<![0-9a-fA-F:])` / `(?!...)`.
fn is_ipv6_boundary_char(b: u8) -> bool {
    matches!(b, b'0'..=b'9' | b'a'..=b'f' | b'A'..=b'F' | b':')
}

/// Apply the IPv6 regex with the PHP-style boundary anchors emulated
/// in code. Matches whose preceding or trailing byte is a hex digit or
/// `:` are skipped (so adjacent hex tokens don't get half-eaten).
fn redact_ipv6_with_anchors(input: &str) -> String {
    let re = ipv6_regex();
    let bytes = input.as_bytes();
    let mut out = String::with_capacity(input.len());
    let mut cursor = 0usize;
    for m in re.find_iter(input) {
        let (start, end) = (m.start(), m.end());
        let before_ok = start == 0 || !is_ipv6_boundary_char(bytes[start - 1]);
        let after_ok = end == bytes.len() || !is_ipv6_boundary_char(bytes[end]);
        if before_ok && after_ok {
            out.push_str(&input[cursor..start]);
            out.push_str("<redacted:ipv6>");
            cursor = end;
        }
    }
    out.push_str(&input[cursor..]);
    out
}

/// Cache the current user's home directory string. Empty when
/// `dirs::home_dir()` cannot resolve (e.g. detached service account
/// with no HOME env var) — in that case the home-prefix pass is a
/// no-op, which is the safe failure mode.
fn home() -> &'static str {
    static HOME: OnceLock<String> = OnceLock::new();
    HOME.get_or_init(|| {
        dirs::home_dir()
            .map(|p| p.to_string_lossy().to_string())
            .unwrap_or_default()
    })
}

/// Cached (pattern, replacement) table. Built once on first call.
/// Order is significant: token prefixes come AFTER email/IPs so a
/// `sk-foo@bar.com` substring redacts on the more-specific email match
/// first. The cross-language harness pins this order via its expected
/// outputs.
fn patterns() -> &'static [(Regex, &'static str)] {
    static PATTERNS: OnceLock<Vec<(Regex, &'static str)>> = OnceLock::new();
    PATTERNS.get_or_init(|| {
        vec![
            (
                Regex::new(r"\b[A-Za-z0-9._%+-]+@[A-Za-z0-9.-]+\.[A-Za-z]{2,}\b").unwrap(),
                "<redacted:email>",
            ),
            (
                Regex::new(r"\b(?:\d{1,3}\.){3}\d{1,3}\b").unwrap(),
                "<redacted:ipv4>",
            ),
            // NOTE: IPv6 is handled separately in `redact_string` via
            // `redact_ipv6_with_anchors` because the Rust `regex` crate
            // has no lookaround, and the PHP-parity pattern relies on
            // `(?<![0-9a-fA-F:])` / `(?![0-9a-fA-F:])` to anchor.
            (
                Regex::new(r"\bsk-[A-Za-z0-9_-]{8,}\b").unwrap(),
                "<redacted:token>",
            ),
            (
                Regex::new(r"\bgh[pousr]_[A-Za-z0-9_-]{16,}\b").unwrap(),
                "<redacted:token>",
            ),
            (
                Regex::new(r"\bxoxb-[0-9]+-[0-9]+-[A-Za-z0-9]{24,}\b").unwrap(),
                "<redacted:token>",
            ),
        ]
    })
}

/// Redact a single string. Pattern-replace passes run first, then the
/// `$HOME` prefix substitution. Returns an owned `String` even when
/// nothing matched — callers always re-wrap into a `serde_json::Value`,
/// so the small allocation is not on a hot path.
pub fn redact_string(s: &str) -> String {
    let mut out = s.to_string();
    for (pat, repl) in patterns() {
        out = pat.replace_all(&out, *repl).to_string();
    }
    // IPv6 pass runs after the table-driven patterns; see
    // `redact_ipv6_with_anchors` for the PHP-parity contract.
    out = redact_ipv6_with_anchors(&out);
    let h = home();
    if !h.is_empty() {
        out = out.replace(h, "$HOME");
    }
    out
}

/// Recursively redact every string leaf in a `serde_json::Value` tree.
/// Numbers, booleans, and nulls pass through unchanged. Object KEYS
/// are preserved verbatim (per ADR-0038 §7: flag KEYS are not
/// redacted; only VALUES route through redact).
pub fn redact(v: Value) -> Value {
    match v {
        Value::String(s) => Value::String(redact_string(&s)),
        Value::Array(arr) => Value::Array(arr.into_iter().map(redact).collect()),
        Value::Object(m) => {
            let mut out = serde_json::Map::new();
            for (k, val) in m {
                out.insert(k, redact(val));
            }
            Value::Object(out)
        }
        _ => v,
    }
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn redact_email() {
        assert_eq!(
            redact_string("contact me at alice@example.com please"),
            "contact me at <redacted:email> please"
        );
    }

    #[test]
    fn redact_ipv4() {
        assert_eq!(
            redact_string("ping 192.168.1.1 ok"),
            "ping <redacted:ipv4> ok"
        );
    }

    #[test]
    fn redact_sk_token() {
        assert_eq!(
            redact_string("Bearer sk-AbCdEf123456 trailing"),
            "Bearer <redacted:token> trailing"
        );
    }

    #[test]
    fn non_string_passes_through() {
        let v = serde_json::json!({"n": 42, "b": true, "nil": null});
        assert_eq!(redact(v.clone()), v);
    }
}
