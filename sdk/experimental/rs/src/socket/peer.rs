//! Authentication: the [`Authenticator`] seam and the kernel-uid
//! [`PeerAuthenticator`] (go: `NewPeerAuthenticator`).

use std::collections::BTreeMap;
use std::fmt;
use std::sync::Arc;

use super::wire::Request;

/// Meta extra key: the peer's effective uid, recorded on every request
/// the peer authenticator decides, admitted or refused.
pub const EXTRA_PEER_UID: &str = "peer_uid";
/// Meta extra key: the peer's effective gid.
pub const EXTRA_PEER_GID: &str = "peer_gid";
/// Meta extra key: the peer's pid where the platform reports it. For
/// the audit trail only: a pid can be reused once the process exits.
pub const EXTRA_PEER_PID: &str = "peer_pid";

/// What the kernel reports about the process on the other end of a
/// Unix socket connection, as of when it connected.
#[derive(Debug, Clone, Copy, PartialEq, Eq)]
pub struct PeerCred {
    pub uid: u32,
    pub gid: u32,
    /// `None` where the platform does not report it (FreeBSD).
    pub pid: Option<i32>,
}

impl PeerCred {
    fn extra(&self) -> BTreeMap<String, String> {
        let mut m = BTreeMap::new();
        m.insert(EXTRA_PEER_UID.to_string(), self.uid.to_string());
        m.insert(EXTRA_PEER_GID.to_string(), self.gid.to_string());
        if let Some(pid) = self.pid.filter(|p| *p > 0) {
            m.insert(EXTRA_PEER_PID.to_string(), pid.to_string());
        }
        m
    }
}

/// An accepted connection, as an [`Authenticator`] sees it.
#[derive(Debug, Clone)]
pub struct Connection {
    peer: Result<PeerCred, String>,
    server_uid: u32,
}

impl Connection {
    /// `peer` is the kernel's answer read when the connection was
    /// accepted, or why it could not be read; `server_uid` is the uid
    /// the server runs as.
    pub fn new(peer: Result<PeerCred, String>, server_uid: u32) -> Self {
        Self { peer, server_uid }
    }

    /// The peer's credentials, or why the kernel would not say.
    pub fn peer(&self) -> Result<&PeerCred, &str> {
        self.peer.as_ref().map_err(String::as_str)
    }

    /// The uid the server process runs as.
    pub fn server_uid(&self) -> u32 {
        self.server_uid
    }
}

/// What an [`Authenticator`] establishes for a request.
#[derive(Debug, Clone, Default)]
pub struct Identity {
    /// The verified caller identifier.
    pub principal: String,
    /// The verified tenant, empty for single-tenant tools.
    pub tenant: String,
    /// Entitlements the verified caller holds, for the handler's
    /// permission check. A caller the socket file alone established
    /// holds the owner's authority instead and is not asked.
    pub scopes: Vec<String>,
    /// Provenance recorded beside the verdict. A `scopes` entry is
    /// dropped: scopes come from [`Identity::scopes`] alone.
    pub extra: BTreeMap<String, String>,
}

/// An authenticator's refusal. `extra` is recorded on the refused
/// request's audit record too.
#[derive(Debug, Clone, Default)]
pub struct Refusal {
    pub message: String,
    pub extra: BTreeMap<String, String>,
}

impl Refusal {
    pub fn new(message: impl Into<String>) -> Self {
        Self {
            message: message.into(),
            extra: BTreeMap::new(),
        }
    }
}

impl fmt::Display for Refusal {
    fn fmt(&self, f: &mut fmt::Formatter<'_>) -> fmt::Result {
        f.write_str(&self.message)
    }
}

impl std::error::Error for Refusal {}

/// Verifies who is on the other end of a socket request. It sees the
/// connection (so an implementation may read peer credentials) and the
/// request (so it may verify a token the caller sent in a flag). A
/// refusal answers the request with `UNAUTHENTICATED`; the returned
/// [`Identity`] replaces the request's claimed caller and tenant.
///
/// The transport ships without one: the socket is owner-only by
/// construction, and for the common case the file permission is the
/// authentication. [`PeerAuthenticator`] names the caller by the uid
/// the kernel reports.
pub trait Authenticator: Send + Sync {
    fn authenticate(&self, conn: &Connection, req: &Request) -> Result<Identity, Refusal>;
}

type ScopeFn = dyn Fn(&PeerCred) -> Vec<String> + Send + Sync;

/// Configures [`PeerAuthenticator`].
#[derive(Clone, Default)]
pub struct PeerAuthConfig {
    /// Refuse a peer whose uid is not the server process's. The
    /// owner-only socket file already confines callers to that uid and
    /// root; this turns root away too, and any caller a deliberately
    /// widened socket would let in.
    pub require_same_uid: bool,
    /// Scopes an admitted peer holds, from its credentials. Not asked
    /// about a refused peer. `None` grants none.
    pub scopes: Option<Arc<ScopeFn>>,
}

impl PeerAuthConfig {
    /// Grant admitted peers the scopes `f` returns.
    pub fn with_scopes<F>(mut self, f: F) -> Self
    where
        F: Fn(&PeerCred) -> Vec<String> + Send + Sync + 'static,
    {
        self.scopes = Some(Arc::new(f));
        self
    }
}

/// Identifies each request by its connection's peer credentials: the
/// principal is `uid:<n>`, the tenant empty, and uid, gid and pid are
/// recorded in the identity's extra ([`EXTRA_PEER_UID`] and friends).
/// The transport marks the identity verified: the kernel, not the
/// caller, said who it is.
///
/// A peer the kernel cannot describe, or one `require_same_uid` turns
/// away, is refused as `UNAUTHENTICATED`, and the refusal carries the
/// peer's credentials when they were read.
///
/// The Go port can also name the principal by user name
/// (`ResolveNames`); that needs a passwd lookup the standard library
/// does not offer, so this one is always `uid:<n>`.
#[derive(Clone, Default)]
pub struct PeerAuthenticator {
    cfg: PeerAuthConfig,
}

impl PeerAuthenticator {
    pub fn new(cfg: PeerAuthConfig) -> Self {
        Self { cfg }
    }
}

impl Authenticator for PeerAuthenticator {
    fn authenticate(&self, conn: &Connection, _req: &Request) -> Result<Identity, Refusal> {
        let cred = conn
            .peer()
            .map_err(|why| Refusal::new(format!("peer credentials: {why}")))?;
        let extra = cred.extra();
        if self.cfg.require_same_uid && cred.uid != conn.server_uid() {
            return Err(Refusal {
                message: format!(
                    "peer uid {} is not the server's uid {}",
                    cred.uid,
                    conn.server_uid()
                ),
                extra,
            });
        }
        Ok(Identity {
            principal: format!("uid:{}", cred.uid),
            tenant: String::new(),
            scopes: self
                .cfg
                .scopes
                .as_ref()
                .map_or_else(Vec::new, |f| clean_scopes(f(cred))),
            extra,
        })
    }
}

/// Trimmed, without empties and duplicates, in first-seen order.
fn clean_scopes(scopes: Vec<String>) -> Vec<String> {
    let mut out: Vec<String> = Vec::new();
    for s in scopes {
        let s = s.trim().to_string();
        if !s.is_empty() && !out.contains(&s) {
            out.push(s);
        }
    }
    out
}
