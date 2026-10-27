//! Per-user base directories, as Go `go/core/xdg` resolves them through
//! github.com/adrg/xdg (v0.5.3). Only what kit-rs needs is ported: the
//! home directory, the config home, the data home and the state home.
//! Every config, data or state path in the crate (config, consent,
//! telemetry, upgrade) resolves here.
//!
//! Resolution reads the environment on every call, like Go's, so a
//! changed `XDG_CONFIG_HOME` is seen on the next call.
//!
//! - home: `$HOME`, else `/` (Unix); `%USERPROFILE%` on Windows.
//! - config / state home: `$XDG_CONFIG_HOME` / `$XDG_STATE_HOME` when
//!   absolute (a leading `~` or `$HOME` expands), else the platform
//!   default: `~/Library/Application Support` for both (macOS),
//!   `%LOCALAPPDATA%` for both (Windows), `~/.config` and
//!   `~/.local/state` (other Unix).
//! - data home: `$XDG_DATA_HOME` under the same rule, else
//!   `~/Library/Application Support` (macOS), `%LOCALAPPDATA%`
//!   (Windows), `~/.local/share` (other Unix).
//! - legacy homes: where kit-rs telemetry looked before it resolved
//!   here (`~/.config`, `~/.local/state`), when that differs from the
//!   home above. Readers migrate a file there once (moved, not copied)
//!   and otherwise count it only as a deny, never as a grant.
//!
//! The resolvers take the OS and the env lookup, so every branch is
//! testable on any host. Gated behind `config`, `telemetry` or
//! `upgrade`, off wasm32.

use std::path::{Path, PathBuf};

/// The platform families adrg/xdg distinguishes.
#[derive(Clone, Copy, Debug, PartialEq, Eq)]
pub enum Os {
    Macos,
    Windows,
    /// Linux, the BSDs and every other Unix.
    Unix,
}

impl Os {
    /// The host.
    pub fn current() -> Os {
        if cfg!(target_os = "macos") {
            Os::Macos
        } else if cfg!(windows) {
            Os::Windows
        } else {
            Os::Unix
        }
    }
}

type Env<'a> = &'a dyn Fn(&str) -> String;

fn os_env(name: &str) -> String {
    std::env::var(name).unwrap_or_default()
}

fn home_for(os: Os, env: Env) -> PathBuf {
    if os == Os::Windows {
        let h = env("USERPROFILE");
        if !h.is_empty() {
            return PathBuf::from(h);
        }
    }
    let h = env("HOME");
    if h.is_empty() {
        PathBuf::from("/")
    } else {
        PathBuf::from(h)
    }
}

/// adrg/xdg `ExpandHome`: `~` or `$HOME` at the start; on Windows
/// `%USERPROFILE%` only.
fn expand_home(p: &str, home: &Path, os: Os) -> PathBuf {
    if os == Os::Windows {
        return match p.strip_prefix("%USERPROFILE%") {
            Some(rest) => home.join(rest.trim_start_matches(['/', '\\'])),
            None => PathBuf::from(p),
        };
    }
    if let Some(rest) = p.strip_prefix('~') {
        return home.join(rest.trim_start_matches(['/', '\\']));
    }
    if let Some(rest) = p.strip_prefix("$HOME") {
        return home.join(rest.trim_start_matches(['/', '\\']));
    }
    PathBuf::from(p)
}

/// adrg/xdg `EnvPath`: the variable when it is an absolute path.
fn env_path(name: &str, os: Os, env: Env) -> Option<PathBuf> {
    let v = env(name);
    if v.is_empty() {
        return None;
    }
    Some(expand_home(&v, &home_for(os, env), os)).filter(|p| is_absolute(p, os))
}

/// `Path::is_absolute` for the target OS, not the host.
fn is_absolute(p: &Path, os: Os) -> bool {
    let s = p.to_string_lossy();
    match os {
        Os::Windows => {
            let b = s.as_bytes();
            s.starts_with("\\\\") || (b.len() >= 3 && b[1] == b':' && matches!(b[2], b'\\' | b'/'))
        }
        _ => s.starts_with('/'),
    }
}

