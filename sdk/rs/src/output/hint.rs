//! Contextual next-step hints: a registry mapping command names to
//! hints, and the rendering rules. Ports Go `console/output/hint.go`.
//!
//! Hints guide users and agents to the logical next action. They are
//! suppressed when output is machine-formatted (anything but `table`),
//! not written to a terminal, or disabled by flag, config or
//! `HOP_QUIET_HINTS`.

use std::collections::HashMap;
use std::io::{self, Write};
use std::sync::{Arc, Mutex};

type Condition = Arc<dyn Fn() -> bool + Send + Sync>;

/// A next-step suggestion attached to a command.
#[derive(Clone)]
pub struct Hint {
    /// The text, e.g. ``Run `hop version` to verify.``
    pub message: String,
    condition: Option<Condition>,
}

impl std::fmt::Debug for Hint {
    fn fmt(&self, f: &mut std::fmt::Formatter<'_>) -> std::fmt::Result {
        f.debug_struct("Hint")
            .field("message", &self.message)
            .field("conditional", &self.condition.is_some())
            .finish()
    }
}

impl Hint {
    /// A hint that always applies.
    pub fn new(message: impl Into<String>) -> Hint {
        Hint {
            message: message.into(),
            condition: None,
        }
    }

    /// Applies only while `condition` returns true.
    pub fn when(mut self, condition: impl Fn() -> bool + Send + Sync + 'static) -> Hint {
        self.condition = Some(Arc::new(condition));
        self
    }

    fn applies(&self) -> bool {
        self.condition.as_ref().is_none_or(|c| c())
    }
}

/// A concurrency-safe registry of hints by command name.
#[derive(Debug, Default)]
pub struct HintSet {
    m: Mutex<HashMap<String, Vec<Hint>>>,
}

impl HintSet {
    /// An empty registry.
    pub fn new() -> HintSet {
        HintSet::default()
    }

    /// Adds hints for `cmd`.
    pub fn register(&self, cmd: &str, hints: impl IntoIterator<Item = Hint>) {
        self.m
            .lock()
            .unwrap_or_else(|e| e.into_inner())
            .entry(cmd.to_string())
            .or_default()
            .extend(hints);
    }

    /// A copy of the hints registered for `cmd`; empty when none are.
    pub fn lookup(&self, cmd: &str) -> Vec<Hint> {
        self.m
            .lock()
            .unwrap_or_else(|e| e.into_inner())
            .get(cmd)
            .cloned()
            .unwrap_or_default()
    }
}

/// The hints whose condition is absent or true.
pub fn active(hints: &[Hint]) -> Vec<Hint> {
    hints.iter().filter(|h| h.applies()).cloned().collect()
}

/// What decides whether hints render (Go reads these from viper).
#[derive(Clone, Copy, Debug, Default, PartialEq, Eq)]
pub struct HintOptions {
    /// `--no-hints`.
    pub no_hints: bool,
    /// `--quiet`.
    pub quiet: bool,
    /// The `hints.enabled` config key; `None` when unset.
    pub config_enabled: Option<bool>,
}

/// Whether hints should render: not under `--no-hints`, `--quiet`,
/// `hints.enabled: false`, or `HOP_QUIET_HINTS` set to `1`, `true` or
/// `yes`.
pub fn hints_enabled(opts: &HintOptions) -> bool {
    if opts.no_hints || opts.quiet || opts.config_enabled == Some(false) {
        return false;
    }
    !matches!(
        std::env::var("HOP_QUIET_HINTS").as_deref(),
        Ok("1" | "true" | "yes")
    )
}

/// Where and how hints are drawn.
#[derive(Clone, Debug, PartialEq, Eq)]
pub struct HintStyle {
    /// Whether the destination is a terminal; hints are for humans and
    /// render nowhere else.
    pub tty: bool,
    /// Draw without ANSI color (`--no-color`).
    pub no_color: bool,
    /// The muted color of the arrow and text, `#RRGGBB`.
    pub muted: String,
}

impl HintStyle {
    /// A style for stdout: the terminal check is made on the process's
    /// stdout, `NO_COLOR` is honored along with `no_color`, and the
    /// muted color is the theme's.
    pub fn stdout(no_color: bool) -> HintStyle {
        use std::io::IsTerminal;
        HintStyle {
            tty: io::stdout().is_terminal(),
            no_color: no_color || std::env::var_os("NO_COLOR").is_some_and(|v| !v.is_empty()),
            muted: "#858183".into(),
        }
    }
}

/// Writes the active hints to `w`, one `→ message` line each after a
/// blank line, dimmed. A no-op unless `format` is `table`, the style
/// says terminal, hints are enabled and something is active.
pub fn render_hints(
    w: &mut dyn Write,
    hints: &[Hint],
    format: &str,
    opts: &HintOptions,
    style: &HintStyle,
) -> io::Result<()> {
    if format != "table" || !style.tty || !hints_enabled(opts) {
        return Ok(());
    }
    let shown = active(hints);
    if shown.is_empty() {
        return Ok(());
    }
    writeln!(w)?;
    let color = (!style.no_color).then(|| sgr(&style.muted)).flatten();
    for h in shown {
        match &color {
            Some(c) => writeln!(w, "{c}→ {}\x1b[0m", h.message)?,
            None => writeln!(w, "→ {}", h.message)?,
        }
    }
    Ok(())
}

fn sgr(hex: &str) -> Option<String> {
    let h = hex
        .strip_prefix('#')
        .filter(|h| h.len() == 6 && h.is_ascii())?;
    let c = |i: usize| u8::from_str_radix(&h[i..i + 2], 16).ok();
    Some(format!("\x1b[38;2;{};{};{}m", c(0)?, c(2)?, c(4)?))
}

/// The standard `upgrade` hint, ``Run `<binary> version` to verify.``,
/// active once `upgraded` returns true.
pub fn register_upgrade_hints(
    set: &HintSet,
    binary: &str,
    upgraded: impl Fn() -> bool + Send + Sync + 'static,
) {
    set.register(
        "upgrade",
        [Hint::new(format!("Run `{binary} version` to verify.")).when(upgraded)],
    );
}

/// The standard `version` hint, ``Run `<binary> upgrade` to get
/// latest.``, active while `update_available` returns true.
pub fn register_version_hints(
    set: &HintSet,
    binary: &str,
    update_available: impl Fn() -> bool + Send + Sync + 'static,
) {
    set.register(
        "version",
        [Hint::new(format!("Run `{binary} upgrade` to get latest.")).when(update_available)],
    );
}
