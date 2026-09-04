//! Filesystem-backed [`Store`] implementation.
//!
//! Keys may contain path separators (e.g. `"a/b/c"`); intermediate
//! directories are created automatically on [`LocalStore::put`].

use std::fs::{self, File, OpenOptions};
use std::io::{self, Read, Write};
#[cfg(unix)]
use std::os::unix::fs::PermissionsExt;
use std::path::{Path, PathBuf};
use std::sync::atomic::{AtomicU64, Ordering};

use super::{BlobError, Object, Store};

/// Monotonic counter feeding staging-file names, so two concurrent puts to
/// the same key never contend for the same temp path.
static TMP_SEQ: AtomicU64 = AtomicU64::new(0);

/// Buffer size used when streaming a blob into its staging file.
const COPY_BUF: usize = 64 * 1024;

/// Mode applied to stored blobs, matching the Go backend. Owner read/write
/// plus group read; never world-readable, since blobs may hold backups.
#[cfg(unix)]
const BLOB_MODE: u32 = 0o640;

/// A filesystem-backed blob store rooted at a directory.
#[derive(Debug, Clone)]
pub struct LocalStore {
    root: PathBuf,
}

impl LocalStore {
    /// Return a store rooted at `dir`, creating the directory if needed.
    pub fn new<P: AsRef<Path>>(dir: P) -> Result<Self, BlobError> {
        let abs = absolute(dir.as_ref()).map_err(|e| BlobError::io("abs root", e))?;
        fs::create_dir_all(&abs).map_err(|e| BlobError::io("mkdir root", e))?;
        Ok(LocalStore { root: abs })
    }

    /// Root directory the store is anchored at.
    pub fn root(&self) -> &Path {
        &self.root
    }

    /// Map a slash-separated key onto an absolute path inside the root,
    /// rejecting anything that escapes it.
    ///
    /// The invariant enforced is `resolved` strictly under `self.root` —
    /// never equal to it. Every empty/dot/leading-slash segment is a
    /// no-op in the walk below (`""` from an empty key, a leading `/`, or
    /// a doubled `//`; `"."` from a literal dot component), so any key
    /// spelling that reduces to zero effective segments — `""`, `"."`,
    /// `"a/.."`, `"./."`, and so on — resolves to exactly `self.root`
    /// unless that's rejected explicitly. It used to not be: `put("")`
    /// or `put(".")` would stage a file and then rename it onto the
    /// store root directory itself. A leading `/` on its own is safe and
    /// intentionally still allowed through — `resolve("/etc/passwd")`
    /// treats the leading `/` as the same no-op empty segment a doubled
    /// `//` would produce, landing at `root/etc/passwd`, safely nested;
    /// rejecting every leading-slash key outright would be broader than
    /// this function needs to be.
    fn resolve(&self, key: &str) -> Result<PathBuf, BlobError> {
        let mut resolved = self.root.clone();
        for segment in key.split('/') {
            match segment {
                "" | "." => continue,
                ".." => {
                    if !resolved.pop() {
                        return Err(BlobError::KeyEscapesRoot(key.to_string()));
                    }
                }
                other => resolved.push(other),
            }
        }
        if resolved == self.root || !resolved.starts_with(&self.root) {
            return Err(BlobError::KeyEscapesRoot(key.to_string()));
        }
        Ok(resolved)
    }
}

/// Report whether `name` is an in-flight (or crash-orphaned) staging file,
/// which must never surface as a key.
fn is_temp_name(name: &str) -> bool {
    name.starts_with('.') && name.ends_with(".tmp")
}

/// Resolve `p` against the process working directory without touching the
/// filesystem, matching Go's `filepath.Abs`.
fn absolute(p: &Path) -> io::Result<PathBuf> {
    if p.is_absolute() {
        return Ok(p.to_path_buf());
    }
    Ok(std::env::current_dir()?.join(p))
}

