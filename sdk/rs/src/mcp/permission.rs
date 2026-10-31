//! The permission hook: whether the caller may run a leaf, asked per
//! `tools/call` after the destructive ceiling and before the leaf runs.
//! Go's `cmdsurface.PermissionFunc` slot; [`MountOptions::permission`]
//! installs it.
//!
//! The hook can only narrow what the earlier gates admitted (auth,
//! confirmation, the ceiling). It is asked for the caller the mount's
//! [`Verifier`](super::Verifier) established, and for none otherwise: a
//! claim in the request body or an `Authorization` header is never a
//! caller. Over stdio no header reaches the verifier, so the caller is
//! unestablished there too.
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

/// One `tools/call` as the hook sees it.
#[derive(Debug)]
pub struct PermissionCall<'a> {
    /// The resolved leaf.
    pub leaf: &'a Leaf,
    /// The caller the verifier established; `None` when none did.
    pub caller: Option<&'a Identity>,
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
/// and scopes; none is nobody.
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
        }
        let permissions = class.permissions.join(",");
        gate.decide(&ServedCall {
            request: &req,
            permissions: Some(permissions.as_str()).filter(|p| !p.is_empty()),
            probe: false,
        })
    })
}
