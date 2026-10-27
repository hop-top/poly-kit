//! Where a provider key may go (Go `endpoint.go`). A key kit resolved
//! from llm.yaml, a store or the environment reaches an endpoint the URI
//! names only when it is loopback or shares an origin with the
//! provider's default or declared endpoints, llm.yaml `base_url` or
//! `LLM_BASE_URL`. A key the URI carries is the caller's choice for any
//! https host. No key goes over plain http to a remote host. Pinned by
//! `contracts/llm-v1` (`key_endpoint`).

use crate::netpolicy::is_loopback;

use super::config::Sources;
use super::error::LlmError;
use super::keys::KeySourceKind;
use super::registry::Registry;
use super::uri::{is_blank, redact_uri, Uri};

struct Parts {
    scheme: String,
    /// The authority's host and port, as a URL carries it (`[::1]:9`).
    host_port: String,
    host: String,
    port: String,
}

/// Split `raw` as a URL far enough to name its origin. `None` when it
/// has no `scheme://host`.
fn parts(raw: &str) -> Option<Parts> {
    let raw = raw.trim();
    let (scheme, rest) = raw.split_once("://")?;
    let authority = rest.split(['/', '?', '#']).next().unwrap_or_default();
    let host_port = authority.rsplit_once('@').map_or(authority, |(_, h)| h);
    let (host, port) = if let Some(inner) = host_port.strip_prefix('[') {
        let (h, after) = inner.split_once(']')?;
        (h.to_string(), after.strip_prefix(':').unwrap_or_default())
    } else {
        match host_port.rsplit_once(':') {
            Some((h, p)) => (h.to_string(), p),
            None => (host_port.to_string(), ""),
        }
    };
    if host.is_empty() {
        return None;
    }
    let scheme = scheme.to_ascii_lowercase();
    let port = match (port, scheme.as_str()) {
        ("", "https") => "443".to_string(),
        ("", "http") => "80".to_string(),
        (p, _) => p.to_string(),
    };
    Some(Parts {
        scheme,
        host_port: host_port.to_string(),
        host: host.to_ascii_lowercase(),
        port,
    })
}

fn origin(raw: &str) -> Option<String> {
    parts(raw).map(|p| format!("{}://{}:{}", p.scheme, p.host, p.port))
}

fn loopback(raw: &str) -> bool {
    parts(raw).is_some_and(|p| is_loopback(&p.host_port))
}

/// Refuse a non-blank `key` over plain http to a host that is not
/// loopback. Adapters call it on the base URL they settle on;
/// [`Registry::resolve`] and [`Registry::load_config`] already do.
pub fn check_key_endpoint(scheme: &str, key: &str, base_url: &str) -> Result<(), LlmError> {
    if is_blank(key) || base_url.is_empty() {
        return Ok(());
    }
    match parts(base_url) {
        Some(p) if p.scheme == "http" && !is_loopback(&p.host_port) => Err(LlmError::KeyEndpoint {
            scheme: scheme.to_string(),
            endpoint: redact_uri(base_url),
            insecure: true,
        }),
        _ => Ok(()),
    }
}

/// Whether credentials kit resolved itself (a stored or env key, a
/// token, a workspace header) may go to `base`: it is loopback, or shares
/// an origin with one of `trusted`. Every credential path decides here.
pub fn credentials_trusted(base: &str, trusted: &[String]) -> bool {
    if loopback(base) {
        return true;
    }
    let Some(want) = origin(base) else {
        return false;
    };
    trusted
        .iter()
        .any(|t| origin(t).as_deref() == Some(want.as_str()))
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
        if kind == KeySourceKind::Uri
            || credentials_trusted(&endpoint, &self.trusted_endpoints(src, &u.scheme))
        {
            return Ok(());
        }
        Err(LlmError::KeyEndpoint {
            scheme: u.scheme.clone(),
            endpoint: redact_uri(&endpoint),
            insecure: false,
        })
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

    #[test]
    fn origins_normalise_case_and_default_ports() {
        assert_eq!(
            origin("https://API.example.com/v1").as_deref(),
            Some("https://api.example.com:443")
        );
        assert_eq!(
            origin("HTTP://u:p@[::1]:9/x?y").as_deref(),
            Some("http://::1:9")
        );
        assert_eq!(origin("example.com"), None);
        assert!(loopback("http://localhost:1234"));
        assert!(!loopback("http://api.example.com"));
    }
}
