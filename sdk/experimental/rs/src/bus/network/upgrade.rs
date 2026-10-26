//! The HTTP side of accepting a peer, independent of any framework:
//! coder/websocket's `Accept` checks, in its order and with its texts.

use tokio_tungstenite::tungstenite::handshake::derive_accept_key;
use tokio_tungstenite::tungstenite::http::{
    header, HeaderMap, Method, Request, Response, StatusCode, Version,
};

/// An upgrade request that passed every check.
#[derive(Clone, Debug, PartialEq, Eq)]
pub struct Upgrade {
    accept: String,
}

impl Upgrade {
    /// The `101 Switching Protocols` response to send before handing the
    /// connection to [`NetworkAdapter::accept_upgraded`].
    ///
    /// [`NetworkAdapter::accept_upgraded`]: super::NetworkAdapter::accept_upgraded
    pub fn response(&self) -> Response<()> {
        let mut r = Response::new(());
        *r.status_mut() = StatusCode::SWITCHING_PROTOCOLS;
        let h = r.headers_mut();
        h.insert(header::UPGRADE, "websocket".parse().expect("static header"));
        h.insert(
            header::CONNECTION,
            "Upgrade".parse().expect("static header"),
        );
        h.insert(
            header::SEC_WEBSOCKET_ACCEPT,
            self.accept.parse().expect("base64 is a valid header value"),
        );
        r
    }
}

/// A refused upgrade: the status and message coder/websocket answers
/// with, plus the headers it adds.
#[derive(Clone, Debug, PartialEq, Eq)]
pub struct Rejection {
    /// The HTTP status.
    pub status: StatusCode,
    /// Why, in coder/websocket's words.
    pub message: String,
    headers: Vec<(header::HeaderName, &'static str)>,
}

impl Rejection {
    fn new(status: StatusCode, message: String) -> Self {
        Rejection {
            status,
            message,
            headers: Vec::new(),
        }
    }

    fn with(mut self, name: header::HeaderName, value: &'static str) -> Self {
        self.headers.push((name, value));
        self
    }

