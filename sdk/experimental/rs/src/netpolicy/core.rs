//! The policy decision itself: the marker, loopback classification, the
//! dial check and the typed refusal. Std only, so it builds wherever the
//! crate does, wasm32 included.

use std::fmt;
use std::net::{IpAddr, Ipv6Addr};
use std::sync::Arc;

/// The refusal text, the analogue of Go's `ErrOffline`. Every
/// [`OfflineError`] ends with it.
pub const OFFLINE_MESSAGE: &str = "network disabled by --offline";

/// The allowlist refusal text, the analogue of Go's `ErrHostNotAllowed`.
/// Every [`HostNotAllowedError`] ends with it.
pub const HOST_NOT_ALLOWED_MESSAGE: &str = "host not allowed by netpolicy.allow_hosts";

type AuditFn = dyn Fn(&Refusal) + Send + Sync;

/// The network policy marker. Threaded at client construction; the
/// analogue of the Go side's context value.
///
/// Default is online with no allowlist, so an unwired port keeps its
/// current behaviour.
#[derive(Clone, Default)]
pub struct NetPolicy {
    offline: bool,
    allow: Option<Arc<AllowList>>,
    audit: Option<Arc<AuditFn>>,
}

impl fmt::Debug for NetPolicy {
    fn fmt(&self, f: &mut fmt::Formatter<'_>) -> fmt::Result {
        f.debug_struct("NetPolicy")
            .field("offline", &self.offline)
            .field("allow_hosts", &self.allow_hosts())
            .field("audit", &self.audit.is_some())
            .finish()
    }
}

/// Equal when both refuse the same way: the audit sink is behaviour of
/// the observer, not of the decision.
impl PartialEq for NetPolicy {
    fn eq(&self, other: &Self) -> bool {
        self.offline == other.offline && self.allow_hosts() == other.allow_hosts()
    }
}

impl Eq for NetPolicy {}

impl NetPolicy {
    /// Build a policy from a boolean, mirroring Go's
    /// `WithOffline(ctx, bool)`. This is the one-liner the CLI layer
    /// calls once it registers the `--offline` flag.
    pub fn new(offline: bool) -> Self {
        Self {
            offline,
            ..Self::default()
        }
    }

    /// A policy that permits network access.
    pub fn online() -> Self {
        Self::new(false)
    }

    /// A policy that refuses non-loopback network access.
    pub fn offline() -> Self {
        Self::new(true)
    }

    /// Whether this policy refuses non-loopback network access.
    pub fn is_offline(&self) -> bool {
        self.offline
    }

    /// Narrow egress to a host allowlist (config key
    /// `netpolicy.allow_hosts`), the analogue of Go's `WithAllowHosts`.
    /// Entries are an exact host (`graph.microsoft.com`) or a `*.suffix`
    /// matching one or more labels below the suffix. While the list is
    /// in force a non-loopback destination matching no entry is refused
    /// with [`HostNotAllowedError`].
    ///
    /// An empty list is no list: no restriction. Entries that
    /// [`validate_allow_hosts`] rejects never match, so a malformed list
    /// fails closed; validate configuration up front to fail loud.
    pub fn with_allow_hosts<I, S>(mut self, patterns: I) -> Self
    where
        I: IntoIterator<Item = S>,
        S: AsRef<str>,
    {
        let raw: Vec<String> = patterns
            .into_iter()
            .map(|p| p.as_ref().to_string())
            .collect();
        self.allow = (!raw.is_empty()).then(|| Arc::new(AllowList::new(raw)));
        self
    }

    /// The allowlist entries as configured; empty when none is in force.
    pub fn allow_hosts(&self) -> &[String] {
        self.allow.as_deref().map_or(&[], |l| l.raw.as_slice())
    }