/// The platform default when the variable is unset; `unix` is the path
/// under home on other Unix.
fn platform_home(os: Os, env: Env, unix: &[&str]) -> PathBuf {
    let home = home_for(os, env);
    match os {
        Os::Macos => home.join("Library").join("Application Support"),
        Os::Windows => {
            let local = env("LOCALAPPDATA");
            if local.is_empty() {
                home.join("AppData").join("Local")
            } else {
                PathBuf::from(local)
            }
        }
        Os::Unix => unix.iter().fold(home, |p, s| p.join(s)),
    }
}

pub(crate) fn config_home_for(os: Os, env: Env) -> PathBuf {
    env_path("XDG_CONFIG_HOME", os, env).unwrap_or_else(|| platform_home(os, env, &[".config"]))
}

pub(crate) fn state_home_for(os: Os, env: Env) -> PathBuf {
    env_path("XDG_STATE_HOME", os, env)
        .unwrap_or_else(|| platform_home(os, env, &[".local", "state"]))
}

pub(crate) fn data_home_for(os: Os, env: Env) -> PathBuf {
    env_path("XDG_DATA_HOME", os, env)
        .unwrap_or_else(|| platform_home(os, env, &[".local", "share"]))
}

/// The pre-xdg kit-rs location, when it differs from `current`.
fn legacy(os: Os, env: Env, var: &str, unix: &[&str], current: PathBuf) -> Option<PathBuf> {
    if env_path(var, os, env).is_some() {
        return None;
    }
    let old = unix.iter().fold(home_for(os, env), |p, s| p.join(s));
    (old != current).then_some(old)
}

pub(crate) fn legacy_config_home_for(os: Os, env: Env) -> Option<PathBuf> {
    legacy(
        os,
        env,
        "XDG_CONFIG_HOME",
        &[".config"],
        config_home_for(os, env),
    )
}

pub(crate) fn legacy_state_home_for(os: Os, env: Env) -> Option<PathBuf> {
    legacy(
        os,
        env,
        "XDG_STATE_HOME",
        &[".local", "state"],
        state_home_for(os, env),
    )
}

/// The current user's home directory.
pub fn home() -> PathBuf {
    home_for(Os::current(), &os_env)
}

/// `$XDG_CONFIG_HOME` or the platform default.
pub fn config_home() -> PathBuf {
    config_home_for(Os::current(), &os_env)
}

/// `$XDG_STATE_HOME` or the platform default.
pub fn state_home() -> PathBuf {
    state_home_for(Os::current(), &os_env)
}

/// `~/.config` when no XDG variable is set and the platform default is
/// elsewhere (macOS, Windows): where kit-rs telemetry used to read.
pub fn legacy_config_home() -> Option<PathBuf> {
    legacy_config_home_for(Os::current(), &os_env)
}

/// `~/.local/state` likewise.
pub fn legacy_state_home() -> Option<PathBuf> {
    legacy_state_home_for(Os::current(), &os_env)
}

/// `<config home>/<tool>`, for building paths (Go `RawConfigDir`; no
/// guard runs here).
pub fn raw_config_dir(tool: &str) -> PathBuf {
    config_home().join(tool)
}

/// `$XDG_DATA_HOME` or the platform default.
pub fn data_home() -> PathBuf {
    data_home_for(Os::current(), &os_env)
}

/// `<data home>/<tool>`, for building paths (Go `RawDataDir`; no guard
/// runs here).
pub fn raw_data_dir(tool: &str) -> PathBuf {
    data_home().join(tool)
}

/// `<state home>/<tool>` (Go `RawStateDir`; no guard runs here).
pub fn raw_state_dir(tool: &str) -> PathBuf {
    state_home().join(tool)
}

#[cfg(test)]
mod tests {
    use super::*;

