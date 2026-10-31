//! CORS, a port of Go's `CORSConfig` validation over tower-http's
//! layer.

use std::time::Duration;

use axum::http::{HeaderName, HeaderValue, Method};
use tower_http::cors::{AllowHeaders, AllowOrigin, Any, CorsLayer, ExposeHeaders};

/// What CORS is configured with.
#[derive(Debug, Clone, Default)]
pub struct CorsConfig {
    /// Allowed origins (`scheme://host[:port]`), or `*` alone.
    pub allow_origins: Vec<String>,
    /// Allowed methods; default GET, POST and HEAD.
    pub allow_methods: Vec<String>,
    /// Allowed request headers; default Origin, Accept, Content-Type
    /// and X-Requested-With.
    pub allow_headers: Vec<String>,
    /// Response headers scripts may read.
    pub expose_headers: Vec<String>,
    /// Whether credentialed requests are allowed.
    pub allow_credentials: bool,
    /// Preflight cache lifetime in seconds; zero omits it.
    pub max_age: i64,
}

fn is_token(s: &str) -> bool {
    !s.is_empty()
        && s.bytes()
            .all(|c| c.is_ascii_alphanumeric() || b"!#$%&'*+-.^_`|~".contains(&c))
}

/// Normalises an origin to `scheme://host[:port]`, lower-cased, with a
/// default port dropped.
pub fn parse_origin(origin: &str) -> Result<String, String> {
    let s = origin.trim();
    let bad = |why: &str| format!("origin {origin:?}: {why}; want scheme://host[:port]");
    if s.contains('*') {
        return Err(bad(
            "a wildcard matches nothing; list each origin, or \"*\" alone for every origin",
        ));
    }
    let (scheme, rest) = s
        .split_once("://")
        .filter(|(sc, r)| !sc.is_empty() && !r.is_empty())
        .ok_or_else(|| bad("scheme and host are required"))?;
    if rest.contains('@') {
        return Err(bad("user information is not part of an origin"));
    }
    if rest.contains(['/', '?', '#']) {
        return Err(bad("path, query and fragment are not part of an origin"));
    }
    let scheme = scheme.to_lowercase();
    let mut host = rest.to_lowercase();
    if (scheme == "https" && host.ends_with(":443")) || (scheme == "http" && host.ends_with(":80"))
    {
        host.truncate(host.rfind(':').unwrap_or(host.len()));
    }
    Ok(format!("{scheme}://{host}"))
}

impl CorsConfig {
    /// Every problem with the configuration, joined; `Ok` when sound.
    pub fn validate(&self) -> Result<(), String> {
        let mut errs: Vec<String> = Vec::new();
        let wildcard = self.allow_origins.iter().any(|o| o.trim() == "*");
        for o in &self.allow_origins {
            if o.trim() != "*" {
                if let Err(e) = parse_origin(o) {
                    errs.push(e);
                }
            }
        }
        if wildcard && self.allow_origins.len() > 1 {
            errs.push("\"*\" grants every origin; list it alone or list origins".into());
        }
        if wildcard && self.allow_credentials {
            errs.push(
                "credentials cannot be allowed with the \"*\" origin (Fetch standard); list the origins instead"
                    .into(),
            );
        }
        for m in &self.allow_methods {
            if !is_token(m.trim()) {
                errs.push(format!("method {m:?} is not an HTTP method token"));
            }
        }
        for h in &self.allow_headers {
            let h = h.trim();
            if h != "*" && !is_token(h) {
                errs.push(format!("header {h:?} is not a header name"));
            }
        }
        for h in &self.expose_headers {
            let h = h.trim();
            if h == "*" && self.allow_credentials {
                errs.push(
                    "a credentialed response exposes no header by \"*\" (Fetch standard); name the headers"
                        .into(),
                );
            } else if h != "*" && !is_token(h) {
                errs.push(format!("header {h:?} is not a header name"));
            }
        }
        if self.max_age < 0 {
            errs.push(format!("max age {} is negative", self.max_age));
        }
        if errs.is_empty() {
            Ok(())
        } else {
            Err(errs.join("\n"))
        }
    }

    /// The layer, or `None` when no origin is allowed (CORS off).
    pub(crate) fn layer(&self) -> Option<CorsLayer> {
        let mut origins: Vec<HeaderValue> = Vec::new();
        let mut any = false;
        for o in &self.allow_origins {
            if o.trim() == "*" {
                any = true;
                break;
            }
            if let Some(v) = parse_origin(o)
                .ok()
                .and_then(|n| HeaderValue::from_str(&n).ok())
            {
                origins.push(v);
            }
        }
        if !any && origins.is_empty() {
            return None;
        }
        let list = |v: &[String]| -> Vec<String> {
            v.iter()
                .map(|s| s.trim().to_string())
                .filter(|s| !s.is_empty())
                .collect()
        };
        let methods: Vec<Method> = {
            let m = list(&self.allow_methods);
            if m.is_empty() {
                vec![Method::GET, Method::POST, Method::HEAD]
            } else {
                m.iter().filter_map(|s| s.parse().ok()).collect()
            }
        };
        let names = |v: Vec<String>| -> Vec<HeaderName> {
            v.iter().filter_map(|s| s.parse().ok()).collect()
        };
        let allow = list(&self.allow_headers);
        let headers = if allow.iter().any(|h| h == "*") {
            AllowHeaders::mirror_request()
        } else if allow.is_empty() {
            AllowHeaders::list(names(
                ["Origin", "Accept", "Content-Type", "X-Requested-With"]
                    .map(String::from)
                    .to_vec(),
            ))
        } else {
            AllowHeaders::list(names(allow))
        };
        let mut layer = CorsLayer::new()
            .allow_methods(methods)
            .allow_headers(headers)
            .allow_credentials(self.allow_credentials);
        layer = if any {
            layer.allow_origin(Any)
        } else {
            layer.allow_origin(AllowOrigin::list(origins))
        };
        let expose = list(&self.expose_headers);
        if expose.iter().any(|h| h == "*") {
            layer = layer.expose_headers(Any);
        } else if !expose.is_empty() {
            layer = layer.expose_headers(ExposeHeaders::list(names(expose)));
        }
        if self.max_age > 0 {
            layer = layer.max_age(Duration::from_secs(self.max_age as u64));
        }
        Some(layer)
    }
}
