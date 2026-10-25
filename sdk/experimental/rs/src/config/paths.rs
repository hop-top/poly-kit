//! The config resolution chain and tool-scoped [`Options`]. Ports Go
//! `paths.go`, `paths_markers.go` and `options.go`.

use std::path::{Component, Path, PathBuf};

use super::nearest::DEFAULT_MAX_DEPTH;
use super::Options;
use crate::xdg;

/// One entry of the resolution chain.
#[derive(Clone, Debug, PartialEq, Eq)]
pub struct ResolvedPath {
    /// Absolute path consulted; `<defaults>` for the synthetic entry.
    pub path: PathBuf,
    /// `cwd`, `project`, `user`, `system` or `default`.
    pub source: String,
    /// User-facing scope label; may be empty.
    pub scope: String,
    /// A regular file exists at `path` (always true for `default`).
    pub exists: bool,
}

const DEFAULTS_SENTINEL: &str = "<defaults>";
const PATHS_TOOL: &str = "kit";
const PROJECT_MARKERS: [&str; 3] = [".kit/config.yaml", ".kit.yaml", "kit.yaml"];

/// Go `filepath.Abs`: joined to the working directory and cleaned
/// lexically (no symlink resolution).
pub(crate) fn go_abs(p: &Path) -> PathBuf {
    let joined = if p.is_absolute() {
        p.to_path_buf()
    } else {
        match std::env::current_dir() {
            Ok(cwd) => cwd.join(p),
            Err(_) => p.to_path_buf(),
        }
    };
    clean(&joined)
}

/// Go `filepath.Clean` for absolute paths: drops `.`, resolves `..`.
fn clean(p: &Path) -> PathBuf {
    let mut out = PathBuf::new();
    for c in p.components() {
        match c {
            Component::CurDir => {}
            Component::ParentDir => {
                if !out.pop() {
                    out.push(c);
                }
            }
            other => out.push(other),
        }
    }
    out
}

/// Go `filepath.Dir`: the root's parent is the root.
fn parent(p: &Path) -> PathBuf {
    p.parent()
        .map_or_else(|| p.to_path_buf(), Path::to_path_buf)
}

/// A regular file (symlinks followed).
pub(crate) fn file_exists(p: &Path) -> bool {
    !p.as_os_str().is_empty() && std::fs::metadata(p).is_ok_and(|m| m.is_file())
}

fn entry(path: PathBuf, source: &str) -> ResolvedPath {
    let exists = file_exists(&path);
    ResolvedPath {
        path,
        source: source.to_string(),
        scope: String::new(),
        exists,
    }
}

fn user_config_path(tool: &str) -> PathBuf {
    xdg::raw_config_dir(tool).join("config.yaml")
}

fn system_config_path(tool: &str) -> PathBuf {
    Path::new("/etc").join(tool).join("config.yaml")
}

fn chain(cwd: &Path, tool: &str, markers: &[&str]) -> Vec<ResolvedPath> {
    let abs = go_abs(cwd);
    let mut out: Vec<ResolvedPath> = markers.iter().map(|m| entry(abs.join(m), "cwd")).collect();
    let home = xdg::home();
    let mut dir = abs;
    for _ in 0..DEFAULT_MAX_DEPTH {
        let up = parent(&dir);
        if up == dir {
            break;
        }
        dir = up;
        if !home.as_os_str().is_empty() && dir == home {
            break;
        }
        out.extend(markers.iter().map(|m| entry(dir.join(m), "project")));
    }
    out.push(entry(user_config_path(tool), "user"));
    out.push(entry(system_config_path(tool), "system"));
    out.push(ResolvedPath {
        path: PathBuf::from(DEFAULTS_SENTINEL),
        source: "default".into(),
        scope: String::new(),
        exists: true,
    });
    out
}

/// The chain the loader would walk from `cwd`, highest precedence
/// first: cwd markers (`.kit/config.yaml`, `.kit.yaml`, `kit.yaml`),
/// the same markers in each parent up to (not including) `$HOME`, the
/// user file, `/etc/kit/config.yaml`, and a synthetic `default` entry.
/// Every entry is listed whether or not it exists.
pub fn paths(cwd: impl AsRef<Path>) -> Vec<ResolvedPath> {
    chain(cwd.as_ref(), PATHS_TOOL, &PROJECT_MARKERS)
}

/// [`paths`] with the user and system layers named for `tool`.
pub fn paths_for_tool(cwd: impl AsRef<Path>, tool: &str) -> Vec<ResolvedPath> {
    let tool = if tool.is_empty() { PATHS_TOOL } else { tool };
    chain(cwd.as_ref(), tool, &PROJECT_MARKERS)
}

/// [`paths_for_tool`] with custom project markers (relative paths, first
/// match wins per directory). Empty `markers` uses kit's defaults.
pub fn paths_for_tool_with_markers(
    cwd: impl AsRef<Path>,
    tool: &str,
    markers: &[&str],
) -> Vec<ResolvedPath> {
    if markers.is_empty() {
        return paths_for_tool(cwd, tool);
    }
    chain(cwd.as_ref(), tool, markers)
}

/// The canonical cascade for `tool` from the working directory: nearest
/// project marker, `<config home>/<tool>/config.yaml`,
/// `/etc/<tool>/config.yaml`. An unresolvable project leaves that slot
/// empty, which the loader skips.
pub fn options_for_tool(tool: &str) -> Options {
    options_for_tool_with_markers(tool, &[])
}

/// [`options_for_tool`] with custom project markers.
pub fn options_for_tool_with_markers(tool: &str, markers: &[&str]) -> Options {
    let cwd = std::env::current_dir().unwrap_or_default();
    options_for_tool_with_markers_from(&cwd, tool, markers)
}

/// [`options_for_tool_with_markers`] from an explicit `cwd`.
pub fn options_for_tool_with_markers_from(cwd: &Path, tool: &str, markers: &[&str]) -> Options {
    let mut opts = Options::default();
    if !cwd.as_os_str().is_empty() {
        let found = if markers.is_empty() {
            paths(cwd)
        } else {
            paths_for_tool_with_markers(cwd, tool, markers)
        };
        if let Some(p) = found
            .into_iter()
            .find(|p| (p.source == "cwd" || p.source == "project") && p.exists)
        {
            opts.project_config_path = p.path;
        }
    }
    opts.user_config_path = user_config_path(tool);
    opts.system_config_path = system_config_path(tool);
    opts
}
