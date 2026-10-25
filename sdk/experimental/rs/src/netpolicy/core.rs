//! The policy decision itself: the marker, loopback classification, the
//! dial check and the typed refusal. Std only, so it builds wherever the
//! crate does, wasm32 included.

use std::fmt;
use std::net::IpAddr;

/// The refusal text, the analogue of Go's `ErrOffline`. Every
/// [`OfflineError`] ends with it.
pub const OFFLINE_MESSAGE: &str = "network disabled by --offline";

/// The network policy marker. Threaded at client construction; the
/// analogue of the Go side's context value.
///
/// Default is online, so an unwired port keeps its current behaviour.
#[derive(Debug, Clone, Copy, Default, PartialEq, Eq)]
pub struct NetPolicy {
    offline: bool,
}

impl NetPolicy {
    /// Build a policy from a boolean, mirroring Go's
    /// `WithOffline(ctx, bool)`. This is the one-liner the CLI layer
    /// calls once it registers the `--offline` flag.
    pub fn new(offline: bool) -> Self {
        Self { offline }
    }

    /// A policy that permits network access.
    pub fn online() -> Self {
        Self { offline: false }
    }

    /// A policy that refuses non-loopback network access.
    pub fn offline() -> Self {
        Self { offline: true }
    }

    /// Whether this policy refuses non-loopback network access.
    pub fn is_offline(&self) -> bool {
        self.offline
    }

    /// Whether traffic to `host` (with or without a port) is permitted
    /// under this policy. Loopback is always permitted.
    pub fn allows_host(&self, host: &str) -> bool {
        !self.offline || is_loopback(host)
    }

    /// Whether a dial to `addr` on `network` is permitted, the analogue
    /// of Go's `CheckDial`. The seam for every client that opens its
    /// own socket (websocket, SMTP, SQL drivers, raw TLS): call it
    /// before dialing.
    ///
    /// Same exemptions as the HTTP guard: an online policy permits
    /// everything, loopback stays reachable, and unix sockets are not
    /// network at all. A refusal reads
    /// `dial <network> <addr>: network disabled by --offline`.
    pub fn check_dial(&self, network: &str, addr: &str) -> Result<(), OfflineError> {
        if !self.offline || is_local_network(network) || is_loopback(addr) {
            return Ok(());
        }
        Err(OfflineError {
            method: format!("dial {network}"),
            url: addr.to_string(),
        })
    }
}

/// The typed refusal, the analogue of Go's `ErrOffline`. Callers match
/// it by type, or through `NetError::as_offline` on the reqwest client.
///
/// Displays as `<method> <url>: network disabled by --offline`, the text
/// Go wraps its sentinel in.
#[derive(Debug, Clone, PartialEq, Eq)]
pub struct OfflineError {
    /// The refused operation: an HTTP method for a request, or
    /// `dial <network>` for a dial.
    pub method: String,
    /// Where it was going. For a request: scheme, host (with port) and
    /// path only; query, fragment and userinfo never appear, as they may
    /// carry credentials. For a dial: the address as given.
    pub url: String,
}

impl fmt::Display for OfflineError {
    fn fmt(&self, f: &mut fmt::Formatter<'_>) -> fmt::Result {
        write!(f, "{} {}: {OFFLINE_MESSAGE}", self.method, self.url)
    }
}

impl std::error::Error for OfflineError {}

/// Whether `host` names a loopback address. `host` may carry a port
/// (`127.0.0.1:8080`, `[::1]:9000`), as a URL authority or a dial
/// address does.
///
/// Hosts that are not literal IPs (DNS names) are treated as remote:
/// resolving them would itself be network access. Matches Go's
/// `isLoopback`: the exact name `localhost`, the whole 127.0.0.0/8
/// range, `::1` and IPv4-mapped loopback.
///
/// One deliberate difference: a bracketed IPv6 literal without a port
/// (`[::1]`, the host of `http://[::1]/`) is loopback here. Go misses
/// it and refuses the request.
pub fn is_loopback(host: &str) -> bool {
    let h = split_host(host).unwrap_or(host);
    if h == "localhost" {
        return true;
    }
    let bare = h
        .strip_prefix('[')
        .and_then(|h| h.strip_suffix(']'))
        .unwrap_or(h);
    bare.parse::<IpAddr>()
        .is_ok_and(|ip| ip.to_canonical().is_loopback())
}

/// The host half of `hostport`, or `None` where Go's `net.SplitHostPort`
/// would fail: no port, a bare IPv6 literal, or stray brackets.
fn split_host(hostport: &str) -> Option<&str> {
    let i = hostport.rfind(':')?;
    let (host, j, k) = if hostport.starts_with('[') {
        let end = hostport.find(']')?;
        if end + 1 != i {
            return None;
        }
        (&hostport[1..end], 1, end + 1)
    } else {
        let host = &hostport[..i];
        if host.contains(':') {
            return None;
        }
        (host, 0, 0)
    };
    if hostport[j..].contains('[') || hostport[k..].contains(']') {
        return None;
    }
    Some(host)
}

/// Whether `network` names a transport that cannot leave the machine.
/// Unix sockets are filesystem objects: `--offline` means "do not talk
/// to the network", and a unix socket is not the network.
fn is_local_network(network: &str) -> bool {
    matches!(network, "unix" | "unixgram" | "unixpacket")
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn split_host_follows_go() {
        let cases = [
            ("127.0.0.1:8080", Some("127.0.0.1")),
            ("[::1]:9000", Some("::1")),
            ("[localhost]:80", Some("localhost")),
            ("127.0.0.1:", Some("127.0.0.1")),
            (":80", Some("")),
            ("localhost", None),
            ("::1", None),
            ("[::1]", None),
            ("[[::1]]:80", None),
            ("[::1]]:80", None),
            ("a[b:80", None),
        ];
        for (raw, want) in cases {
            assert_eq!(split_host(raw), want, "{raw}");
        }
    }

    #[test]
    fn offline_error_message_names_the_flag() {
        let e = OfflineError {
            method: "GET".into(),
            url: "https://example.com/x".into(),
        };
        assert_eq!(
            e.to_string(),
            "GET https://example.com/x: network disabled by --offline"
        );
    }
}
