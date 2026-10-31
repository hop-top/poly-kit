//! The logger of a kit root: levels from the `-V` count, the `--quiet`
//! floor, themed `LEVEL msg key=val` lines on stderr. Ports Go
//! `console/log`. The `-V` count to level table and the quiet override
//! are the parity contract's `verbosity` block, so the vocabulary is
//! identical across ports.
//!
//! Output is JSON-free by design (as Go's default): one text line per
//! call, no time, pid or hostname.

use std::fmt::Display;
use std::io::{IsTerminal, Write};
use std::sync::Mutex;

use super::run::{Globals, Invocation};

/// A log level, ordered from the most verbose.
#[derive(Clone, Copy, Debug, PartialEq, Eq, PartialOrd, Ord)]
pub enum Level {
    /// High-volume diagnostics.
    Trace,
    /// Debugging detail.
    Debug,
    /// Routine progress.
    Info,
    /// Something to look at.
    Warn,
    /// A failure.
    Error,
    /// A failure that ends the process.
    Fatal,
}

impl Level {
    /// The contract's spelling: `trace|debug|info|warn|error|fatal`.
    pub fn name(self) -> &'static str {
        match self {
            Level::Trace => "trace",
            Level::Debug => "debug",
            Level::Info => "info",
            Level::Warn => "warn",
            Level::Error => "error",
            Level::Fatal => "fatal",
        }
    }

    fn from_name(s: &str) -> Option<Level> {
        Some(match s {
            "trace" => Level::Trace,
            "debug" => Level::Debug,
            "info" => Level::Info,
            "warn" => Level::Warn,
            "error" => Level::Error,
            "fatal" => Level::Fatal,
            _ => return None,
        })
    }

    /// The theme's prefix color and weight.
    fn style(self) -> (&'static str, bool) {
        match self {
            Level::Error | Level::Fatal => ("\x1b[38;2;255;56;139m", true),
            Level::Warn => ("\x1b[38;2;255;181;135m", true),
            Level::Info => ("\x1b[38;2;133;131;146m", false),
            Level::Debug | Level::Trace => ("\x1b[38;2;191;188;200m", false),
        }
    }
}

/// The contract's `verbosity` block: which level a `-V` count means,
/// and the level `--quiet` raises to.
#[derive(Clone, Debug, PartialEq, Eq)]
pub struct Verbosity {
    levels: Vec<(u32, Level)>,
    quiet_override: Level,
}

impl Verbosity {
    /// The table `contracts/parity/parity.json` declares: 0 info,
    /// 1 debug, 2 trace; quiet raises to warn.
    pub fn contract() -> Verbosity {
        Verbosity::new([("0", "info"), ("1", "debug"), ("2", "trace")], "warn")
    }

    /// A table from count/name pairs and a quiet override name. An
    /// unreadable entry is skipped; an unreadable override is `warn`.
    pub fn new<'a>(
        levels: impl IntoIterator<Item = (&'a str, &'a str)>,
        quiet_override: &str,
    ) -> Verbosity {
        let mut levels: Vec<(u32, Level)> = levels
            .into_iter()
            .filter_map(|(k, v)| Some((k.parse().ok()?, Level::from_name(v)?)))
            .collect();
        levels.sort_by_key(|(n, _)| *n);
        Verbosity {
            levels,
            quiet_override: Level::from_name(quiet_override).unwrap_or(Level::Warn),
        }
    }

    /// The level of a `-V` count: the highest declared count at or
    /// below it, clamped; info when the table is empty or `count` is
    /// below every key.
    pub fn level(&self, count: u8) -> Level {
        self.levels
            .iter()
            .rev()
            .find(|(n, _)| *n <= u32::from(count))
            .map_or(Level::Info, |(_, l)| *l)
    }

    /// The level `--quiet` raises to.
    pub fn quiet_override(&self) -> Level {
        self.quiet_override
    }
}

/// A leveled logger writing themed lines, to stderr by default.
pub struct Logger {
    level: Level,
    color: bool,
    out: Mutex<Box<dyn Write + Send>>,
}

impl std::fmt::Debug for Logger {
    fn fmt(&self, f: &mut std::fmt::Formatter<'_>) -> std::fmt::Result {
        f.debug_struct("Logger")
            .field("level", &self.level)
            .field("color", &self.color)
            .finish_non_exhaustive()
    }
}

impl Logger {
    /// A logger at the level of `globals.verbose`; `--quiet` raises it
    /// to the contract's override, never lowers it.
    pub fn from_globals(globals: &Globals) -> Logger {
        Logger::with_level(globals, Verbosity::contract().level(globals.verbose))
    }

    /// A logger at the level of an explicit `-V` count (Go
    /// `WithVerbose`).
    pub fn with_verbose(globals: &Globals, verbose: u8) -> Logger {
        Logger::with_level(globals, Verbosity::contract().level(verbose))
    }

