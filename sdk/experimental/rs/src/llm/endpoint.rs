//! Where a provider credential may go (Go `endpoint.go`). A key kit
//! resolved from llm.yaml, a store or the environment reaches an endpoint
//! the URI names only when it is loopback or shares an origin with the
//! provider's default or declared endpoints, llm.yaml `base_url` or
//! `LLM_BASE_URL`. A key the URI carries is the caller's choice for any
//! https host. No credential goes over plain http to a remote host, or to
//! a malformed endpoint. Pinned by `contracts/llm-v1` (`key_endpoint`,
//! `credential_headers`).
//!
//! Endpoints parse with `url::Url`, the parser reqwest sends with; an
//! adapter checks the very `Url` it hands the client. Text that parsers
//! read differently is refused before parsing: userinfo, backslashes,
//! percent-escapes, whitespace and control characters.

use std::net::IpAddr;

use url::{Host, Url};

use super::config::Sources;
use super::error::{KeyEndpointReason, LlmError};
use super::keys::KeySourceKind;
use super::registry::Registry;
use super::uri::{is_blank, redact_uri, Uri};

/// Parse `raw` for a credential check; `None` (no credential may go
/// there) when it does not parse, has no host, or carries userinfo, a
/// backslash, a percent-escape, whitespace or a control character.
pub fn parse_endpoint(raw: &str) -> Option<Url> {
    if raw
        .chars()
        .any(|c| c == '\\' || c == '%' || c.is_whitespace() || c.is_control())
    {
        return None;
    }
    let u = Url::parse(raw).ok()?;
    acceptable(&u).then_some(u)
}

fn acceptable(u: &Url) -> bool {
    u.username().is_empty() && u.password().is_none() && u.host().is_some()
}

/// `localhost` exactly, 127.0.0.0/8, `::1` or a v4-mapped loopback.
pub fn is_loopback_url(u: &Url) -> bool {
    match u.host() {
        Some(Host::Domain(d)) => d == "localhost",
        Some(Host::Ipv4(ip)) => ip.is_loopback(),
        Some(Host::Ipv6(ip)) => {
            ip.is_loopback()
                || ip
                    .to_ipv4_mapped()
                    .is_some_and(|v4| IpAddr::V4(v4).is_loopback())
        }
        None => false,
    }
}

fn same_origin(a: &Url, b: &Url) -> bool {
    a.scheme() == b.scheme()
        && a.host() == b.host()
        && a.port_or_known_default() == b.port_or_known_default()
}

fn refusal(scheme: &str, raw: &str, reason: KeyEndpointReason) -> LlmError {
    LlmError::KeyEndpoint {
        scheme: scheme.to_string(),
        endpoint: redact_uri(raw),
        reason,
    }
}

/// Refuse a non-blank `key` for `url` (parsed from `raw`, which names it
/// in the error) when the URL is malformed for credentials or is plain
/// http to a host that is not loopback.
pub fn check_endpoint_url(scheme: &str, key: &str, raw: &str, url: &Url) -> Result<(), LlmError> {
    if is_blank(key) {
        return Ok(());
    }
    if parse_endpoint(raw).is_none() || !acceptable(url) {
        return Err(refusal(scheme, raw, KeyEndpointReason::Malformed));
    }
    if url.scheme() == "http" && !is_loopback_url(url) {
        return Err(refusal(scheme, raw, KeyEndpointReason::Insecure));
    }
    Ok(())
}

/// [`check_endpoint_url`] for a base URL not yet parsed.
/// [`Registry::resolve`] and [`Registry::load_config`] call it.
pub fn check_key_endpoint(scheme: &str, key: &str, base_url: &str) -> Result<(), LlmError> {
    if is_blank(key) || base_url.is_empty() {
        return Ok(());
    }
    match parse_endpoint(base_url) {
        Some(u) => check_endpoint_url(scheme, key, base_url, &u),
        None => Err(refusal(scheme, base_url, KeyEndpointReason::Malformed)),
    }
}