    /// Report every refusal this policy makes to `sink`, the analogue
    /// of Go's `WithAudit`. The sink runs synchronously on the refusing
    /// task and must not block; it sees scheme, host and path only,
    /// never credentials. Wire it to the tool's log or bus.
    pub fn with_audit<F>(mut self, sink: F) -> Self
    where
        F: Fn(&Refusal) + Send + Sync + 'static,
    {
        self.audit = Some(Arc::new(sink));
        self
    }

    /// This policy folded over `prev`, for a holder that remembers what
    /// it has seen: offline once seen stays on, and an allowlist or
    /// audit sink replaces the remembered one only when this policy
    /// carries one.
    #[cfg_attr(not(feature = "bus-network"), allow(dead_code))]
    pub(crate) fn sticky(&self, prev: &NetPolicy) -> NetPolicy {
        NetPolicy {
            offline: self.offline || prev.offline,
            allow: self.allow.clone().or_else(|| prev.allow.clone()),
            audit: self.audit.clone().or_else(|| prev.audit.clone()),
        }
    }

    /// Whether this policy restricts anything.
    #[cfg_attr(not(feature = "bus-network"), allow(dead_code))]
    pub(crate) fn is_restrictive(&self) -> bool {
        self.offline || self.allow.is_some()
    }

    /// Whether traffic to `host` (with or without a port) is permitted
    /// under this policy. Loopback is always permitted.
    pub fn allows_host(&self, host: &str) -> bool {
        self.denial(host).is_none()
    }

    /// Why `host` is refused, if it is. Offline wins over the allowlist:
    /// it is the stricter rule, and its loopback exemption is the
    /// allowlist's.
    pub(crate) fn denial(&self, host: &str) -> Option<RefusalReason> {
        if is_loopback(host) {
            return None;
        }
        if self.offline {
            return Some(RefusalReason::Offline);
        }
        match &self.allow {
            Some(l) if !l.matches(host) => Some(RefusalReason::HostNotAllowed),
            _ => None,
        }
    }

    /// Build the typed refusal for `reason` and report it to the audit
    /// sink. Every refusal this crate makes passes through here.
    pub(crate) fn refuse(&self, reason: RefusalReason, op: String, dest: String) -> PolicyError {
        if let Some(sink) = &self.audit {
            sink(&Refusal {
                reason,
                op: op.clone(),
                dest: dest.clone(),
            });
        }
        match reason {
            RefusalReason::Offline => PolicyError::Offline(OfflineError {
                method: op,
                url: dest,
            }),
            RefusalReason::HostNotAllowed => PolicyError::HostNotAllowed(HostNotAllowedError {
                method: op,
                url: dest,
            }),
        }
    }

    /// Whether a dial to `addr` on `network` is permitted under this
    /// policy, the analogue of Go's `CheckDial`. The seam for every
    /// client that opens its own socket (websocket, SMTP, SQL drivers,
    /// raw TLS): call it before dialing.
    ///
    /// Same exemptions as the HTTP guard: loopback stays reachable and
    /// unix sockets are not network at all. A refusal reads
    /// `dial <network> <addr>: network disabled by --offline`, or
    /// `...: host not allowed by netpolicy.allow_hosts`.
    pub fn check_dial(&self, network: &str, addr: &str) -> Result<(), PolicyError> {
        if is_local_network(network) {
            return Ok(());
        }
        match self.denial(addr) {
            None => Ok(()),
            Some(reason) => Err(self.refuse(reason, format!("dial {network}"), addr.to_string())),
        }
    }
}

/// Why an operation was refused.
#[derive(Debug, Clone, Copy, PartialEq, Eq)]
pub enum RefusalReason {
    /// `--offline` and the destination is not loopback.
    Offline,
    /// A host allowlist is in force and the destination is not on it.
    HostNotAllowed,
}

/// One refused operation, handed to the audit sink
/// ([`NetPolicy::with_audit`]).
#[derive(Debug, Clone, PartialEq, Eq)]
pub struct Refusal {
    /// Which rule refused.
    pub reason: RefusalReason,
    /// The HTTP method of a refused request, or `dial <network>`.
    pub op: String,
    /// Where it was going. For a request: scheme, host (with port) and
    /// path only, never query, fragment or userinfo. For a dial: the
    /// address as given.
    pub dest: String,
}

