//! The desktop notification sink: bus events as OS-native notifications.
//! Gated behind `notify-os`; native only.
//!
//! Ports `go/runtime/notify/sinks/osnotify`. macOS runs `osascript`
//! (`display notification`); Linux runs `notify-send`, probed on `PATH`
//! when the sink is built. Windows is not supported. The pipeline per
//! event is title and text templates, a redactor on each, then the
//! breaker-gated [`Runner`].
//!
//! The commands are spawned directly, never through a shell. A title or
//! text is one argument, so no shell metacharacter is interpreted; the
//! AppleScript literal is escaped for `"` and `\`.

use std::fmt;
use std::io;
use std::path::Path;
use std::process::{Command, Stdio};
use std::sync::Arc;
use std::time::Duration;

use crate::breaker::Breaker;
use crate::bus::{BusError, Context, Event, Sink};
use crate::redact::Redactor;

use super::template::Template;

/// The notification mechanism of a platform.
#[derive(Clone, Copy, Debug, PartialEq, Eq)]
pub enum Platform {
    /// `osascript -e 'display notification ...'`.
    Darwin,
    /// `notify-send <title> <text>`.
    Linux,
}

/// Why a sink could not be built.
#[derive(Debug, PartialEq, Eq)]
pub enum OsNotifyError {
    /// Linux without `notify-send` on `PATH`.
    NotifySendMissing,
    /// Windows.
    Windows,
    /// Any other platform; the name of the OS.
    Unsupported(String),
}

impl fmt::Display for OsNotifyError {
    fn fmt(&self, f: &mut fmt::Formatter<'_>) -> fmt::Result {
        match self {
            OsNotifyError::NotifySendMissing => f.write_str("osnotify: notify-send not on PATH"),
            OsNotifyError::Windows => f.write_str("osnotify: not supported on windows in MVP"),
            OsNotifyError::Unsupported(os) => write!(f, "osnotify: unsupported platform {os:?}"),
        }
    }
}

impl std::error::Error for OsNotifyError {}

/// Runs a command to completion. The seam a test replaces; the default
/// ([`ExecRunner`]) spawns a process.
pub trait Runner: Send + Sync {
    /// Runs `name args...`, giving up when `ctx` is done where it can.
    fn run(&self, ctx: &Context, name: &str, args: &[String]) -> io::Result<()>;
}

/// Spawns the command and waits, killing it if `ctx` ends first. Go's
/// `execRunner` over `exec.CommandContext`.
#[derive(Clone, Copy, Debug, Default)]
pub struct ExecRunner;

impl Runner for ExecRunner {
    fn run(&self, ctx: &Context, name: &str, args: &[String]) -> io::Result<()> {
        if let Some(e) = ctx.err() {
            return Err(io::Error::other(e));
        }
        let mut child = Command::new(name)
            .args(args)
            .stdin(Stdio::null())
            .stdout(Stdio::null())
            .stderr(Stdio::null())
            .spawn()?;
        loop {
            if let Some(status) = child.try_wait()? {
                return if status.success() {
                    Ok(())
                } else {
                    Err(io::Error::other(format!("exit status: {status}")))
                };
            }
            if let Some(e) = ctx.err() {
                // The child may have exited since the poll.
                child.kill().ok();
                child.wait().ok();
                return Err(io::Error::other(e));
            }
            std::thread::sleep(Duration::from_millis(10));
        }
    }
}

/// Delivers an event as a desktop notification. Go's
/// `osnotifysink.Sink`.
pub struct OsNotifySink {
    platform: Platform,
    runner: Box<dyn Runner>,
    title: Option<Box<dyn Template>>,
    text: Option<Box<dyn Template>>,
    redactor: Option<Arc<Redactor>>,
    breaker: Option<Arc<Breaker>>,
}

impl OsNotifySink {
    /// A sink for the host platform: Go's `New`. On Linux `notify-send`
    /// is probed once, here; one installed later is not picked up.
    pub fn new() -> Result<Self, OsNotifyError> {
        let platform = match std::env::consts::OS {
            "macos" => Platform::Darwin,
            "linux" => {
                if !on_path("notify-send") {
                    return Err(OsNotifyError::NotifySendMissing);
                }
                Platform::Linux
            }
            "windows" => return Err(OsNotifyError::Windows),
            other => return Err(OsNotifyError::Unsupported(other.to_string())),
        };
        Ok(Self::for_platform(platform))
    }

