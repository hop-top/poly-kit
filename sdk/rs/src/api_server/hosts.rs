//! The Host allowlist, a port of Go's `HostCheck` and `ListenerHosts`.

use std::collections::HashSet;
use std::net::{IpAddr, SocketAddr};

use axum::extract::{Request, State};
use axum::middleware::Next;
use axum::response::Response;

use super::ctx::host_of;
use super::error::{refuse, ApiError, RefusalWriter, CODE_HOST_REJECTED};

/// The hosts a loopback listener serves.
pub const LOOPBACK_HOSTS: [&str; 3] = ["localhost", "127.0.0.1", "::1"];

/// Splits `host` into a lower-cased name (trailing dot and IPv6
/// brackets dropped, IPs in canonical form) and a port, empty when
/// none.
pub fn split_host(host: &str) -> (String, String) {
    let (mut name, port) = if let Ok(sa) = host.parse::<SocketAddr>() {
        (sa.ip().to_string(), sa.port().to_string())
    } else if let Some((h, p)) = host.rsplit_once(':') {
        let bracketed = h.starts_with('[') && h.ends_with(']');
        if (!h.contains(':') || bracketed) && !p.is_empty() && p.bytes().all(|b| b.is_ascii_digit())
        {
            (h.to_string(), p.to_string())
        } else {
            (host.to_string(), String::new())
        }
    } else {
        (host.to_string(), String::new())
    };
    name = name
        .trim_start_matches('[')
        .trim_end_matches(']')
        .to_string();
    name = name.to_lowercase();
    name = name.trim_end_matches('.').to_string();
    if let Ok(ip) = name.parse::<IpAddr>() {
        name = ip.to_canonical().to_string();
    }
    (name, port)
}

/// The Host values a listener at `addr` answers to, and whether it is a
/// wildcard bind (which cannot tell which names are its own). A
/// loopback bind serves all three loopback names plus its own.
pub fn listener_hosts(addr: &str) -> (Vec<String>, bool) {
    let host = match addr.rsplit_once(':') {
        Some((h, p)) if p.bytes().all(|b| b.is_ascii_digit()) => h,
        _ => addr,
    };
    let (name, _) = split_host(host);
    if name.is_empty() {
        return (Vec::new(), true);
    }
    let bare = name.split('%').next().unwrap_or(&name);
    let ip = bare.parse::<IpAddr>().ok();
    if ip.is_some_and(|i| i.is_unspecified()) {
        return (Vec::new(), true);
    }
    if name == "localhost" || ip.is_some_and(|i| i.is_loopback()) {
        let mut hosts: Vec<String> = LOOPBACK_HOSTS.iter().map(|s| s.to_string()).collect();
        if !hosts.contains(&name) {
            hosts.push(name);
        }
        return (hosts, false);
    }
    (vec![name], false)
}

/// What the host check is configured with.
#[derive(Clone, Default)]
pub struct HostCheckConfig {
    /// Allowed entries: `name` (any port), `name:port`, or `*`.
    pub allow: Vec<String>,
    /// Answers a refusal in the transport's own envelope.
    pub refuse: Option<RefusalWriter>,
}

#[derive(Clone)]
pub(crate) struct HostAllowlist {
    any: bool,
    names: HashSet<String>,
    host_port: HashSet<String>,
    refuse: Option<RefusalWriter>,
}

impl HostAllowlist {
    pub(crate) fn new(cfg: &HostCheckConfig) -> Self {
        let mut a = HostAllowlist {
            any: false,
            names: HashSet::new(),
            host_port: HashSet::new(),
            refuse: cfg.refuse.clone(),
        };
        for e in &cfg.allow {
            let e = e.trim();
            if e == "*" {
                a.any = true;
                continue;
            }
            let (name, port) = split_host(e);
            if name.is_empty() {
                continue;
            }
            if port.is_empty() {
                a.names.insert(name);
            } else {
                a.host_port.insert(format!("{name}:{port}"));
            }
        }
        a
    }

    pub(crate) fn permits(&self, host: &str) -> bool {
        if self.any || host.is_empty() {
            return true;
        }
        let (name, port) = split_host(host);
        self.names.contains(&name) || self.host_port.contains(&format!("{name}:{port}"))
    }
}

pub(crate) async fn host_check(
    State(allow): State<HostAllowlist>,
    req: Request,
    next: Next,
) -> Response {
    let host = host_of(&req);
    if !allow.permits(&host) {
        return refuse(
            &allow.refuse,
            ApiError::new(
                403,
                CODE_HOST_REJECTED,
                format!("host {host:?} is not served here"),
            ),
        );
    }
    next.run(req).await
}

/// Whether `host` passes an allowlist, for tests of the matching rules
/// without a server.
pub fn host_permits(cfg: &HostCheckConfig, host: &str) -> bool {
    HostAllowlist::new(cfg).permits(host)
}
