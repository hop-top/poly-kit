//! The advisory lock the SQLite and directory drivers share: an exclusive
//! `std::fs::File::lock` on `.lock` in the version file's directory
//! (`<data dir>/<schema>/`). On unix `File::try_lock` is flock(2), the
//! lock Go's `gofrs/flock` takes on the same file, so a Rust and a Go
//! process migrating one store exclude each other.

use std::fs::{self, File, TryLockError};
use std::path::{Path, PathBuf};
use std::thread;
use std::time::Duration;

use crate::bus::Context;

use super::{default_data_dir, fs_util, BoxError, Unlock};

/// The lock file's name, beside the version file.
const LOCK_FILE: &str = ".lock";

/// How often a blocked wait retries.
const POLL: Duration = Duration::from_millis(50);

/// The directory of schema `name`'s version file: `<data dir>/<name>`, the
/// data dir being `data_dir` or [`default_data_dir`] of `tool`. The lock
/// file sits here.
pub(super) fn state_dir(data_dir: Option<&Path>, tool: &str, name: &str) -> PathBuf {
    let base = data_dir.map_or_else(|| default_data_dir(tool), Path::to_path_buf);
    base.join(name)
}

/// Takes the exclusive lock on `<dir>/.lock`, creating `dir` and the file
/// if needed, and waits (polling `try_lock`) until it is held or `ctx` is
/// done: then the context's error comes back and no descriptor is left
/// open. One descriptor per call; the file is never deleted. The returned
/// unlock owns the descriptor.
pub(super) fn lock_in(dir: &Path, ctx: &Context) -> Result<Unlock, BoxError> {
    fs_util::create_dir_all(dir).map_err(|e| format!("create lock dir: {e}"))?;
    let path = dir.join(LOCK_FILE);
    let file = open(&path).map_err(|e| format!("lock {}: {e}", path.display()))?;
    loop {
        if let Some(e) = ctx.err() {
            return Err(Box::new(e)); // `file` drops: nothing held
        }
        match file.try_lock() {
            Ok(()) => break,
            Err(TryLockError::WouldBlock) => thread::sleep(POLL),
            Err(TryLockError::Error(e)) => {
                return Err(format!("lock {}: {e}", path.display()).into())
            }
        }
    }
    Ok(Box::new(move || {
        file.unlock()
            .map_err(|e| format!("unlock {}: {e}", path.display()).into())
    }))
}

fn open(path: &Path) -> std::io::Result<File> {
    let mut o = fs::OpenOptions::new();
    o.read(true).write(true).create(true).truncate(false);
    #[cfg(unix)]
    std::os::unix::fs::OpenOptionsExt::mode(&mut o, 0o600);
    o.open(path)
}
