//! The permission hook: whether the caller may run a leaf, asked per
//! `tools/call` after the destructive ceiling and before the leaf runs.
//! Go's `cmdsurface.PermissionFunc` slot; [`MountOptions::permission`]
//! installs it.
//!
//! The hook can only narrow what the earlier gates admitted (auth,
//! confirmation, the ceiling). It is asked for the caller the mount's
//! [`Verifier`](super::Verifier) established, and for none otherwise: a
//! claim in the request body or an `Authorization` header is never a
//! caller. Over stdio no header reaches the verifier; the peer spawned
//! the process, so [`PermissionCall::owner`] is set under
//! [`StdioCaller::Owner`] (the default) and the caller is unestablished
//! under [`StdioCaller::Unestablished`].
//!
//! A refusal renders as an `isError` result at HTTP 200 carrying
//! `cmdsurface: permission denied: <command> on mcp: <reason>`, and is
//! audited ([`audit::ERR_PERMISSION_DENIED`](super::audit)).
//!
//! With features `mcp` and `cli-permissions`, [`served_hook`] adapts a
//! [`ServedGate`](crate::cli::ServedGate): the policy's `callers:`
//! rules, `permissions:` rules and `max_ops` budgets.
//!
//! [`MountOptions::permission`]: super::MountOptions::permission

use std::fmt;
use std::sync::Arc;

use serde_json::{Map, Value};

use super::bridge::Leaf;
use super::identity::Identity;

/// How the stdio peer is established: the values of
/// `services.mcp.stdio_caller`, [`MountOptions::stdio_caller`](super::MountOptions::stdio_caller).
///
/// The peer spawned the process and runs as its user, so by default it
/// holds the owner's authority, as the owner-only socket's caller does.
/// A claim inside the protocol (a `caller` field, `_meta`, an argument)
/// never changes the answer.
#[derive(Clone, Copy, Debug, Default, PartialEq, Eq)]
pub enum StdioCaller {
    /// The spawn establishes the peer as the owner: it holds every scope
    /// and matches no principal rule. The default, as in Go.
    #[default]
    Owner,
    /// Nothing is established, like an unauthenticated remote caller:
    /// caller-bound rules and budgets do not match.
    Unestablished,
}

impl StdioCaller {
    /// Parses `owner` or `unestablished`, case-insensitively; empty is
    /// the default (`owner`).
    ///
    /// # Errors
    ///
    /// An unknown value, naming the key.
    pub fn parse(s: &str) -> Result<StdioCaller, String> {
        match s.trim().to_ascii_lowercase().as_str() {
            "" | "owner" => Ok(StdioCaller::Owner),
            "unestablished" => Ok(StdioCaller::Unestablished),
            v => Err(format!(
                "services.mcp.stdio_caller: unknown caller \"{v}\"; use owner or unestablished"
            )),
        }
    }
}

/// One `tools/call` as the hook sees it.
#[derive(Debug)]
pub struct PermissionCall<'a> {
    /// The resolved leaf.
    pub leaf: &'a Leaf,
    /// The caller the verifier established; `None` when none did.
    pub caller: Option<&'a Identity>,
    /// The transport itself vouches for the caller as the owner: a
    /// stdio call under [`StdioCaller::Owner`] that no verifier
    /// established. Never true over HTTP, and never together with
    /// `caller`.
    pub owner: bool,
    /// The `arguments` object as received.
    pub arguments: &'a Map<String, Value>,
}

/// The hook a mount asks: `Err` is the refusal's reason.
pub type PermissionHook = Arc<dyn Fn(&PermissionCall<'_>) -> Result<(), String> + Send + Sync>;

/// How a [`PermissionHook`] prints: `Some(<fn>)` or `None`.
pub(crate) struct HookDebug<'a>(pub(crate) &'a Option<PermissionHook>);

impl fmt::Debug for HookDebug<'_> {
    fn fmt(&self, f: &mut fmt::Formatter<'_>) -> fmt::Result {
        f.write_str(if self.0.is_some() {
            "Some(<fn>)"
        } else {
            "None"
        })
    }
}

/// Adapts `gate` to a mount's permission hook.
///
/// The rules see the call as Go's MCP surface describes it: surface
/// `mcp`, the leaf's path, its declared `kit/side-effect` tier (a leaf
/// marked only destructive is `destructive`), every argument a flag and
/// no positionals (a leaf publishes no `args` property here), and no
/// peer address. A verified caller is matched by its principal, tenant
/// and scopes; the transport-vouched owner holds the owner's authority
/// (the owner-only socket's mapping); none is nobody.
#[cfg(feature = "cli-permissions")]
pub fn served_hook(gate: Arc<crate::cli::ServedGate>) -> PermissionHook {
    use crate::cli::{CallerSource, PermissionRequest, ServedCall};
    Arc::new(move |call| {
        let class = &call.leaf.class;
        let tier = match (class.side_effect.as_str(), class.destructive) {
            ("", true) => "destructive",
            (t, _) => t,
        };
        let mut req = PermissionRequest {
            surface: super::audit::SURFACE.into(),
            path: call.leaf.path.clone(),
            tier: tier.into(),
            flags: call
                .arguments
                .iter()
                .map(|(k, v)| (k.clone(), v.clone()))
                .collect(),
            ..PermissionRequest::default()
        };
        if let Some(id) = call.caller {
            req.caller = id.caller.clone();
            req.tenant = id.tenant.clone();
            req.scopes = id.scopes.clone();
            req.source = CallerSource::Verified;
        } else if call.owner {
            req.source = CallerSource::Transport;
        }
        let permissions = class.permissions.join(",");
        gate.decide(&ServedCall {
            request: &req,
            permissions: Some(permissions.as_str()).filter(|p| !p.is_empty()),
            probe: false,
        })
    })
}
