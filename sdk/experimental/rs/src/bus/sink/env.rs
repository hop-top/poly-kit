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
///
/// Values are handled as bytes, as Go's strings are: a path that is not
/// UTF-8 opens that exact file (on Unix), and `%q` prints stray bytes as
/// `\xNN`.
pub(super) fn sinks_from_env() -> Vec<Box<dyn Sink>> {
    #[cfg(test)]
    if let Some(sinks) = INJECTED.with(|c| c.borrow_mut().take()) {
        return sinks;
    }
    let Some(raw) = env_bytes(ENV_SINK_KIND) else {
        return Vec::new();
    };
    let kind = trim_space(&raw).to_ascii_lowercase();
    if kind.is_empty() {
        return Vec::new();
    }
    if kind != b"jsonl" {
        report(format!(
            "bus: unknown {ENV_SINK_KIND}={} (want \"jsonl\"); skipping env sink",
            quote_bytes(&raw)
        ));
        return Vec::new();
    }
    let path = env_bytes(ENV_SINK_PATH).unwrap_or_default();
    let path = trim_space(&path);
    if path.is_empty() {
        report(format!(
            "bus: {ENV_SINK_KIND}=jsonl but {ENV_SINK_PATH} is empty; skipping env sink"
        ));
        return Vec::new();
    }
    match JsonlSink::open(path_of(path)) {
        Ok(sink) => vec![Box::new(sink)],
        Err(e) => {
            report(format!(
                "bus: {ENV_SINK_KIND}=jsonl: open {}: open {}: {}; skipping env sink",
                quote_bytes(path),
                String::from_utf8_lossy(path),
                go_errno_text(&e)
            ));
            Vec::new()
        }
    }
}

#[cfg(test)]
thread_local! {
    static INJECTED: std::cell::RefCell<Option<Vec<Box<dyn Sink>>>> =
        const { std::cell::RefCell::new(None) };
}

/// Test hook: the next bus built on this thread gets `sinks` as its env
/// sinks, whatever the environment says.
#[cfg(test)]
pub(super) fn inject_sinks(sinks: Vec<Box<dyn Sink>>) {
    INJECTED.with(|c| *c.borrow_mut() = Some(sinks));
}

/// The raw bytes of env var `name`. Lossy off Unix, where env values
/// are not byte strings.
fn env_bytes(name: &str) -> Option<Vec<u8>> {
    let v = std::env::var_os(name)?;
    #[cfg(unix)]
    return Some(std::os::unix::ffi::OsStrExt::as_bytes(v.as_os_str()).to_vec());
    #[cfg(not(unix))]
    Some(v.to_string_lossy().into_owned().into_bytes())
}

#[cfg(unix)]
fn path_of(b: &[u8]) -> &std::path::Path {
    std::path::Path::new(<std::ffi::OsStr as std::os::unix::ffi::OsStrExt>::from_bytes(b))
}

#[cfg(not(unix))]
fn path_of(b: &[u8]) -> std::path::PathBuf {
    String::from_utf8_lossy(b).into_owned().into()
}

/// Go's `strings.TrimSpace` on bytes: Unicode white space off both
/// ends; an invalid byte is not space and stops the trim.
fn trim_space(b: &[u8]) -> &[u8] {
    let (mut start, mut end, mut at, mut seen) = (0, 0, 0, false);
    for chunk in b.utf8_chunks() {
        for c in chunk.valid().chars() {
            let next = at + c.len_utf8();
            if !c.is_whitespace() {
                if !seen {
                    (start, seen) = (at, true);
                }
                end = next;
            }
            at = next;
        }
        if !chunk.invalid().is_empty() {
            if !seen {
                (start, seen) = (at, true);
            }
            at += chunk.invalid().len();
            end = at;
        }
    }
    &b[start..end]
}

/// Go's `%q` on a byte string: [`quote`] for the valid text, `\xNN`
/// for each invalid byte.
fn quote_bytes(b: &[u8]) -> String {
    let mut out = String::from("\"");
    for chunk in b.utf8_chunks() {
        let q = quote(chunk.valid());
        out.push_str(&q[1..q.len() - 1]);
        for byte in chunk.invalid() {
            out.push_str(&format!("\\x{byte:02x}"));
        }
    }
    out.push('"');
    out
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

#[cfg(test)]
mod tests {
    use super::{quote_bytes, trim_space};

    #[test]
    fn trim_space_matches_go() {
        assert_eq!(trim_space(b"  a b \t\n"), b"a b");
        assert_eq!(trim_space(" \u{a0}x\u{3000}".as_bytes()), b"x");
        assert_eq!(trim_space(b" \xff "), b"\xff");
        assert_eq!(trim_space(b"   "), b"");
        assert_eq!(trim_space(b""), b"");
    }

    #[test]
    fn quote_bytes_matches_go() {
        // Expected texts are Go's %q of the same bytes.
        assert_eq!(quote_bytes("a\u{0}\"é".as_bytes()), "\"a\\x00\\\"é\"");
        assert_eq!(quote_bytes(b"a\xff\xfeb"), "\"a\\xff\\xfeb\"");
    }
}
