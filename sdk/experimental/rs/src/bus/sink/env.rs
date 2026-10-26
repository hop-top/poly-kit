//! The env-configured sink: Go's `env_sink.go`. Not on wasm32.

use std::io;
use std::sync::{Arc, RwLock};

use super::{JsonlSink, Sink};
use crate::bus::mem::{BusError, SharedErrFn};
use crate::bus::quote::quote;

/// Selects a sink for [`DynBus::new`](crate::bus::DynBus::new) to
/// attach: Go's `EnvSinkKind`. Trimmed and case-insensitive; the one
/// kind is `jsonl`, a [`JsonlSink`] appending to the file at
/// [`ENV_SINK_PATH`] (created if absent). Unset or blank, no sink is
/// attached and the bus is not wrapped. Any other value is reported
/// (see [`set_env_sink_err_reporter`]) and attaches nothing.
pub const ENV_SINK_KIND: &str = "KIT_BUS_SINK";

/// The file the `jsonl` env sink appends to: Go's `EnvSinkPath`. Blank
/// with [`ENV_SINK_KIND`] set to `jsonl`, the sink is skipped and the
/// misconfiguration reported, so events do not vanish silently.
pub const ENV_SINK_PATH: &str = "KIT_BUS_SINK_PATH";

/// Receives env-sink diagnostics. `None` drops them, so the bus stays
/// quiet by default, as in Go.
static REPORTER: RwLock<Option<SharedErrFn>> = RwLock::new(None);

/// Sends env-sink diagnostics (unknown kind, blank path, open failure)
/// to `f`: Go's `SetEnvSinkErrReporter`. Process-wide, as the env
/// switch is; it applies to buses built afterwards.
pub fn set_env_sink_err_reporter(f: impl Fn(&BusError) + Send + Sync + 'static) {
    *REPORTER.write().unwrap_or_else(|p| p.into_inner()) = Some(Arc::new(f));
}

/// Drops env-sink diagnostics again, the default: Go's
/// `SetEnvSinkErrReporter(nil)`.
pub fn reset_env_sink_err_reporter() {
    *REPORTER.write().unwrap_or_else(|p| p.into_inner()) = None;
}

fn report(msg: String) {
    let f = REPORTER.read().unwrap_or_else(|p| p.into_inner()).clone();
    if let Some(f) = f {
        f(&BusError::handler(msg));
    }
}

/// The sinks the environment configures: Go's `envSinksFromEnv`. Empty
/// when none is, so the caller skips the tee. Misconfiguration is
/// reported and skipped: the bus keeps working on a typo.
pub(super) fn sinks_from_env() -> Vec<Box<dyn Sink>> {
    let Some(raw) = std::env::var_os(ENV_SINK_KIND) else {
        return Vec::new();
    };
    let raw = raw.to_string_lossy();
    let kind = raw.trim().to_lowercase();
    if kind.is_empty() {
        return Vec::new();
    }
    if kind != "jsonl" {
        report(format!(
            "bus: unknown {ENV_SINK_KIND}={} (want \"jsonl\"); skipping env sink",
            quote(&raw)
        ));
        return Vec::new();
    }
    let path = std::env::var_os(ENV_SINK_PATH).unwrap_or_default();
    let path = path.to_string_lossy();
    let path = path.trim();
    if path.is_empty() {
        report(format!(
            "bus: {ENV_SINK_KIND}=jsonl but {ENV_SINK_PATH} is empty; skipping env sink"
        ));
        return Vec::new();
    }
    match JsonlSink::open(path) {
        Ok(sink) => vec![Box::new(sink)],
        Err(e) => {
            report(format!(
                "bus: {ENV_SINK_KIND}=jsonl: open {}: open {path}: {}; skipping env sink",
                quote(path),
                go_errno_text(&e)
            ));
            Vec::new()
        }
    }
}

/// The text Go's `syscall.Errno` prints for the common open failures;
/// any other error keeps its own text.
fn go_errno_text(e: &io::Error) -> String {
    match e.kind() {
        io::ErrorKind::NotFound => "no such file or directory".into(),
        io::ErrorKind::PermissionDenied => "permission denied".into(),
        io::ErrorKind::IsADirectory => "is a directory".into(),
        io::ErrorKind::NotADirectory => "not a directory".into(),
        io::ErrorKind::ReadOnlyFilesystem => "read-only file system".into(),
        _ => e.to_string(),
    }
}