/// Whether credentials kit resolved itself (a stored or env key, a
/// token, a workspace header) may go to `url`: it is loopback, or shares
/// an origin with one of `trusted`. Every credential path decides here.
pub fn credentials_trusted(url: &Url, trusted: &[String]) -> bool {
    if !acceptable(url) {
        return false;
    }
    is_loopback_url(url)
        || trusted
            .iter()
            .filter_map(|t| parse_endpoint(t))
            .any(|t| same_origin(&t, url))
}

/// The endpoint a URI itself names: `base_url`, else its host.
fn uri_endpoint(u: &Uri) -> Option<String> {
    match u.params.get("base_url").filter(|v| !v.is_empty()) {
        Some(b) => Some(b.clone()),
        None if !u.host.is_empty() => Some(format!("http://{}", u.host)),
        None => None,
    }
}

impl Registry {
    /// Whether a key from `kind` may go where `u` points.
    pub(crate) fn guard_key_endpoint(
        &self,
        src: &Sources<'_>,
        u: &Uri,
        kind: KeySourceKind,
        key: &str,
    ) -> Result<(), LlmError> {
        let Some(endpoint) = uri_endpoint(u) else {
            return Ok(());
        };
        if is_blank(key) {
            return Ok(());
        }
        check_key_endpoint(&u.scheme, key, &endpoint)?;
        let url = parse_endpoint(&endpoint).expect("checked above");
        if kind == KeySourceKind::Uri
            || credentials_trusted(&url, &self.trusted_endpoints(src, &u.scheme))
        {
            return Ok(());
        }
        Err(refusal(&u.scheme, &endpoint, KeyEndpointReason::Untrusted))
    }

    fn trusted_endpoints(&self, src: &Sources<'_>, scheme: &str) -> Vec<String> {
        let mut out = Vec::new();
        if let Some(d) = self.declaration(scheme) {
            if !d.base_url.is_empty() {
                out.push(d.base_url);
            }
            out.extend(d.endpoints);
        }
        if let Some((_, b)) = src.file.block(&[scheme]) {
            if !b.base_url.is_empty() {
                out.push(b.base_url.clone());
            }
        }
        out.extend(src.env("LLM_BASE_URL"));
        out
    }
}

#[cfg(test)]
mod tests {
    use super::*;

    fn trusted(raw: &str) -> bool {
        let t = ["https://api.anthropic.com".to_string()];
        parse_endpoint(raw).is_some_and(|u| credentials_trusted(&u, &t))
    }

    #[test]
    fn adversarial_endpoints() {
        for bad in [
            "https://evil.example\\@api.anthropic.com/",
            "https://api.anthropic.com@evil.example",
            "https://user:pw@api.anthropic.com",
            "https://api.anthropic.com.",
            "https://api%2Eanthropic.com",
            "https://api.anthropic.com:8443",
            "https://api.anthropic.com\t.evil.example",
            "http://localhost.evil.example",
            "https://api.anthropic.com.evil.example",
        ] {
            assert!(!trusted(bad), "{bad}");
        }
        for good in [
            "https://api.anthropic.com",
            "HTTPS://Api.Anthropic.COM/v1",
            "https://api.anthropic.com:443",
            "http://[::1]:8080",
            "http://127.10.0.1:8080",
            "http://localhost:1234",
        ] {
            assert!(trusted(good), "{good}");
        }
    }

    #[test]
    fn insecure_and_malformed_refusals() {
        let reason = |raw: &str| match check_key_endpoint("s", "k", raw) {
            Err(LlmError::KeyEndpoint { reason, .. }) => Some(reason),
            _ => None,
        };
        assert_eq!(
            reason("http://gw.example"),
            Some(KeyEndpointReason::Insecure)
        );
        assert_eq!(
            reason("https://u@gw.example"),
            Some(KeyEndpointReason::Malformed)
        );
        assert_eq!(reason("http://[::1]:9"), None);
        assert_eq!(reason("https://gw.example"), None);
    }
}
