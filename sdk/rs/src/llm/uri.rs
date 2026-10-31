//! Provider URIs (`scheme://[host:port/]model[?params]`) and their
//! credential masking. Ports Go `ParseURI` and `RedactURI` text for text;
//! pinned by `contracts/llm-v1`.

use std::collections::BTreeMap;
use std::fmt;

use super::error::LlmError;

const REDACTED: &str = "REDACTED";

/// A parsed provider URI. `Debug` masks credential params.
#[derive(Clone, PartialEq, Eq, Default)]
pub struct Uri {
    pub scheme: String,
    pub model: String,
    /// `host:port` when the URI names one (local providers).
    pub host: String,
    /// Query params, not unescaped. Empty when the URI has none.
    pub params: BTreeMap<String, String>,
}

impl Uri {
    /// The URI's own `api_key` param, when it is not blank.
    pub(crate) fn key(&self) -> Option<&str> {
        self.params
            .get("api_key")
            .map(String::as_str)
            .filter(|v| !is_blank(v))
    }

    /// Drops a blank `api_key` param: a blank key is no key.
    pub(crate) fn drop_blank_key(&mut self) {
        if self.params.get("api_key").is_some_and(|v| is_blank(v)) {
            self.params.remove("api_key");
        }
    }
}

impl fmt::Debug for Uri {
    fn fmt(&self, f: &mut fmt::Formatter<'_>) -> fmt::Result {
        let params: BTreeMap<&str, &str> = self
            .params
            .iter()
            .map(|(k, v)| {
                let v = if credential_param(k) && !v.is_empty() {
                    REDACTED
                } else {
                    v.as_str()
                };
                (k.as_str(), v)
            })
            .collect();
        f.debug_struct("Uri")
            .field("scheme", &self.scheme)
            .field("model", &self.model)
            .field("host", &self.host)
            .field("params", &params)
            .finish()
    }
}

/// A key value that is no key: empty or only whitespace.
pub(crate) fn is_blank(v: &str) -> bool {
    v.trim().is_empty()
}

/// Parse a provider URI. Errors quote it with credentials masked by
/// [`redact_uri`].
pub fn parse_uri(raw: &str) -> Result<Uri, LlmError> {
    if raw.is_empty() {
        return Err(LlmError::invalid("empty URI"));
    }
    let Some(idx) = raw.find("://") else {
        return Err(LlmError::invalid(format!(
            "missing scheme in URI {:?}",
            redact_uri(raw)
        )));
    };
    let scheme = &raw[..idx];
    if scheme.is_empty() {
        return Err(LlmError::invalid(format!(
            "empty scheme in URI {:?}",
            redact_uri(raw)
        )));
    }
    let mut rest = &raw[idx + 3..];
    let mut params = BTreeMap::new();
    if let Some(q) = rest.find('?') {
        params = parse_query(&rest[q + 1..]);
        rest = &rest[..q];
    }
    let (mut host, mut model) = (String::new(), rest.to_string());
    if let Some(slash) = rest.find('/').filter(|&s| s > 0) {
        if let Some((h, p)) = split_host_port(&rest[..slash]).filter(|(_, p)| !p.is_empty()) {
            host = join_host_port(h, p);
            model = rest[slash + 1..].to_string();
        }
    }
    Ok(Uri {
        scheme: scheme.to_string(),
        model,
        host,
        params,
    })
}

fn parse_query(q: &str) -> BTreeMap<String, String> {
    let mut m = BTreeMap::new();
    if q.is_empty() {
        return m;
    }
    for pair in q.split('&') {
        let (k, v) = pair.split_once('=').unwrap_or((pair, ""));
        if !k.is_empty() {
            m.insert(k.to_string(), v.to_string());
        }
    }
    m
}

/// Go `net.SplitHostPort`: `None` where Go returns an error.
fn split_host_port(hp: &str) -> Option<(&str, &str)> {
    let i = hp.rfind(':')?;
    let (host, j, k) = if hp.starts_with('[') {
        let end = hp.find(']')?;
        if end + 1 != i {
            return None; // missing port, or too many colons
        }
        (&hp[1..end], 1, end + 1)
    } else {
        let host = &hp[..i];
        if host.contains(':') {
            return None;
        }
        (host, 0, 0)
    };
    if hp[j..].contains('[') || hp[k..].contains(']') {
        return None;
    }
    Some((host, &hp[i + 1..]))
}

fn join_host_port(h: &str, p: &str) -> String {
    if h.contains(':') {
        format!("[{h}]:{p}")
    } else {
        format!("{h}:{p}")
    }
}