    fn env_of(pairs: &'static [(&'static str, &'static str)]) -> impl Fn(&str) -> String {
        move |k| {
            pairs
                .iter()
                .find(|(n, _)| *n == k)
                .map(|(_, v)| v.to_string())
                .unwrap_or_default()
        }
    }

    const HOME: &[(&str, &str)] = &[
        ("HOME", "/h"),
        ("USERPROFILE", "/w"),
        ("LOCALAPPDATA", "/w/L"),
    ];

    #[test]
    fn macos_uses_application_support_for_config_and_state() {
        let e = env_of(HOME);
        let app = PathBuf::from("/h/Library/Application Support");
        assert_eq!(config_home_for(Os::Macos, &e), app);
        assert_eq!(state_home_for(Os::Macos, &e), app);
        assert_eq!(
            legacy_config_home_for(Os::Macos, &e),
            Some(PathBuf::from("/h/.config"))
        );
        assert_eq!(
            legacy_state_home_for(Os::Macos, &e),
            Some(PathBuf::from("/h/.local/state"))
        );
    }

    #[test]
    fn unix_uses_dot_config_and_local_state_with_no_legacy() {
        let e = env_of(HOME);
        assert_eq!(config_home_for(Os::Unix, &e), PathBuf::from("/h/.config"));
        assert_eq!(
            state_home_for(Os::Unix, &e),
            PathBuf::from("/h/.local/state")
        );
        assert_eq!(legacy_config_home_for(Os::Unix, &e), None);
        assert_eq!(legacy_state_home_for(Os::Unix, &e), None);
    }

    #[test]
    fn windows_uses_local_app_data() {
        let e = env_of(HOME);
        assert_eq!(config_home_for(Os::Windows, &e), PathBuf::from("/w/L"));
        assert_eq!(state_home_for(Os::Windows, &e), PathBuf::from("/w/L"));
        assert_eq!(
            legacy_config_home_for(Os::Windows, &e),
            Some(PathBuf::from("/w/.config"))
        );
        let bare = env_of(&[("USERPROFILE", "/w")]);
        assert_eq!(
            config_home_for(Os::Windows, &bare),
            PathBuf::from("/w/AppData/Local")
        );
    }

    #[test]
    fn an_absolute_xdg_var_wins_everywhere_and_leaves_no_legacy() {
        let e = env_of(&[
            ("HOME", "/h"),
            ("XDG_CONFIG_HOME", "/x/c"),
            ("XDG_STATE_HOME", "~/s"),
        ]);
        for os in [Os::Macos, Os::Unix] {
            assert_eq!(config_home_for(os, &e), PathBuf::from("/x/c"));
            assert_eq!(legacy_config_home_for(os, &e), None);
        }
        assert_eq!(state_home_for(Os::Macos, &e), PathBuf::from("/h/s"));
    }

    #[test]
    fn windows_needs_a_volume_and_expands_only_userprofile() {
        let e = env_of(&[
            ("USERPROFILE", "C:\\u"),
            ("LOCALAPPDATA", "C:\\u\\L"),
            ("XDG_CONFIG_HOME", "/x/c"),
            ("XDG_STATE_HOME", "~/s"),
        ]);
        assert_eq!(config_home_for(Os::Windows, &e), PathBuf::from("C:\\u\\L"));
        assert_eq!(state_home_for(Os::Windows, &e), PathBuf::from("C:\\u\\L"));
        let e = env_of(&[("USERPROFILE", "C:\\u"), ("XDG_CONFIG_HOME", "D:\\c")]);
        assert_eq!(config_home_for(Os::Windows, &e), PathBuf::from("D:\\c"));
    }

    #[test]
    fn a_relative_xdg_var_is_ignored() {
        let e = env_of(&[("HOME", "/h"), ("XDG_CONFIG_HOME", "rel/c")]);
        assert_eq!(
            config_home_for(Os::Macos, &e),
            PathBuf::from("/h/Library/Application Support")
        );
        assert_eq!(config_home_for(Os::Unix, &e), PathBuf::from("/h/.config"));
    }
}
