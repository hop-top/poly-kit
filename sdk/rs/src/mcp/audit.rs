//! The audit hook: one record per refusal the surface decides before
//! adopter code runs.
//!
//! Go's bridge emits every pre-flight refusal to its sinks
//! (`cmdsurface.Bridge.Audit`) before the surface answers, so a sink
//! sees "refused" with the same provenance whether or not the command
//! ran. This port has no sink abstraction; a mount takes one hook
//! instead ([`MountOptions::audit`]) and calls it with an
//! [`AuditRecord`] for each refusal, on both eras and over stdio.
//!
//! The hook is best-effort. It runs synchronously once the refusal is
//! decided and before the response is returned (Go's order: audit, then
//! respond), and a panic inside it is caught and dropped, so nothing it
//! does can change the wire response.
//!
//! Audited: an auth-required leaf refused, a confirmation gate
//! unsatisfied, a confirmation declined, a confirmation `requestState`
//! that failed verification, the destructive ceiling, and a permission
//! refusal. Not audited:
//! an unknown tool, malformed arguments, the origin allowlist, a run's
//! own outcome, and an expired-but-authentic `requestState` (Go
//! re-prompts silently).
//!
//! [`MountOptions::audit`]: super::MountOptions::audit

use std::collections::BTreeMap;
use std::fmt;
use std::panic::{self, AssertUnwindSafe};
use std::sync::Arc;
use std::time::SystemTime;

use super::bridge::Leaf;
use super::HandlerConfig;

/// The hook a mount calls with each refusal.
pub type AuditHook = Arc<dyn Fn(&AuditRecord) + Send + Sync>;

/// The `surface` on every record: Go's `cmdsurface.SurfaceMCP`, one
/// value for both revisions.
pub const SURFACE: &str = "mcp";

/// The `extra` key carrying the refused request's spec revision.
pub const EXTRA_SPEC_VERSION: &str = "mcp_spec_version";
/// The `extra` key naming why a `requestState` was rejected.
pub const EXTRA_CONFIRM_REJECTION: &str = "mcp_confirm_rejection";
/// The one `mcp_confirm_rejection` value: verification failed.
pub const CONFIRM_REJECTION_STATE_VERIFICATION_FAILED: &str = "request_state_verification_failed";

/// `error` for an auth-required leaf refused: Go's
/// `cmdsurface.ErrAuthRefused`.
pub const ERR_AUTH_REFUSED: &str = "cmdsurface: authentication refused";
/// `error` for a confirmation gate nothing satisfied: Go's
/// `mcpsdk.ErrConfirmationRequired`.
pub const ERR_CONFIRMATION_REQUIRED: &str = "mcpsdk: confirmation required";
/// `error` for a confirmation the client's user declined or dismissed:
/// Go's `mcpsdk.ErrConfirmationDeclined`.
pub const ERR_CONFIRMATION_DECLINED: &str = "mcpsdk: confirmation declined";
/// `error` for a presented `requestState` that failed verification:
/// Go's `mcpsdk.ErrConfirmationStateRejected`.
pub const ERR_CONFIRMATION_STATE_REJECTED: &str =
    "mcpsdk: confirmation requestState failed verification";
/// `error` for a call the permission hook refused: Go's
/// `cmdsurface.ErrPermissionDenied`.
pub const ERR_PERMISSION_DENIED: &str = "cmdsurface: permission denied";
/// `error` for a destructive leaf the mount policy refuses: Go's
/// `cmdsurface.ErrDestructiveBlocked`.
pub const ERR_DESTRUCTIVE_BLOCKED: &str = "cmdsurface: destructive command blocked on this surface";

/// One refusal, as the hook receives it.
///
/// The subset of Go's `cmdsurface.Invocation` a pre-flight refusal
/// carries: the leaf, the surface, when it was decided, plus the error
/// and the `Meta.Extra` bag. No caller, arguments or flags: none has
/// been established or decoded when these gates answer.
#[derive(Debug, Clone, PartialEq, Eq)]
pub struct AuditRecord {
    /// The resolved leaf's path segments, e.g. `["widget", "delete"]`.
    /// Only a resolved leaf is audited.
    pub path: Vec<String>,
    /// Always [`SURFACE`].
    pub surface: &'static str,
    /// When the refusal was decided.
    pub requested_at: SystemTime,
    /// The refused request's revision: `2024-11-05` or `2026-07-28`.
    pub spec_version: String,
    /// Go's sentinel message for the event, verbatim: one of the `ERR_*`
    /// constants in this module.
    pub error: String,
    /// Go's `Meta.Extra`: always [`EXTRA_SPEC_VERSION`], plus
    /// [`EXTRA_CONFIRM_REJECTION`] for a rejected `requestState`.
    pub extra: BTreeMap<String, String>,
}

/// Which refusal a record describes.
#[derive(Debug, Clone, Copy, PartialEq, Eq)]
pub(crate) enum Refusal {
    /// An auth-required leaf, no caller established.
    AuthRefused,
    /// A confirmation gate nothing satisfied.
    ConfirmationRequired,
    /// A confirmation the client answered decline or cancel.
    ConfirmationDeclined,
    /// A `requestState` that failed verification.
    ConfirmationStateRejected,
    /// The destructive ceiling.
    DestructiveBlocked,
    /// The permission hook.
    PermissionDenied,
}