/// `raw` with credentials replaced by `REDACTED`, for errors and logs.
///
/// Masks credential params (`key`, `token`, `secret`, `password`,
/// `auth`, `signature`, names ending `_key`, `_token`, `apikey` and the
/// like, case-insensitive, `-` read as `_`) and URL userinfo, in `raw`
/// or in a param value such as `base_url`. `raw` need not parse. A model
/// id `name@version` is kept. Go `RedactURI`.
pub fn redact_uri(raw: &str) -> String {
    let mut out = String::with_capacity(raw.len());
    let mut rest = raw;
    while let Some(i) = rest.find(['?', '&']) {
        out.push_str(&redact_segment(&rest[..i]));
        out.push_str(&rest[i..i + 1]);
        rest = &rest[i + 1..];
    }
    out.push_str(&redact_segment(rest));
    out
}

fn redact_segment(seg: &str) -> String {
    match seg.split_once('=') {
        Some((k, v)) if !k.contains("://") => {
            if credential_param(k) {
                if v.is_empty() {
                    seg.to_string()
                } else {
                    format!("{k}={REDACTED}")
                }
            } else {
                format!("{k}={}", redact_userinfo(v, true))
            }
        }
        _ => redact_userinfo(seg, false),
    }
}

fn redact_userinfo(s: &str, strict: bool) -> String {
    let (start, scheme) = match s.find("://") {
        Some(i) => (i + 3, s[..i].to_ascii_lowercase()),
        None => (0, String::new()),
    };
    let rest = &s[start..];
    let (end, has_path) = match rest.find('/') {
        Some(e) => (e, true),
        None => (rest.len(), false),
    };
    let Some(at) = rest[..end].rfind('@').filter(|&a| a > 0) else {
        return s.to_string();
    };
    let credential = strict
        || scheme == "http"
        || scheme == "https"
        || (has_path && start > 0)
        || rest[..at].contains(':');
    if !credential {
        return s.to_string();
    }
    format!("{}{REDACTED}{}", &s[..start], &rest[at..])
}

/// Whether a param named `name` carries a credential.
pub(crate) fn credential_param(name: &str) -> bool {
    let name = query_unescape(name).unwrap_or_else(|| name.to_string());
    let name = name.trim().to_ascii_lowercase().replace('-', "_");
    const EXACT: &[&str] = &[
        "key",
        "apikey",
        "token",
        "secret",
        "password",
        "passwd",
        "pwd",
        "auth",
        "authorization",
        "signature",
        "sig",
        "credential",
        "credentials",
    ];
    const SUFFIX: &[&str] = &[
        "_key",
        "apikey",
        "_token",
        "_secret",
        "_password",
        "_signature",
        "_credential",
        "_credentials",
    ];
    EXACT.contains(&name.as_str()) || SUFFIX.iter().any(|s| name.ends_with(s))
}

/// Go `url.QueryUnescape`: `+` is a space; `None` on a bad escape.
fn query_unescape(s: &str) -> Option<String> {
    let b = s.as_bytes();
    let mut out = Vec::with_capacity(b.len());
    let mut i = 0;
    while i < b.len() {
        match b[i] {
            b'%' => {
                let hex = s.get(i + 1..i + 3)?;
                out.push(u8::from_str_radix(hex, 16).ok()?);
                i += 3;
            }
            b'+' => {
                out.push(b' ');
                i += 1;
            }
            c => {
                out.push(c);
                i += 1;
            }
        }
    }
    String::from_utf8(out).ok()
}

/// `raw` without any `name` param, splitting its query as [`parse_uri`]
/// does; the `?` goes too when nothing is left.
pub(crate) fn strip_param(raw: &str, name: &str) -> String {
    let Some(scheme) = raw.find("://") else {
        return raw.to_string();
    };
    let Some(q) = raw[scheme + 3..].find('?').map(|q| q + scheme + 3) else {
        return raw.to_string();
    };
    let kept: Vec<&str> = raw[q + 1..]
        .split('&')
        .filter(|pair| pair.split_once('=').map_or(*pair, |(k, _)| k) != name)
        .collect();
    if kept.is_empty() {
        raw[..q].to_string()
    } else {
        format!("{}?{}", &raw[..q], kept.join("&"))
    }
}

/// The scheme of `uri`, or all of it when it has no `://`.
pub(crate) fn scheme_of(uri: &str) -> &str {
    match uri.find("://") {
        Some(i) if i > 0 => &uri[..i],
        _ => uri,
    }
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn strip_param_keeps_the_rest() {
        assert_eq!(strip_param("a://m?api_key=&x=1", "api_key"), "a://m?x=1");
        assert_eq!(strip_param("a://m?api_key= ", "api_key"), "a://m");
        assert_eq!(strip_param("a://m", "api_key"), "a://m");
    }

    #[test]
    fn debug_masks_credential_params() {
        let u = parse_uri("a://m?api_key=s3cret&t=1").unwrap();
        let shown = format!("{u:?}");
        assert!(!shown.contains("s3cret"), "{shown}");
        assert!(shown.contains("REDACTED"));
    }
}
