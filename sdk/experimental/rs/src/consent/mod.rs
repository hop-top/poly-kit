//! Per-capability consent grants, a port of Go `go/core/consent`.
//! Gated behind the `consent` feature; the per-tool files and the relay
//! behind `consent-file` (native only).
//!
//! A [`Grant`] answers "may this subject use this capability now": a
//! capability id such as `io.screen.capture`, a subject (empty for
//! any), allow or deny, and a duration of once, session, until or
//! always. [`Grants::request`] asks a caller-supplied [`Prompter`] when
//! nothing stands; [`Grants::use_grant`] gates every tap and audits it
//! first; every path fails closed. Refusals map onto the kit exit
//! taxonomy ([`RefusedError`]: `denied` is `UNAUTHORIZED`, exit 5,
//! permanent; `unset`, `expired`, `declined` are `CONSENT_REFUSED`,
//! exit 7, transient).
//!
//! With `consent-file`, [`open`] builds a session over the per-tool
//! `<XDG_STATE_HOME>/<tool>/consent/grants.yaml` ([`FileStore`]) and
//! audit log ([`FileAuditor`]); a [`Relay`] delivers that log onto the
//! bus as `kit.core.consent.*` events with `<log>:<seq>` idempotency
//! keys and an ack cursor.
//!
//! Telemetry consent is the [`CAP_TELEMETRY`] capability: the file
//! store combines the grants file, `kit.telemetry.consent` in
//! `<XDG_CONFIG_HOME>/kit/config.yaml` and the legacy
//! `kit/telemetry.yaml` deny-wins, mirrors writes into the legacy
//! records and clears all of them on revoke. Checks run the telemetry
//! resolver, so `DO_NOT_TRACK` and `KIT_TELEMETRY_MODE=off` are a
//! standing deny.
//!
//! The contract is `contracts/consent-v1`.
//!
//! # Example
//!
//! ```
//! use std::sync::Arc;
//! use hop_top_kit::consent::{Answer, Duration, Effect, Grants, MemoryGrantStore, Prompt};
//!
//! let g = Grants::builder(Arc::new(MemoryGrantStore::new())).build();
//! let ask = |_: &Prompt| -> Result<Answer, String> {
//!     Ok(Answer { decision: Effect::Allow, duration: Duration::Session, expires_at: None })
//! };
//! g.request("io.clipboard.read", "app:dek", Some(&ask)).unwrap();
//! g.use_grant("io.clipboard.read", "app:dek").unwrap(); // before the tap
//! ```

mod audit;
mod builder;
mod error;
mod grant;
mod grants;
mod prompt;
mod store;
mod telemetry;
mod time;

#[cfg(feature = "consent-file")]
mod file_audit;
#[cfg(feature = "consent-file")]
mod file_store;
#[cfg(feature = "consent-file")]
mod paths;
#[cfg(feature = "consent-file")]
mod relay;
#[cfg(feature = "consent-file")]
mod yaml_doc;

pub use audit::{
    AuditEvent, AuditKind, Auditor, BusAuditor, Delivery, MultiAuditor, Publisher, RefusalCause,
    Topics, EVENT_SOURCE,
};
pub use builder::GrantsBuilder;
pub use error::{Error, ErrorKind, GrantError, Reason, RefusedError, EXIT_DENIED, EXIT_REFUSED};
pub use grant::{
    validate_capability, validate_subject, DecisionSource, Duration, Effect, Grant, CAP_TELEMETRY,
};
pub use grants::{Clock, Grants, Verdict};
pub use prompt::{Answer, Prompt, Prompter};
pub use store::{GrantStore, MemoryGrantStore};
pub use telemetry::{deny_wins, do_not_track, os_env, Env, TelemetryDecision};

#[cfg(feature = "consent-file")]
pub use file_audit::FileAuditor;
#[cfg(feature = "consent-file")]
pub use file_store::FileStore;
#[cfg(feature = "consent-file")]
pub use paths::{default_tool, open, state_file};
#[cfg(feature = "consent-file")]
pub use relay::{FlushError, FlushResult, Relay};