/// Either typed refusal, returned by [`NetPolicy::check_dial`].
#[derive(Debug, Clone, PartialEq, Eq)]
pub enum PolicyError {
    /// Refused by `--offline`.
    Offline(OfflineError),
    /// Refused by the host allowlist.
    HostNotAllowed(HostNotAllowedError),
}

impl PolicyError {
    /// The offline refusal, if this is one.
    pub fn as_offline(&self) -> Option<&OfflineError> {
        match self {
            PolicyError::Offline(e) => Some(e),
            PolicyError::HostNotAllowed(_) => None,
        }
    }

    /// The allowlist refusal, if this is one.
    pub fn as_host_not_allowed(&self) -> Option<&HostNotAllowedError> {
        match self {
            PolicyError::HostNotAllowed(e) => Some(e),
            PolicyError::Offline(_) => None,
        }
    }
}

impl fmt::Display for PolicyError {
    fn fmt(&self, f: &mut fmt::Formatter<'_>) -> fmt::Result {
        match self {
            PolicyError::Offline(e) => e.fmt(f),
            PolicyError::HostNotAllowed(e) => e.fmt(f),
        }
    }
}

impl std::error::Error for PolicyError {
    fn source(&self) -> Option<&(dyn std::error::Error + 'static)> {
        match self {
            PolicyError::Offline(e) => Some(e),
            PolicyError::HostNotAllowed(e) => Some(e),
        }
    }
}

impl From<OfflineError> for PolicyError {
    fn from(e: OfflineError) -> Self {
        PolicyError::Offline(e)
    }
}