    /// A logger at `level`, still honoring `--quiet` and `--no-color`
    /// (Go `WithLevel`).
    pub fn with_level(globals: &Globals, level: Level) -> Logger {
        let floor = Verbosity::contract().quiet_override();
        let level = if globals.quiet && level < floor {
            floor
        } else {
            level
        };
        let stderr = std::io::stderr();
        let color = !globals.no_color
            && !std::env::var_os("NO_COLOR").is_some_and(|v| !v.is_empty())
            && stderr.is_terminal();
        Logger {
            level,
            color,
            out: Mutex::new(Box::new(stderr)),
        }
    }

    /// Redirects the lines (a test sink, a file).
    pub fn with_output(mut self, out: impl Write + Send + 'static) -> Logger {
        self.out = Mutex::new(Box::new(out));
        self
    }

    /// Forces color on or off, whatever the destination.
    pub fn with_color(mut self, color: bool) -> Logger {
        self.color = color;
        self
    }

    /// The threshold in force.
    pub fn level(&self) -> Level {
        self.level
    }

    /// Whether lines carry ANSI color.
    pub fn color(&self) -> bool {
        self.color
    }

    /// Whether a line at `level` would be written.
    pub fn enabled(&self, level: Level) -> bool {
        level >= self.level
    }

    /// Logs at trace.
    pub fn trace(&self, msg: &str, kv: &[(&str, &dyn Display)]) {
        self.log(Level::Trace, "TRAC", msg, kv);
    }

    /// Logs at debug.
    pub fn debug(&self, msg: &str, kv: &[(&str, &dyn Display)]) {
        self.log(Level::Debug, "DEBU", msg, kv);
    }

    /// Logs at info.
    pub fn info(&self, msg: &str, kv: &[(&str, &dyn Display)]) {
        self.log(Level::Info, "INFO", msg, kv);
    }

    /// Logs at info with the `SUCC` label: filtered exactly like info,
    /// not a level of its own.
    pub fn success(&self, msg: &str, kv: &[(&str, &dyn Display)]) {
        self.log(Level::Info, "SUCC", msg, kv);
    }

    /// Logs at warn.
    pub fn warn(&self, msg: &str, kv: &[(&str, &dyn Display)]) {
        self.log(Level::Warn, "WARN", msg, kv);
    }

    /// Logs at error.
    pub fn error(&self, msg: &str, kv: &[(&str, &dyn Display)]) {
        self.log(Level::Error, "ERRO", msg, kv);
    }

    fn log(&self, level: Level, label: &str, msg: &str, kv: &[(&str, &dyn Display)]) {
        if !self.enabled(level) {
            return;
        }
        let mut line = self.prefix(level, label);
        line.push(' ');
        line.push_str(msg);
        for (k, v) in kv {
            line.push(' ');
            line.push_str(k);
            line.push('=');
            line.push_str(&quote(&v.to_string()));
        }
        line.push('\n');
        let mut out = self.out.lock().unwrap_or_else(|e| e.into_inner());
        let _ = out.write_all(line.as_bytes());
    }

    fn prefix(&self, level: Level, label: &str) -> String {
        if !self.color {
            return label.to_string();
        }
        if label == "SUCC" {
            return format!("\x1b[38;2;82;207;132m{label}\x1b[0m");
        }
        let (color, bold) = level.style();
        let b = if bold { "\x1b[1m" } else { "" };
        format!("{b}{color}{label}\x1b[0m")
    }
}

/// A value as logfmt writes it: quoted when empty or when it holds a
/// space, `=`, a quote or a control character.
fn quote(v: &str) -> String {
    if !v.is_empty()
        && !v
            .chars()
            .any(|c| c.is_whitespace() || c == '=' || c == '"' || c.is_control())
    {
        return v.to_string();
    }
    let mut out = String::with_capacity(v.len() + 2);
    out.push('"');
    for c in v.chars() {
        match c {
            '"' => out.push_str("\\\""),
            '\\' => out.push_str("\\\\"),
            '\n' => out.push_str("\\n"),
            '\t' => out.push_str("\\t"),
            c if c.is_control() => out.push_str(&format!("\\u{:04x}", c as u32)),
            c => out.push(c),
        }
    }
    out.push('"');
    out
}

impl Invocation {
    /// The raw `-V` count (Go `VerboseCount`): unchanged under
    /// `--quiet`, which the logger applies as a floor.
    pub fn verbose_count(&self) -> u8 {
        self.globals.verbose
    }

    /// Whether `--quiet` was given (Go `IsQuiet`).
    pub fn is_quiet(&self) -> bool {
        self.globals.quiet
    }

    /// The logger of this invocation (see [`Logger::from_globals`]).
    pub fn logger(&self) -> Logger {
        Logger::from_globals(&self.globals)
    }

    /// The inputs of hint enablement: `--no-hints` and `--quiet`.
    pub fn hint_options(&self) -> crate::output::hint::HintOptions {
        crate::output::hint::HintOptions {
            no_hints: self.globals.no_hints,
            quiet: self.globals.quiet,
            config_enabled: None,
        }
    }
}
