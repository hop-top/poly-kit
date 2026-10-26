//! Enforcement mode from string, environment and configuration.
//! Mirrors Go `go/runtime/bus/config.go`.
//!
//! Precedence for [`mode_from_config`]: the [`CONFIG_KEY_ENFORCE`] value
//! when present and parseable, then [`ENV_ENFORCE`] when set and
//! parseable, then [`Mode::Warn`]. An unparseable value at any layer
//! falls through to the next rather than failing.
//!
//! Environment access is injectable (the `_with` variants) so callers
//! and tests need no process-global state. On `wasm32` there is no
//! process environment; the real reader always yields [`Mode::Warn`].

use std::collections::{BTreeMap, HashMap};
use std::fmt;
use std::str::FromStr;

use super::mem::{BusBuilder, Mode};
use super::quote::quote;

/// Environment variable that overrides the enforcement mode. Values
/// (case-insensitive): `off`, `warn`, `strict`; anything else is ignored.
pub const ENV_ENFORCE: &str = "KIT_BUS_ENFORCE";

/// Configuration key the bus reads its enforcement mode from.
pub const CONFIG_KEY_ENFORCE: &str = "kit.bus.enforce";

/// Minimal configuration source: the bus does not depend on a config
/// crate, callers thread a getter. Maps implement it.
pub trait ConfigGetter {
    /// Returns the value for `key`, if present.
    fn get(&self, key: &str) -> Option<String>;
}

impl ConfigGetter for HashMap<String, String> {
    fn get(&self, key: &str) -> Option<String> {
        HashMap::get(self, key).cloned()
    }
}

impl ConfigGetter for BTreeMap<String, String> {
    fn get(&self, key: &str) -> Option<String> {
        BTreeMap::get(self, key).cloned()
    }
}

/// Error from parsing an unknown enforcement mode. Holds the original,
/// untrimmed input.
#[derive(Clone, Debug, PartialEq, Eq)]
pub struct ParseModeError(String);

impl ParseModeError {
    /// The input that failed to parse, as given.
    pub fn input(&self) -> &str {
        &self.0
    }
}

impl fmt::Display for ParseModeError {
    fn fmt(&self, f: &mut fmt::Formatter<'_>) -> fmt::Result {
        write!(
            f,
            "bus: unknown enforce mode {} (want off|warn|strict)",
            quote(&self.0)
        )
    }
}

impl std::error::Error for ParseModeError {}

/// Parses `off`, `warn` or `strict`: case-insensitive, surrounding
/// whitespace allowed, empty (or all whitespace) is [`Mode::Warn`].
/// Go's `ModeFromString` also returns `Warn` alongside the error;
/// callers wanting that use `unwrap_or_default()`.
impl FromStr for Mode {
    type Err = ParseModeError;

    fn from_str(s: &str) -> Result<Self, Self::Err> {
        let t = s.trim();
        if t.is_empty() {
            return Ok(Mode::Warn);
        }
        match t.to_lowercase().as_str() {
            "off" => Ok(Mode::Off),
            "warn" => Ok(Mode::Warn),
            "strict" => Ok(Mode::Strict),
            _ => Err(ParseModeError(s.to_string())),
        }
    }
}

/// Go-shaped [`Mode::from_str`]: the parsed mode, or [`Mode::Warn`] together
/// with the error for an unknown value.
pub fn mode_from_string(s: &str) -> (Mode, Option<ParseModeError>) {
    match s.parse() {
        Ok(m) => (m, None),
        Err(e) => (Mode::Warn, Some(e)),
    }
}

/// Reads [`ENV_ENFORCE`] from the process environment. Unset, empty,
/// unreadable or unrecognised yields [`Mode::Warn`]; always `Warn` on
/// `wasm32`.
pub fn mode_from_env() -> Mode {
    mode_from_env_with(process_env)
}

/// [`mode_from_env`] over an injected environment reader.
pub fn mode_from_env_with(env: impl Fn(&str) -> Option<String>) -> Mode {
    env(ENV_ENFORCE)
        .and_then(|v| v.parse().ok())
        .unwrap_or_default()
}

/// Resolves the mode: config value, then environment, then
/// [`Mode::Warn`]. `None` means no config source.
pub fn mode_from_config(getter: Option<&dyn ConfigGetter>) -> Mode {
    mode_from_config_with(getter, process_env)
}

/// [`mode_from_config`] over an injected environment reader.
pub fn mode_from_config_with(
    getter: Option<&dyn ConfigGetter>,
    env: impl Fn(&str) -> Option<String>,
) -> Mode {
    if let Some(m) = getter
        .and_then(|g| g.get(CONFIG_KEY_ENFORCE))
        .and_then(|v| v.parse().ok())
    {
        return m;
    }
    mode_from_env_with(env)
}

#[cfg(not(target_arch = "wasm32"))]
fn process_env(key: &str) -> Option<String> {
    std::env::var_os(key).and_then(|v| v.into_string().ok())
}

#[cfg(target_arch = "wasm32")]
fn process_env(_key: &str) -> Option<String> {
    None
}

/// Options that pick the enforcement mode from the environment or
/// configuration. They set the same field as [`BusBuilder::enforce`], so
/// the last call wins: an explicit `enforce(..)` after
/// `enforce_from_env()` overrides it, as in Go.
impl BusBuilder {
    /// Sets the mode from [`ENV_ENFORCE`] ([`mode_from_env`]).
    pub fn enforce_from_env(self) -> Self {
        self.enforce(mode_from_env())
    }

    /// [`enforce_from_env`](Self::enforce_from_env) over an injected
    /// environment reader.
    pub fn enforce_from_env_with(self, env: impl Fn(&str) -> Option<String>) -> Self {
        self.enforce(mode_from_env_with(env))
    }

    /// Sets the mode from config then environment ([`mode_from_config`]).
    pub fn enforce_from_config(self, getter: Option<&dyn ConfigGetter>) -> Self {
        self.enforce(mode_from_config(getter))
    }

    /// [`enforce_from_config`](Self::enforce_from_config) over an
    /// injected environment reader.
    pub fn enforce_from_config_with(
        self,
        getter: Option<&dyn ConfigGetter>,
        env: impl Fn(&str) -> Option<String>,
    ) -> Self {
        self.enforce(mode_from_config_with(getter, env))
    }
}
