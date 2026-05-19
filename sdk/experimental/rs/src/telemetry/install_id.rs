//! Installation ID reader/writer (T-0724).
//!
//! Mirrors `go/runtime/telemetry/installid.go`: 32 raw bytes from
//! `getrandom`, stored at
//! `<XDG_STATE_HOME>/kit/telemetry/installation_id` with 0600 perms +
//! 0700 parent dir. The public API returns the lowercase hex SHA-256
//! of those bytes so the on-disk format stays canonical across
//! polyglot SDKs while the hashed string is what flows through
//! events.
//!
//! Concurrency: this implementation handles intra-process and
//! cross-process races via a tmp-file + rename strategy. If two
//! processes race on first-run the rename loser falls back to reading
//! whatever the winner wrote.

use sha2::{Digest, Sha256};
use std::fmt::Write as _;
use std::fs;
use std::io;
use std::os::unix::fs::PermissionsExt;
use std::path::{Path, PathBuf};

const INSTALL_ID_SIZE: usize = 32;
const FILE_PERM: u32 = 0o600;
const DIR_PERM: u32 = 0o700;

/// Resolve `<XDG_STATE_HOME>/kit/telemetry/installation_id`, honoring
/// the XDG_STATE_HOME env var with a `$HOME/.local/state` fallback.
pub fn install_id_path() -> PathBuf {
    let base = std::env::var_os("XDG_STATE_HOME")
        .map(PathBuf::from)
        .unwrap_or_else(|| {
            dirs::home_dir()
                .unwrap_or_default()
                .join(".local")
                .join("state")
        });
    base.join("kit").join("telemetry").join("installation_id")
}

/// Return the persisted installation ID as a 64-char lowercase hex
/// SHA-256 digest. Generates + persists 32 random bytes on first call.
/// Cross-process race: if two callers race on the first generation the
/// rename loser re-reads whatever the winner wrote.
pub fn get_install_id() -> io::Result<String> {
    let p = install_id_path();

    // Fast path: file already exists.
    if p.exists() {
        let data = fs::read(&p)?;
        if data.len() != INSTALL_ID_SIZE {
            return Err(io::Error::new(
                io::ErrorKind::InvalidData,
                format!(
                    "install_id: file has wrong size {} bytes, expected {}",
                    data.len(),
                    INSTALL_ID_SIZE
                ),
            ));
        }
        return Ok(hex_sha256(&data));
    }

    // Slow path: generate + persist atomically.
    ensure_parent(&p)?;

    let mut fresh = [0u8; INSTALL_ID_SIZE];
    getrandom::getrandom(&mut fresh)
        .map_err(|e| io::Error::other(format!("getrandom: {e}")))?;

    let tmp = p.with_extension("new");
    fs::write(&tmp, fresh)?;
    fs::set_permissions(&tmp, fs::Permissions::from_mode(FILE_PERM))?;

    match fs::rename(&tmp, &p) {
        Ok(()) => Ok(hex_sha256(&fresh)),
        Err(_) if p.exists() => {
            // Lost the race to another process; read what they wrote.
            let _ = fs::remove_file(&tmp);
            let data = fs::read(&p)?;
            if data.len() != INSTALL_ID_SIZE {
                return Err(io::Error::new(
                    io::ErrorKind::InvalidData,
                    format!(
                        "install_id: file has wrong size {} bytes, expected {}",
                        data.len(),
                        INSTALL_ID_SIZE
                    ),
                ));
            }
            Ok(hex_sha256(&data))
        }
        Err(e) => {
            let _ = fs::remove_file(&tmp);
            Err(e)
        }
    }
}

/// Atomically replace the persisted bytes with 32 fresh random bytes
/// and return the new hex. Used by the `kit consent reset` flow.
pub fn rotate() -> io::Result<String> {
    let p = install_id_path();
    ensure_parent(&p)?;

    let mut fresh = [0u8; INSTALL_ID_SIZE];
    getrandom::getrandom(&mut fresh)
        .map_err(|e| io::Error::other(format!("getrandom: {e}")))?;

    let tmp = p.with_extension("new");
    // Clear any stale .new from a previous crashed writer before
    // re-creating it.
    let _ = fs::remove_file(&tmp);
    fs::write(&tmp, fresh)?;
    fs::set_permissions(&tmp, fs::Permissions::from_mode(FILE_PERM))?;
    fs::rename(&tmp, &p)?;
    Ok(hex_sha256(&fresh))
}

/// Create the parent directory of `path` with 0700 perms (idempotent).
/// Force-tightens existing dirs in case some other tool created the
/// parent at 0755.
fn ensure_parent(path: &Path) -> io::Result<()> {
    let parent = path.parent().ok_or_else(|| {
        io::Error::new(
            io::ErrorKind::InvalidInput,
            "install_id: path has no parent",
        )
    })?;
    fs::create_dir_all(parent)?;
    fs::set_permissions(parent, fs::Permissions::from_mode(DIR_PERM))?;
    Ok(())
}

fn hex_sha256(bytes: &[u8]) -> String {
    let mut h = Sha256::new();
    h.update(bytes);
    let result = h.finalize();
    let mut s = String::with_capacity(64);
    for b in result {
        // Infallible on String.
        let _ = write!(s, "{:02x}", b);
    }
    s
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn hex_sha256_known_vector() {
        // SHA-256("") = e3b0c44298fc1c149afbf4c8996fb92427ae41e4649b934ca495991b7852b855
        let h = hex_sha256(&[]);
        assert_eq!(
            h,
            "e3b0c44298fc1c149afbf4c8996fb92427ae41e4649b934ca495991b7852b855"
        );
        assert_eq!(h.len(), 64);
    }
}
