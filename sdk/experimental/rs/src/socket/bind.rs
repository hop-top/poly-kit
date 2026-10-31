//! Creating the socket file: owner-only from the first instant it is
//! visible, never stealing a live server's path.

use std::fs;
use std::io;
use std::os::unix::fs::{DirBuilderExt, FileTypeExt, MetadataExt, PermissionsExt};
use std::os::unix::net::{UnixListener as StdListener, UnixStream as StdStream};
use std::path::{Path, PathBuf};

use super::wire::{MAX_PATH_BYTES, SOCKET_MODE};

/// A bound socket and the identity of its file, so close unlinks only
/// the file this process created.
pub(crate) struct Bound {
    pub(crate) listener: tokio::net::UnixListener,
    pub(crate) path: PathBuf,
    pub(crate) dev_ino: (u64, u64),
    pub(crate) uid: u32,
}

/// Binds `path` mode 0600 without a bind-then-chmod window.
///
/// `bind(2)` creates the file with the process umask applied, and
/// narrowing it afterwards leaves a window in which the file is more
/// open than promised. So the socket is bound inside a fresh 0700
/// directory beside the target, narrowed there, then linked to its
/// real name: the link fails if the name exists (never replacing a live
/// server's socket) and the file is 0600 the moment the name appears.
///
/// A stale socket no process listens on is removed first; a live one
/// refuses with `already in use`; a path that is not a socket is left
/// untouched and refused.
pub(crate) fn bind(path: &Path) -> io::Result<Bound> {
    if path.as_os_str().is_empty() {
        return Err(other("socket: empty path"));
    }
    let path = std::path::absolute(path)?;
    check_len(&path)?;
    let parent = path
        .parent()
        .ok_or_else(|| other("socket: path has no parent directory"))?;
    fs::DirBuilder::new()
        .recursive(true)
        .mode(0o700)
        .create(parent)?;
    clear_stale(&path)?;

    let stage = parent.join(format!(".k{:08x}", rand32()));
    fs::DirBuilder::new().mode(0o700).create(&stage)?;
    let result = bind_staged(&stage, &path);
    let _ = fs::remove_dir(&stage);
    result
}

fn bind_staged(stage: &Path, path: &Path) -> io::Result<Bound> {
    let tmp = stage.join("s");
    check_len(&tmp)?;
    let ln = StdListener::bind(&tmp)?;
    ln.set_nonblocking(true)?;
    fs::set_permissions(&tmp, fs::Permissions::from_mode(SOCKET_MODE))?;
    match fs::hard_link(&tmp, path) {
        Ok(()) => {}
        Err(e) if e.kind() == io::ErrorKind::AlreadyExists => {
            let _ = fs::remove_file(&tmp);
            return Err(other(format!(
                "socket: {} is already in use",
                path.display()
            )));
        }
        Err(_) => {
            // A filesystem that cannot link a socket: rename is the
            // fallback, with the same 0600 file and no window, but it
            // replaces whatever appeared at the name meanwhile.
            fs::rename(&tmp, path)?;
        }
    }
    let _ = fs::remove_file(&tmp);
    let meta = fs::symlink_metadata(path)?;
    Ok(Bound {
        listener: tokio::net::UnixListener::from_std(ln)?,
        path: path.to_path_buf(),
        dev_ino: (meta.dev(), meta.ino()),
        uid: meta.uid(),
    })
}

fn clear_stale(path: &Path) -> io::Result<()> {
    let meta = match fs::symlink_metadata(path) {
        Ok(m) => m,
        Err(e) if e.kind() == io::ErrorKind::NotFound => return Ok(()),
        Err(e) => return Err(e),
    };
    if !meta.file_type().is_socket() {
        return Err(other(format!(
            "socket: {} exists and is not a socket",
            path.display()
        )));
    }
    // A successful connect means a live server owns this path.
    if StdStream::connect(path).is_ok() {
        return Err(other(format!(
            "socket: {} is already in use",
            path.display()
        )));
    }
    fs::remove_file(path)
}

/// Unlinks `path` if it is still the file this process bound.
pub(crate) fn unlink(path: &Path, dev_ino: (u64, u64)) {
    if let Ok(m) = fs::symlink_metadata(path) {
        if (m.dev(), m.ino()) == dev_ino {
            let _ = fs::remove_file(path);
        }
    }
}

fn check_len(path: &Path) -> io::Result<()> {
    use std::os::unix::ffi::OsStrExt;
    let n = path.as_os_str().as_bytes().len();
    if n > MAX_PATH_BYTES {
        return Err(other(format!(
            "socket: path is {n} bytes, over the {MAX_PATH_BYTES}-byte limit"
        )));
    }
    Ok(())
}

fn other(msg: impl Into<String>) -> io::Error {
    io::Error::other(msg.into())
}

pub(crate) fn rand32() -> u32 {
    let mut b = [0u8; 4];
    if getrandom::fill(&mut b).is_err() {
        return std::time::SystemTime::now()
            .duration_since(std::time::UNIX_EPOCH)
            .map_or(0, |d| d.subsec_nanos());
    }
    u32::from_le_bytes(b)
}