impl From<HostNotAllowedError> for PolicyError {
    fn from(e: HostNotAllowedError) -> Self {
        PolicyError::HostNotAllowed(e)
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

/// The allowlist refusal, the analogue of Go's `ErrHostNotAllowed`.
/// Fields as on [`OfflineError`]; displays as
/// `<method> <url>: host not allowed by netpolicy.allow_hosts`.
#[derive(Debug, Clone, PartialEq, Eq)]
pub struct HostNotAllowedError {
    /// The refused operation: an HTTP method, or `dial <network>`.
    pub method: String,
    /// Where it was going, stripped as on [`OfflineError::url`].
    pub url: String,
}

impl fmt::Display for HostNotAllowedError {
    fn fmt(&self, f: &mut fmt::Formatter<'_>) -> fmt::Result {
        write!(
            f,
            "{} {}: {HOST_NOT_ALLOWED_MESSAGE}",
            self.method, self.url
        )
    }
}

impl std::error::Error for HostNotAllowedError {}

/// A `netpolicy.allow_hosts` entry that can never match.
#[derive(Debug, Clone, PartialEq, Eq)]
pub struct InvalidAllowHost {
    /// The entry as configured.
    pub entry: String,
}

impl fmt::Display for InvalidAllowHost {
    fn fmt(&self, f: &mut fmt::Formatter<'_>) -> fmt::Result {
        write!(
            f,
            "netpolicy.allow_hosts: invalid entry {:?}: want a host or *.suffix",
            self.entry
        )
    }
}

impl std::error::Error for InvalidAllowHost {}

/// Report the first entry that can never match: empty, a bare `*`, a
/// wildcard anywhere but a leading `*.` label, a port, a path, or a
/// `*.` suffix over an IP literal. The analogue of Go's
/// `ValidateAllowHosts`.
pub fn validate_allow_hosts<S: AsRef<str>>(patterns: &[S]) -> Result<(), InvalidAllowHost> {
    match patterns.iter().find(|p| parse_entry(p.as_ref()).is_none()) {
        Some(p) => Err(InvalidAllowHost {
            entry: p.as_ref().to_string(),
        }),
        None => Ok(()),
    }
}

struct AllowList {
    raw: Vec<String>,
    entries: Vec<Entry>,
}

struct Entry {
    host: String,
    suffix: bool,
}

impl AllowList {
    fn new(raw: Vec<String>) -> Self {
        let entries = raw.iter().filter_map(|p| parse_entry(p)).collect();
        Self { raw, entries }
    }

    fn matches(&self, hostport: &str) -> bool {
        let Some(h) = normalize_host(hostport) else {
            return false;
        };
        let is_ip = h.parse::<IpAddr>().is_ok();
        self.entries.iter().any(|e| {
            if e.suffix {
                !is_ip && h.len() > e.host.len() + 1 && h.ends_with(&format!(".{}", e.host))
            } else {
                h == e.host
            }
        })
    }
}

fn parse_entry(p: &str) -> Option<Entry> {
    let p = p.trim();
    let (p, suffix) = match p.strip_prefix("*.") {
        Some(rest) => (rest, true),
        None => (p, false),
    };
    if p.contains(['*', '/', ' ', '\t', '%']) {
        return None;
    }
    // A port or any other colon is only legal inside an IPv6 literal.
    if p.contains(':') && !p.starts_with('[') && p.parse::<Ipv6Addr>().is_err() {
        return None;
    }
    if p.starts_with('[') && !p.ends_with(']') {
        return None;
    }
    let host = normalize_host(p)?;
    if suffix && host.parse::<IpAddr>().is_ok() {
        return None;
    }
    Some(Entry { host, suffix })
}

/// Reduce `hostport` (optionally with a port, optionally a bracketed
/// IPv6 literal) to its comparison form: lower case, no trailing dot,
/// IDNA names in ASCII, IP literals canonical. `None` for anything that
/// cannot be a host. Domain names go through the `url` crate's host
/// parser, the one reqwest sends with, so the host judged is the host
/// dialed.
fn normalize_host(hostport: &str) -> Option<String> {
    let h = split_host(hostport).unwrap_or_else(|| unbracket_ipv6(hostport));
    let h = h.strip_prefix('[').unwrap_or(h);
    let h = h.strip_suffix(']').unwrap_or(h);
    if h.is_empty() || h.contains('%') {
        return None;
    }
    if let Ok(ip) = h.parse::<IpAddr>() {
        return Some(ip.to_canonical().to_string());
    }
    let h = h.strip_suffix('.').unwrap_or(h);
    if h.is_empty() || h.starts_with('.') || h.contains("..") {
        return None;
    }
    match url::Host::parse(h) {
        Ok(url::Host::Domain(d)) => Some(d),
        Ok(url::Host::Ipv4(ip)) => Some(ip.to_string()),
        _ => None,
    }
}

/// Whether `host` names a loopback address. `host` may carry a port
/// (`127.0.0.1:8080`, `[::1]:9000`), as a URL authority or a dial
/// address does.
///
/// Hosts that are not literal IPs (DNS names) are treated as remote:
/// resolving them would itself be network access. Matches Go's
/// `isLoopback`: the exact name `localhost`, the whole 127.0.0.0/8
/// range, `::1` and IPv4-mapped loopback.
///
/// Without a port, a URL keeps an IPv6 literal bracketed (`[::1]`, the
/// host of `http://[::1]/`). The brackets are dropped only when they
/// hold an IPv6 address, so `[127.0.0.1]` and `[localhost]` stay remote.
pub fn is_loopback(host: &str) -> bool {
    let h = split_host(host).unwrap_or_else(|| unbracket_ipv6(host));
    if h == "localhost" {
        return true;
    }
    h.parse::<IpAddr>()
        .is_ok_and(|ip| ip.to_canonical().is_loopback())
}

/// The address inside `[...]` when it is an IPv6 literal, `host`
/// unchanged otherwise.
fn unbracket_ipv6(host: &str) -> &str {
    host.strip_prefix('[')
        .and_then(|h| h.strip_suffix(']'))
        .filter(|inner| inner.parse::<Ipv6Addr>().is_ok())
        .unwrap_or(host)
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
