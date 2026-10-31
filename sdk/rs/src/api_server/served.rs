//! The command projection's served-call gate: who called, and what a
//! refusal looks like on HTTP. The gate itself is
//! [`ServedGate`](crate::cli::ServedGate).

use std::collections::BTreeMap;

use axum::extract::Request;
use serde_json::Value;

use super::commands::{CommandDescriptor, CODE_PERMISSION_DENIED};
use super::ctx::{self, Authenticated};
use super::error::ApiError;
use crate::authn::Claims;
use crate::cli::{CallerSource, PermissionRequest, ServedCall, ServedGate};

/// The `surface` rules see on this listener: Go's `cmdsurface.SurfaceREST`.
const SURFACE: &str = "rest";

/// The caller a request carries, before its payload is decoded. Only
/// the stack's authentication establishes one: a bare `Authorization`
/// header is not authentication.
pub(super) fn caller_of(req: &Request) -> PermissionRequest {
    let mut r = PermissionRequest {
        surface: SURFACE.into(),
        remote_addr: ctx::peer(req).map(|a| a.to_string()).unwrap_or_default(),
        ..PermissionRequest::default()
    };
    if req.extensions().get::<Authenticated>().is_some() {
        if let Some(c) = req.extensions().get::<Claims>() {
            r.caller = c.subject.clone();
            r.tenant = c.tenant.clone();
            r.scopes = c.scopes.clone();
            r.source = CallerSource::Verified;
        }
    }
    r
}

/// Asks the gate about one call of `d`: `Err` is the 403 to answer.
pub(super) fn admit(
    gate: &ServedGate,
    d: &CommandDescriptor,
    mut req: PermissionRequest,
    args: Vec<String>,
    flags: BTreeMap<String, Value>,
) -> Result<(), ApiError> {
    req.path = d.path.clone();
    req.tier = d.tier.clone();
    req.args = args;
    req.flags = flags;
    let call = ServedCall {
        request: &req,
        permissions: Some(d.permissions.as_str()).filter(|p| !p.is_empty()),
        probe: false,
    };
    gate.decide(&call).map_err(|reason| {
        // Go's wording: the bridge's sentinel, the command, the surface,
        // then the decider's reason.
        ApiError::new(
            403,
            CODE_PERMISSION_DENIED,
            format!(
                "cmdsurface: permission denied: {} on {SURFACE}: {reason}",
                d.path.join(" ")
            ),
        )
    })
}