    /// The response Go's `http.Error` writes: the message and a newline
    /// as plain text.
    pub fn response(&self) -> Response<String> {
        let mut r = Response::new(format!("{}\n", self.message));
        *r.status_mut() = self.status;
        let h = r.headers_mut();
        for (name, value) in &self.headers {
            h.insert(name, value.parse().expect("static header"));
        }
        h.insert(
            header::CONTENT_TYPE,
            "text/plain; charset=utf-8".parse().expect("static header"),
        );
        h.insert(
            header::X_CONTENT_TYPE_OPTIONS,
            "nosniff".parse().expect("static header"),
        );
        r
    }
}

/// Checks an upgrade request as coder/websocket's `Accept` does with no
/// options: HTTP/1.1 or later, `Connection: Upgrade`, `Upgrade:
/// websocket`, `GET`, version 13 and one 16-byte key, then the Origin
/// check. An `Origin` header, when present, must name the request's
/// `Host` (case-insensitively); browsers on other sites are refused with
/// 403. Non-browser peers send no `Origin`.
pub fn check_upgrade<B>(req: &Request<B>) -> Result<Upgrade, Rejection> {
    verify_client_request(req.method(), req.version(), req.headers())?;
    authenticate_origin(req.headers())?;
    let key = header_str(req.headers(), header::SEC_WEBSOCKET_KEY);
    Ok(Upgrade {
        accept: derive_accept_key(key.trim().as_bytes()),
    })
}

fn verify_client_request(m: &Method, v: Version, h: &HeaderMap) -> Result<(), Rejection> {
    if v < Version::HTTP_11 {
        return Err(Rejection::new(
            StatusCode::UPGRADE_REQUIRED,
            format!(
                "WebSocket protocol violation: handshake request must be at least HTTP/1.1: {:?}",
                proto(v)
            ),
        ));
    }
    if !contains_token(h, header::CONNECTION, "upgrade") {
        return Err(Rejection::new(
            StatusCode::UPGRADE_REQUIRED,
            format!(
                "WebSocket protocol violation: Connection header {:?} does not contain Upgrade",
                header_str(h, header::CONNECTION)
            ),
        )
        .with(header::CONNECTION, "Upgrade")
        .with(header::UPGRADE, "websocket"));
    }
    if !contains_token(h, header::UPGRADE, "websocket") {
        return Err(Rejection::new(
            StatusCode::UPGRADE_REQUIRED,
            format!(
                "WebSocket protocol violation: Upgrade header {:?} does not contain websocket",
                header_str(h, header::UPGRADE)
            ),
        )
        .with(header::CONNECTION, "Upgrade")
        .with(header::UPGRADE, "websocket"));
    }
    if m != Method::GET {
        return Err(Rejection::new(
            StatusCode::METHOD_NOT_ALLOWED,
            format!(
                "WebSocket protocol violation: handshake request method is not GET but {:?}",
                m.as_str()
            ),
        ));
    }
    let version = header_str(h, header::SEC_WEBSOCKET_VERSION);
    if version != "13" {
        return Err(Rejection::new(
            StatusCode::BAD_REQUEST,
            format!("unsupported WebSocket protocol version (only 13 is supported): {version:?}"),
        )
        .with(header::SEC_WEBSOCKET_VERSION, "13"));
    }
    let keys: Vec<_> = h.get_all(header::SEC_WEBSOCKET_KEY).iter().collect();
    match keys.len() {
        0 => {
            return Err(Rejection::new(
                StatusCode::BAD_REQUEST,
                "WebSocket protocol violation: missing Sec-WebSocket-Key".into(),
            ))
        }
        1 => {}
        _ => {
            return Err(Rejection::new(
                StatusCode::BAD_REQUEST,
                "WebSocket protocol violation: multiple Sec-WebSocket-Key headers".into(),
            ))
        }
    }
    let key = String::from_utf8_lossy(keys[0].as_bytes());
    let key = key.trim();
    if !is_16_byte_base64(key) {
        return Err(Rejection::new(
            StatusCode::BAD_REQUEST,
            format!(
                "WebSocket protocol violation: invalid Sec-WebSocket-Key {key:?}, must be a 16 byte base64 encoded string"
            ),
        ));
    }
    Ok(())
}

/// coder/websocket's `authenticateOrigin` without origin patterns.
fn authenticate_origin(h: &HeaderMap) -> Result<(), Rejection> {
    let origin = header_str(h, header::ORIGIN);
    if origin.is_empty() {
        return Ok(());
    }
    let forbidden = |m: String| Rejection::new(StatusCode::FORBIDDEN, m);
    let host = origin_host(&origin)
        .ok_or_else(|| forbidden(format!("failed to parse Origin header {origin:?}")))?;
    let req_host = header_str(h, header::HOST);
    if req_host.eq_ignore_ascii_case(&host) {
        return Ok(());
    }
    if host.is_empty() {
        return Err(forbidden(format!(
            "request Origin {origin:?} is not a valid URL with a host"
        )));
    }
    Err(forbidden(format!(
        "request Origin {host:?} is not authorized for Host {req_host:?}"
    )))
}

/// The host (with port) of an Origin value, as Go's `url.Parse` reads
/// it: empty without a `scheme://` authority; `None` when unparseable
/// (control characters, a bad scheme or a non-numeric port).
fn origin_host(origin: &str) -> Option<String> {
    if origin.bytes().any(|b| b < 0x20 || b == 0x7f) {
        return None;
    }
    let Some((_, rest)) = split_scheme(origin)? else {
        // No scheme: a path, which may not hold a colon in its first
        // segment.
        let colon = origin.find(':');
        let slash = origin.find('/');
        if colon.is_some_and(|c| slash.is_none_or(|s| c < s)) {
            return None;
        }
        return Some(String::new());
    };
    let Some(rest) = rest.strip_prefix("//") else {
        return Some(String::new());
    };
    let end = rest.find(['/', '?', '#']).unwrap_or(rest.len());
    let authority = &rest[..end];
    let host = authority.rsplit_once('@').map_or(authority, |(_, h)| h);
    let port = if host.starts_with('[') {
        let close = host.find(']')?;
        &host[close + 1..]
    } else {
        host.rfind(':').map_or("", |i| &host[i..])
    };
    if let Some(digits) = port.strip_prefix(':') {
        if !digits.bytes().all(|b| b.is_ascii_digit()) {
            return None;
        }
    } else if !port.is_empty() {
        return None;
    }
    Some(host.to_string())
}

/// Go's `getScheme`: `Ok(None)` when there is no scheme, `None` for a
/// leading colon.
fn split_scheme(s: &str) -> Option<Option<(&str, &str)>> {
    for (i, c) in s.char_indices() {
        match c {
            'a'..='z' | 'A'..='Z' => {}
            '0'..='9' | '+' | '-' | '.' if i == 0 => return Some(None),
            '0'..='9' | '+' | '-' | '.' => {}
            ':' if i == 0 => return None,
            ':' => return Some(Some((&s[..i], &s[i + 1..]))),
            _ => return Some(None),
        }
    }
    Some(None)
}

fn proto(v: Version) -> &'static str {
    match v {
        Version::HTTP_09 => "HTTP/0.9",
        Version::HTTP_10 => "HTTP/1.0",
        Version::HTTP_11 => "HTTP/1.1",
        Version::HTTP_2 => "HTTP/2.0",
        _ => "HTTP/3.0",
    }
}