impl Refusal {
    /// Go's sentinel for the event.
    fn error(self) -> &'static str {
        match self {
            Self::AuthRefused => ERR_AUTH_REFUSED,
            Self::ConfirmationRequired => ERR_CONFIRMATION_REQUIRED,
            Self::ConfirmationDeclined => ERR_CONFIRMATION_DECLINED,
            Self::ConfirmationStateRejected => ERR_CONFIRMATION_STATE_REJECTED,
            Self::DestructiveBlocked => ERR_DESTRUCTIVE_BLOCKED,
            Self::PermissionDenied => ERR_PERMISSION_DENIED,
        }
    }
}

/// Calls the mount's hook, if any, with the record for `refusal` on
/// `leaf`. Never fails: a panicking hook is caught and ignored.
pub(crate) fn emit(cfg: &HandlerConfig, leaf: &Leaf, spec_version: &str, refusal: Refusal) {
    let Some(hook) = &cfg.audit else {
        return;
    };
    let mut extra = BTreeMap::new();
    extra.insert(EXTRA_SPEC_VERSION.to_owned(), spec_version.to_owned());
    if refusal == Refusal::ConfirmationStateRejected {
        extra.insert(
            EXTRA_CONFIRM_REJECTION.to_owned(),
            CONFIRM_REJECTION_STATE_VERIFICATION_FAILED.to_owned(),
        );
    }
    let record = AuditRecord {
        path: leaf.path.clone(),
        surface: SURFACE,
        requested_at: SystemTime::now(),
        spec_version: spec_version.to_owned(),
        error: refusal.error().to_owned(),
        extra,
    };
    // Best-effort by contract: the hook's outcome, a panic included,
    // never reaches the response.
    let _ = panic::catch_unwind(AssertUnwindSafe(|| hook(&record)));
}

/// How an [`AuditHook`] prints: `Some(<fn>)` or `None`.
pub(crate) struct HookDebug<'a>(pub(crate) &'a Option<AuditHook>);

impl fmt::Debug for HookDebug<'_> {
    fn fmt(&self, f: &mut fmt::Formatter<'_>) -> fmt::Result {
        match self.0 {
            Some(_) => f.write_str("Some(<fn>)"),
            None => f.write_str("None"),
        }
    }
}

#[cfg(test)]
mod tests {
    use super::*;
    use crate::mcp::bridge::CallResult;
    use std::sync::Mutex;

    fn leaf() -> Leaf {
        Leaf::new(&["widget", "delete"], "Delete", |_| {
            Ok(CallResult::default())
        })
    }

    #[test]
    fn no_hook_emits_nothing_and_a_hook_gets_every_field() {
        emit(
            &HandlerConfig::default(),
            &leaf(),
            "2024-11-05",
            Refusal::AuthRefused,
        );

        let seen = Arc::new(Mutex::new(Vec::new()));
        let sink = Arc::clone(&seen);
        let cfg = HandlerConfig {
            audit: Some(Arc::new(move |r: &AuditRecord| {
                sink.lock().unwrap().push(r.clone());
            })),
            ..HandlerConfig::default()
        };
        let before = SystemTime::now();
        emit(
            &cfg,
            &leaf(),
            "2026-07-28",
            Refusal::ConfirmationStateRejected,
        );

        let seen = seen.lock().unwrap();
        assert_eq!(seen.len(), 1);
        let record = &seen[0];
        assert_eq!(record.path, ["widget", "delete"]);
        assert_eq!(record.surface, "mcp");
        assert_eq!(record.spec_version, "2026-07-28");
        assert_eq!(record.error, ERR_CONFIRMATION_STATE_REJECTED);
        assert_eq!(
            record.extra,
            BTreeMap::from([
                (
                    "mcp_confirm_rejection".to_owned(),
                    "request_state_verification_failed".to_owned()
                ),
                ("mcp_spec_version".to_owned(), "2026-07-28".to_owned()),
            ])
        );
        assert!(record.requested_at >= before && record.requested_at <= SystemTime::now());
    }

    #[test]
    fn only_a_rejected_state_carries_the_rejection_key() {
        let seen = Arc::new(Mutex::new(Vec::new()));
        let sink = Arc::clone(&seen);
        let cfg = HandlerConfig {
            audit: Some(Arc::new(move |r: &AuditRecord| {
                sink.lock().unwrap().push(r.clone());
            })),
            ..HandlerConfig::default()
        };
        for refusal in [
            Refusal::AuthRefused,
            Refusal::ConfirmationRequired,
            Refusal::ConfirmationDeclined,
            Refusal::DestructiveBlocked,
        ] {
            emit(&cfg, &leaf(), "2024-11-05", refusal);
        }
        for record in seen.lock().unwrap().iter() {
            assert_eq!(
                record.extra.keys().collect::<Vec<_>>(),
                ["mcp_spec_version"],
                "{record:?}"
            );
        }
    }

    #[test]
    fn a_panicking_hook_is_contained() {
        let cfg = HandlerConfig {
            audit: Some(Arc::new(|_: &AuditRecord| panic!("sink down"))),
            ..HandlerConfig::default()
        };
        emit(&cfg, &leaf(), "2024-11-05", Refusal::DestructiveBlocked);
    }

    #[test]
    fn hook_debug_never_prints_the_closure() {
        assert_eq!(format!("{:?}", HookDebug(&None)), "None");
        let hook: Option<AuditHook> = Some(Arc::new(|_| {}));
        assert_eq!(format!("{:?}", HookDebug(&hook)), "Some(<fn>)");
    }
}
