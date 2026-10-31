//! The four seams kit treats as the canonical boundary for mutating side
//! effects in CLI commands, a port of Go `go/runtime/sideeffect`: [`Fs`],
//! [`Http`], [`Bus`] and [`Exec`].
//!
//! Three implementations of each:
//!
//! - `real` (feature `sideeffect-real`, `sideeffect-http` for HTTP):
//!   stdlib and kit primitives, the production default. Native only.
//! - [`dryrun`]: prints what would happen and returns synthetic answers.
//! - [`testfake`]: records calls with assertion helpers, for tests.
//!
//! Reads (`std::fs::read`, metadata, GET and HEAD) pass through to the
//! standard library unchanged: dry-run does not pretend reads are unsafe.
//!
//! The dry-run flag travels on a [`Context`](crate::bus::Context)
//! ([`with_dry_run`], [`is_dry_run`]); [`DryRunPublisher`] tags published
//! events with it. The `--dry-run` command-line flag is not wired here:
//! that follows the CLI root port.
//!
//! # Differences from Go
//!
//! - Payloads are `serde_json::Value`s taken by value, not `any`, so the
//!   dry-run tag is applied to every payload that carries qualifiers,
//!   not only ones passed by pointer; see [`tag_mechanism`] for what
//!   "carries qualifiers" means without field embedding.
//! - `Http` is async and works on plain [`Request`] / [`Response`] data;
//!   `Exec` takes a [`Command`] rather than an `exec.Cmd`.
//! - The real bus publishes on a kit [`Adapter`](crate::bus::Adapter)
//!   (Go wraps `domain.EventPublisher`, whose Rust payload type is a
//!   closed enum).
//! - Test fakes fail an unexpected call by panicking, where Go calls
//!   `t.Fatalf`.
//!
//! # Example
//!
//! ```
//! use hop_top_kit::sideeffect::{dryrun::SharedBuf, dryrun, Fs};
//!
//! let out = SharedBuf::default();
//! let fs = dryrun::Fs::new().with_writer(out.clone());
//! fs.write_file("/etc/app.conf".as_ref(), b"x=1", 0o644).unwrap();
//! assert_eq!(
//!     out.contents(),
//!     "[dry-run] would write /etc/app.conf (3 bytes, mode 0644)\n"
//! );
//! ```
//!
//! [`Adapter`]: crate::bus::Adapter

mod context;
pub mod dryrun;
mod publisher;
mod seams;
pub mod testfake;

#[cfg(all(feature = "sideeffect-real", not(target_arch = "wasm32")))]
pub mod real;

pub use context::{is_dry_run, with_dry_run};
pub use publisher::{tag_mechanism, DryRunPublisher, DRY_RUN_MECHANISM};
pub use seams::{
    BoxFuture, Bus, Command, Exec, ExecError, Fs, Http, HttpError, PublishError, Request, Response,
};