fn header_str(h: &HeaderMap, name: header::HeaderName) -> String {
    h.get(name)
        .map(|v| String::from_utf8_lossy(v.as_bytes()).into_owned())
        .unwrap_or_default()
}

/// Whether any `name` header holds `token` in its comma-separated list,
/// ignoring case.
fn contains_token(h: &HeaderMap, name: header::HeaderName, token: &str) -> bool {
    h.get_all(name).iter().any(|v| {
        String::from_utf8_lossy(v.as_bytes())
            .split(',')
            .any(|t| t.trim().eq_ignore_ascii_case(token))
    })
}

/// Whether `key` is standard base64 for exactly 16 bytes: 22 alphabet
/// characters and `==`. Go's decoder does not check the padding bits.
fn is_16_byte_base64(key: &str) -> bool {
    let b = key.as_bytes();
    b.len() == 24
        && b[22..] == *b"=="
        && b[..22]
            .iter()
            .all(|c| c.is_ascii_alphanumeric() || *c == b'+' || *c == b'/')
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn origin_host_reads_like_go_url_parse() {
        let cases: &[(&str, Option<&str>)] = &[
            ("http://example.com", Some("example.com")),
            (
                "https://Example.com:8443/path?q#f",
                Some("Example.com:8443"),
            ),
            ("http://user:pw@host:1", Some("host:1")),
            ("http://[::1]:9000", Some("[::1]:9000")),
            ("null", Some("")),
            ("file://", Some("")),
            ("mailto:x@y", Some("")),
            ("http://host:abc", None),
            ("http://host\n", None),
            (":nope", None),
            ("1http://x", None),
            ("x/y:z", Some("")),
            ("a b:c", None),
        ];
        for (origin, want) in cases {
            assert_eq!(origin_host(origin).as_deref(), *want, "{origin}");
        }
    }

    #[test]
    fn sixteen_byte_keys_only() {
        assert!(is_16_byte_base64("dGhlIHNhbXBsZSBub25jZQ=="));
        assert!(!is_16_byte_base64("dGhlIHNhbXBsZSBub25jZQ="));
        assert!(!is_16_byte_base64("dGhlIHNhbXBsZSBub25jZQ!="));
        assert!(!is_16_byte_base64(""));
    }
}
