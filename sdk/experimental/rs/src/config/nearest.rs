//! [`nearest`]: walk up to a project marker. Ports Go `nearest.go`.

use std::path::{Path, PathBuf};

use super::paths::go_abs;
use super::{Error, ErrorKind};

/// Caps the parent walk; the filesystem root normally ends it first.
pub const DEFAULT_MAX_DEPTH: usize = 64;

/// Walks up from `start_dir` (empty: the working directory) looking for
/// `marker`, a path relative to each ancestor (`.rlz` matches a
/// directory, `.rlz/config.yaml` a file). Returns the first hit, or
/// [`ErrorKind::MarkerNotFound`] (`config: project marker not found`).
pub fn nearest(start_dir: impl AsRef<Path>, marker: &str) -> Result<PathBuf, Error> {
    nearest_with_depth(start_dir, marker, DEFAULT_MAX_DEPTH)
}

/// [`nearest`] with an explicit depth cap (0 = unlimited).
pub fn nearest_with_depth(
    start_dir: impl AsRef<Path>,
    marker: &str,
    max_depth: usize,
) -> Result<PathBuf, Error> {
    if marker.is_empty() {
        return Err(Error::invalid("config: marker is empty"));
    }
    let start = start_dir.as_ref();
    let mut dir = if start.as_os_str().is_empty() {
        let cwd = std::env::current_dir()
            .map_err(|e| super::error::io_error("getwd", Path::new(""), &e))?;
        go_abs(&cwd)
    } else {
        go_abs(start)
    };
    let mut depth = 0;
    while max_depth == 0 || depth < max_depth {
        let candidate = dir.join(marker);
        match std::fs::metadata(&candidate) {
            Ok(_) => return Ok(candidate),
            Err(e) if e.kind() == std::io::ErrorKind::NotFound => {}
            Err(e) => return Err(super::error::io_error("stat", &candidate, &e)),
        }
        match dir.parent() {
            Some(p) if p != dir => dir = p.to_path_buf(),
            _ => break,
        }
        depth += 1;
    }
    Err(Error::new(
        ErrorKind::MarkerNotFound,
        "config: project marker not found",
    ))
}
