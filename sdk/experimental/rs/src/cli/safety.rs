//! Factor 10, delegation safety: guards risky operations behind
//! `--force`, stricter off a terminal where a scripted caller or an
//! agent cannot answer a prompt. Ports the TypeScript `safety.ts` and
//! Python `safety.py` guard. (Go's `SafetyGuard` is deprecated there in
//! favor of the `--confirm` gate, which this crate has in `gate.rs`;
//! use that for command-tree enforcement and this for a standalone
//! check inside a handler.)

use std::fmt;
use std::io::IsTerminal;
use std::str::FromStr;

use crate::output::CliError;

/// How risky an operation is.
#[derive(Clone, Copy, Debug, PartialEq, Eq)]
pub enum SafetyLevel {
    /// Always allowed.
    Read,
    /// Allowed on a terminal; needs `--force` off one.
    Caution,
    /// Always needs `--force`.
    Dangerous,
}

impl SafetyLevel {
    /// The level's name: `read`, `caution` or `dangerous`.
    pub fn as_str(self) -> &'static str {
        match self {
            SafetyLevel::Read => "read",
            SafetyLevel::Caution => "caution",
            SafetyLevel::Dangerous => "dangerous",
        }
    }
}

impl fmt::Display for SafetyLevel {
    fn fmt(&self, f: &mut fmt::Formatter<'_>) -> fmt::Result {
        f.write_str(self.as_str())
    }
}

impl FromStr for SafetyLevel {
    type Err = String;

    fn from_str(s: &str) -> Result<SafetyLevel, String> {
        match s {
            "read" => Ok(SafetyLevel::Read),
            "caution" => Ok(SafetyLevel::Caution),
            "dangerous" => Ok(SafetyLevel::Dangerous),
            other => Err(format!("unknown safety level {other:?}")),
        }
    }
}

/// What the guard decides on.
#[derive(Clone, Copy, Debug, Default, PartialEq, Eq)]
pub struct SafetyOpts {
    /// `--force` was given.
    pub force: bool,
    /// The caller is on a terminal.
    pub tty: bool,
}

impl SafetyOpts {
    /// `force` with the terminal check made on the process's stdout.
    pub fn detect(force: bool) -> SafetyOpts {
        SafetyOpts {
            force,
            tty: std::io::stdout().is_terminal(),
        }
    }
}

/// A refused operation; the message names `--force`.
#[derive(Clone, Debug, PartialEq, Eq)]
pub struct SafetyError {
    level: SafetyLevel,
}

impl SafetyError {
    /// The refused level.
    pub fn level(&self) -> SafetyLevel {
        self.level
    }
}

impl fmt::Display for SafetyError {
    fn fmt(&self, f: &mut fmt::Formatter<'_>) -> fmt::Result {
        match self.level {
            SafetyLevel::Caution => {
                f.write_str("caution-level operation in non-TTY requires --force")
            }
            _ => write!(f, "{} operation requires --force", self.level),
        }
    }
}

impl std::error::Error for SafetyError {}

impl From<SafetyError> for CliError {
    fn from(e: SafetyError) -> CliError {
        CliError::unauthorized(e.to_string())
    }
}

/// Whether the context permits `level`.
///
/// - `read`: always.
/// - `caution`: on a terminal; off one only with `--force`.
/// - `dangerous`: only with `--force`.
pub fn safety_guard(level: SafetyLevel, opts: SafetyOpts) -> Result<(), SafetyError> {
    let refused = match level {
        SafetyLevel::Read => false,
        SafetyLevel::Caution => !opts.tty && !opts.force,
        SafetyLevel::Dangerous => !opts.force,
    };
    if refused {
        Err(SafetyError { level })
    } else {
        Ok(())
    }
}