/// Create a uniquely-named staging file next to `dest`, returning the handle
/// and its path.
fn create_temp(dir: &Path, base: &str) -> io::Result<(File, PathBuf)> {
    for _ in 0..1000 {
        let seq = TMP_SEQ.fetch_add(1, Ordering::Relaxed);
        let pid = std::process::id();
        let path = dir.join(format!(".{base}.{pid}.{seq}.tmp"));
        match OpenOptions::new().write(true).create_new(true).open(&path) {
            Ok(f) => return Ok((f, path)),
            Err(e) if e.kind() == io::ErrorKind::AlreadyExists => continue,
            Err(e) => return Err(e),
        }
    }
    Err(io::Error::new(
        io::ErrorKind::AlreadyExists,
        "exhausted staging file name attempts",
    ))
}

/// Collect every file under `dir`, recursing into subdirectories.
fn walk(dir: &Path, out: &mut Vec<PathBuf>) -> io::Result<()> {
    let entries = match fs::read_dir(dir) {
        Ok(e) => e,
        Err(e) if e.kind() == io::ErrorKind::NotFound => return Ok(()),
        Err(e) => return Err(e),
    };
    for entry in entries {
        let entry = entry?;
        let path = entry.path();
        if entry.file_type()?.is_dir() {
            walk(&path, out)?;
        } else {
            out.push(path);
        }
    }
    Ok(())
}

impl Store for LocalStore {
    type Reader = File;

    fn put<R: Read>(&self, key: &str, mut r: R, _content_type: &str) -> Result<(), BlobError> {
        let dest = self.resolve(key)?;
        let dir = dest
            .parent()
            .ok_or_else(|| BlobError::KeyEscapesRoot(key.to_string()))?;
        fs::create_dir_all(dir).map_err(|e| BlobError::io("mkdir", e))?;

        let base = dest
            .file_name()
            .and_then(|n| n.to_str())
            .ok_or_else(|| BlobError::KeyEscapesRoot(key.to_string()))?
            .to_string();

        let (mut f, tmp) = create_temp(dir, &base).map_err(|e| BlobError::io("create tmp", e))?;

        // Stage, sync, rename. Any failure removes the staging file so the
        // destination keeps whatever value it already held.
        let staged = (|| -> io::Result<()> {
            let mut buf = vec![0u8; COPY_BUF];
            loop {
                let n = match r.read(&mut buf) {
                    Ok(0) => break,
                    Ok(n) => n,
                    Err(e) if e.kind() == io::ErrorKind::Interrupted => continue,
                    Err(e) => return Err(e),
                };
                f.write_all(&buf[..n])?;
            }
            f.sync_all()?;
            Ok(())
        })();

        drop(f);

        if let Err(e) = staged {
            let _ = fs::remove_file(&tmp);
            return Err(BlobError::io("write", e));
        }
        // Match the Go backend's 0640 on the staged file before it becomes
        // visible at the destination. Rust creates files at 0666 & ~umask,
        // typically 0644, which would publish blobs world-readable.
        #[cfg(unix)]
        if let Err(e) = fs::set_permissions(&tmp, PermissionsExt::from_mode(BLOB_MODE)) {
            let _ = fs::remove_file(&tmp);
            return Err(BlobError::io("chmod", e));
        }
        if let Err(e) = fs::rename(&tmp, &dest) {
            let _ = fs::remove_file(&tmp);
            return Err(BlobError::io("rename", e));
        }
        Ok(())
    }

    fn get(&self, key: &str) -> Result<Self::Reader, BlobError> {
        let p = self.resolve(key)?;
        File::open(&p).map_err(|e| match e.kind() {
            io::ErrorKind::NotFound => BlobError::NotFound(key.to_string()),
            _ => BlobError::io("open", e),
        })
    }

    fn delete(&self, key: &str) -> Result<(), BlobError> {
        let p = self.resolve(key)?;
        fs::remove_file(&p).map_err(|e| match e.kind() {
            io::ErrorKind::NotFound => BlobError::NotFound(key.to_string()),
            _ => BlobError::io("remove", e),
        })
    }