    /// A sink for `platform` without probing the host: for tests and for
    /// building the command of another platform.
    pub fn for_platform(platform: Platform) -> Self {
        OsNotifySink {
            platform,
            runner: Box::new(ExecRunner),
            title: None,
            text: None,
            redactor: None,
            breaker: None,
        }
    }

    /// The title template. Required: `drain` fails without it, as Go's
    /// does.
    pub fn title(mut self, t: impl Template + 'static) -> Self {
        self.title = Some(Box::new(t));
        self
    }

    /// The body text template. Required.
    pub fn text(mut self, t: impl Template + 'static) -> Self {
        self.text = Some(Box::new(t));
        self
    }

    /// Runs the rendered title and text through `r`: Go's `WithRedactor`.
    pub fn redactor(mut self, r: Arc<Redactor>) -> Self {
        self.redactor = Some(r);
        self
    }

    /// Gates the runner on `b`: Go's `WithBreaker`. An open circuit
    /// returns [`BROKEN_CIRCUIT`](super::BROKEN_CIRCUIT) as the whole
    /// message and the runner is not called.
    pub fn breaker(mut self, b: Arc<Breaker>) -> Self {
        self.breaker = Some(b);
        self
    }

    /// Replaces the process runner; Go's unexported `withRunner`.
    pub fn runner(mut self, r: impl Runner + 'static) -> Self {
        self.runner = Box::new(r);
        self
    }

    /// The command and arguments for a rendered `title` and `text`.
    pub fn command(&self, title: &str, text: &str) -> (String, Vec<String>) {
        match self.platform {
            // `display notification` takes the body first, the title last.
            Platform::Darwin => (
                "osascript".to_string(),
                vec![
                    "-e".to_string(),
                    format!(
                        "display notification {} with title {}",
                        escape_applescript(text),
                        escape_applescript(title)
                    ),
                ],
            ),
            Platform::Linux => (
                "notify-send".to_string(),
                vec![title.to_string(), text.to_string()],
            ),
        }
    }
}

impl Sink for OsNotifySink {
    fn drain(&self, ctx: &Context, event: &Event) -> Result<(), BusError> {
        let (Some(title_t), Some(text_t)) = (&self.title, &self.text) else {
            return Err(BusError::handler(
                "osnotify: title and text templates required",
            ));
        };
        let mut title = title_t
            .render(event)
            .map_err(|e| BusError::handler(format!("osnotify: render title: {e}")))?;
        let mut text = text_t
            .render(event)
            .map_err(|e| BusError::handler(format!("osnotify: render text: {e}")))?;
        if let Some(r) = &self.redactor {
            title = r.apply(&title).into_owned();
            text = r.apply(&text).into_owned();
        }
        let (name, args) = self.command(&title, &text);
        let call = || {
            self.runner
                .run(ctx, &name, &args)
                .map_err(|e| BusError::handler(format!("osnotify: run {name}: {e}")))
        };
        let Some(b) = &self.breaker else {
            return call();
        };
        if let Err(e) = b.allow() {
            return Err(BusError::handler(e.to_string()));
        }
        let out = call();
        b.record(out.is_ok(), 0);
        out
    }

    /// A no-op: the sink owns no long-lived resource.
    fn close(&self) -> Result<(), BusError> {
        Ok(())
    }
}

/// `s` as an AppleScript string literal: double quotes around it, `"`
/// and `\` escaped. Everything else passes through.
pub fn escape_applescript(s: &str) -> String {
    let mut out = String::with_capacity(s.len() + 2);
    out.push('"');
    for c in s.chars() {
        if matches!(c, '"' | '\\') {
            out.push('\\');
        }
        out.push(c);
    }
    out.push('"');
    out
}

/// Whether an executable `name` is in a `PATH` directory.
fn on_path(name: &str) -> bool {
    let Some(path) = std::env::var_os("PATH") else {
        return false;
    };
    std::env::split_paths(&path).any(|dir| is_executable(&dir.join(name)))
}

#[cfg(unix)]
fn is_executable(p: &Path) -> bool {
    use std::os::unix::fs::PermissionsExt;
    p.metadata()
        .is_ok_and(|m| m.is_file() && m.permissions().mode() & 0o111 != 0)
}

#[cfg(not(unix))]
fn is_executable(p: &Path) -> bool {
    p.is_file()
}
