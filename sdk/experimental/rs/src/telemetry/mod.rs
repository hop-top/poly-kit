//! SDK telemetry module. Gated behind the `telemetry` feature.
//!
//! Mirrors the Go-side contract (`hop.top/kit/go/runtime/telemetry`) at
//! the data-only seams: Mode enum + env precedence (T-0723), install_id
//! reader/writer (T-0724), consent-file reader (T-0725). The Rust SDK
//! is read-only against the consent file — kit-consent (Go) owns
//! writes. See ADR-0035 (canonical contract) and ADR-0038 (SDK delta).

pub mod client;
pub mod consent;
pub mod install_id;
pub mod mode;
pub mod redact;

pub use client::{Client, ClientError, ClientOptions, Envelope, SinkKind};
pub use consent::{consent_path, load_consent, Consent};
pub use install_id::{get_install_id, install_id_path, rotate};
pub use mode::{parse_mode, resolve_mode, Mode};
pub use redact::{redact, redact_string};