    fn list(&self, prefix: &str) -> Result<Vec<Object>, BlobError> {
        let mut paths = Vec::new();
        walk(&self.root, &mut paths).map_err(|e| BlobError::io("walk", e))?;

        let mut objects = Vec::new();
        for path in paths {
            let name = path
                .file_name()
                .and_then(|n| n.to_str())
                .unwrap_or_default();
            if is_temp_name(name) {
                continue;
            }
            let rel = match path.strip_prefix(&self.root) {
                Ok(r) => r,
                Err(_) => continue,
            };
            let key = rel
                .components()
                .map(|c| c.as_os_str().to_string_lossy())
                .collect::<Vec<_>>()
                .join("/");
            if !key.starts_with(prefix) {
                continue;
            }
            let meta = fs::metadata(&path).map_err(|e| BlobError::io("stat", e))?;
            objects.push(Object {
                key,
                size: meta.len(),
                content_type: String::new(),
            });
        }
        objects.sort_by(|a, b| a.key.cmp(&b.key));
        Ok(objects)
    }

    fn exists(&self, key: &str) -> Result<bool, BlobError> {
        let p = self.resolve(key)?;
        match fs::metadata(&p) {
            Ok(_) => Ok(true),
            Err(e) if e.kind() == io::ErrorKind::NotFound => Ok(false),
            Err(e) => Err(BlobError::io("stat", e)),
        }
    }
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn temp_names_are_recognised() {
        assert!(is_temp_name(".greet.txt.12.0.tmp"));
        assert!(!is_temp_name("greet.txt"));
        assert!(!is_temp_name(".hidden"));
        assert!(!is_temp_name("plain.tmp"));
    }

    #[test]
    fn resolve_rejects_escaping_keys() {
        let dir = std::env::temp_dir().join(format!("kit-blob-resolve-{}", std::process::id()));
        let s = LocalStore::new(&dir).expect("new store");
        assert!(matches!(
            s.resolve("../escape"),
            Err(BlobError::KeyEscapesRoot(_))
        ));
        assert!(s.resolve("a/b/c").is_ok());
        let _ = fs::remove_dir_all(&dir);
    }

    /// Every key spelling that reduces to zero effective path segments
    /// resolves to exactly the store root, and must be rejected — not
    /// just the empty string. Pre-fix, only `""` and a leading `/` were
    /// special-cased; `"."` and `"a/.."` both slipped through the same
    /// way `""` did, reaching `put()`'s rename step with the store root
    /// itself as the destination.
    #[test]
    fn resolve_rejects_every_key_that_resolves_to_root() {
        let dir =
            std::env::temp_dir().join(format!("kit-blob-resolve-root-{}", std::process::id()));
        let s = LocalStore::new(&dir).expect("new store");
        for key in ["", ".", "/", "a/..", "./.", "a/../.", "a/b/../.."] {
            assert!(
                matches!(s.resolve(key), Err(BlobError::KeyEscapesRoot(_))),
                "key {key:?} must be rejected: resolves to the store root"
            );
        }
        let _ = fs::remove_dir_all(&dir);
    }

    /// A leading `/` is not itself an escape attempt: `split('/')`
    /// produces an empty first segment, the same no-op a doubled `//`
    /// would produce, so `"/etc/passwd"` resolves safely nested under
    /// the store root (`root/etc/passwd`) rather than at the real
    /// filesystem's `/etc/passwd`. Rejecting every leading-slash key
    /// outright would be broader than the escape-prevention this
    /// function exists for — only a key that ends up outside root, or
    /// exactly at root, is unsafe.
    #[test]
    fn resolve_accepts_leading_slash_key_as_nested_under_root() {
        let dir = std::env::temp_dir().join(format!("kit-blob-resolve-abs-{}", std::process::id()));
        let s = LocalStore::new(&dir).expect("new store");
        let resolved = s
            .resolve("/etc/passwd")
            .expect("leading slash must not escape");
        assert_eq!(resolved, s.root().join("etc").join("passwd"));
        let _ = fs::remove_dir_all(&dir);
    }
}
