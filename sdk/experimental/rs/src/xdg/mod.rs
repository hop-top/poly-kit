//! Per-user base directories, as Go `go/core/xdg` resolves them through
//! github.com/adrg/xdg. Only what the config loader needs is ported:
//! the home directory and the config home.
//!
//! Resolution reads the environment on every call, like Go's, so a
//! changed `XDG_CONFIG_HOME` is seen on the next call.
//!
//! - home: `$HOME`, else `/` (Unix); `%USERPROFILE%` on Windows.
//! - config home: `$XDG_CONFIG_HOME` when it is absolute (a leading `~`
//!   or `$HOME` expands), else the platform default:
//!   `~/Library/Application Support` (macOS), `%LOCALAPPDATA%`
//!   (Windows), `~/.config` (other Unix).
//!
//! Gated behind `config`, off wasm32.

use std::path::{Path, PathBuf};

fn env(name: &str) -> String {
    std::env::var(name).unwrap_or_default()
}

/// The current user's home directory.
pub fn home() -> PathBuf {
    #[cfg(windows)]
    {
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

/// adrg/xdg `ExpandHome`: `~` or `$HOME` at the start.
fn expand_home(p: &str) -> PathBuf {
    let home = home();
    if let Some(rest) = p.strip_prefix('~') {
        return home.join(rest.trim_start_matches(['/', '\\']));
    }
    if let Some(rest) = p.strip_prefix("$HOME") {
        return home.join(rest.trim_start_matches(['/', '\\']));
    }
    PathBuf::from(p)
}

/// adrg/xdg `EnvPath`: the variable when it is an absolute path, else
/// the fallback.
fn env_path(name: &str, fallback: impl FnOnce() -> PathBuf) -> PathBuf {
    let v = env(name);
    if !v.is_empty() {
        let p = expand_home(&v);
        if p.is_absolute() {
            return p;
        }
    }
    fallback()
}

fn default_config_home(home: &Path) -> PathBuf {
    if cfg!(target_os = "macos") {
        home.join("Library").join("Application Support")
    } else if cfg!(windows) {
        let local = env("LOCALAPPDATA");
        if local.is_empty() {
            home.join("AppData").join("Local")
        } else {
            PathBuf::from(local)
        }
    } else {
        home.join(".config")
    }
}

/// `$XDG_CONFIG_HOME` or the platform default.
pub fn config_home() -> PathBuf {
    env_path("XDG_CONFIG_HOME", || default_config_home(&home()))
}

/// `<config home>/<tool>`, for building paths (Go `RawConfigDir`; no
/// guard runs here).
pub fn raw_config_dir(tool: &str) -> PathBuf {
    config_home().join(tool)
}
