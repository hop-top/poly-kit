//! A [`SchemaDriver`] for a directory tree. Go `upgrade/driver/fsdir`.
//!
//! `backup` writes `<dest>/<name>.tar.gz`, byte-compatible with Go's: a
//! gzip tar whose member names are relative to the directory (`.` for the
//! root, `/` separators, no `./` prefix), directories and regular files
//! with their mode bits. A Go backup restores here and the reverse.
//! `restore` extracts into a temporary directory beside the target and
//! swaps it in, so a failed extraction leaves the original intact; it
//! refuses archives that would write outside the directory. The driver
//! locks `<data dir>/<name>/.lock`.

use std::fs::{self, File};
use std::io;
use std::path::{Component, Path, PathBuf};
use std::sync::atomic::{AtomicU32, Ordering};

use flate2::read::GzDecoder;
use flate2::write::GzEncoder;
use flate2::Compression;
use tar::{Archive, Builder, EntryType, Header};

use crate::bus::Context;

use super::{filelock, fs_util, read_version_file_in, BoxError, Locker, SchemaDriver, Unlock};

/// Backs up and restores one directory; the Migrator keeps the version in
/// its version file.
#[derive(Debug, Clone)]
pub struct FsDirDriver {
    name: String,
    tool: String,
    data_dir: Option<PathBuf>,
    dir_path: PathBuf,
}

impl FsDirDriver {
    /// A driver for the directory `dir_path`, schema `name` (it names the
    /// archive `<name>.tar.gz`). Go `New`.
    pub fn new(name: impl Into<String>, dir_path: impl Into<PathBuf>) -> FsDirDriver {
        FsDirDriver {
            name: name.into(),
            tool: String::new(),
            data_dir: None,
            dir_path: dir_path.into(),
        }
    }

    /// The tool whose [`default_data_dir`](super::default_data_dir) holds
    /// the version file and the lock. Go `WithTool`.
    pub fn with_tool(mut self, tool: impl Into<String>) -> FsDirDriver {
        self.tool = tool.into();
        self
    }

    /// Reads the version file and locks under `dir`; pass the migrator's
    /// [`data_dir`](super::Migrator::data_dir) when it sets one. Go
    /// `WithDataDir`.
    pub fn with_data_dir(mut self, dir: impl Into<PathBuf>) -> FsDirDriver {
        self.data_dir = Some(dir.into());
        self
    }

    fn archive_name(&self) -> String {
        format!("{}.tar.gz", self.name)
    }
}

impl SchemaDriver for FsDirDriver {
    fn name(&self) -> &str {
        &self.name
    }

    fn version(&self) -> Result<String, BoxError> {
        let dir = self
            .data_dir
            .clone()
            .unwrap_or_else(|| super::default_data_dir(&self.tool));
        Ok(read_version_file_in(&dir, &self.name)?)
    }

    /// The runner has written the version file already.
    fn set_version(&mut self, _version: &str) -> Result<(), BoxError> {
        Ok(())
    }

    fn as_locker(&self) -> Option<&dyn Locker> {
        Some(self)
    }

    /// Writes `<dest>/<name>.tar.gz`; a missing directory is "nothing to
    /// back up" (`Ok`, no archive). Symlinks are stored as links; sockets,
    /// devices and pipes are an error.
    fn backup(&self, dest: &Path) -> Result<(), BoxError> {
        fs_util::create_dir_all(dest).map_err(|e| format!("create backup dir: {e}"))?;
        match fs::symlink_metadata(&self.dir_path) {
            Ok(_) => {}
            Err(e) if e.kind() == io::ErrorKind::NotFound => return Ok(()),
            Err(e) => return Err(format!("stat dir: {e}").into()),
        }
        let archive = dest.join(self.archive_name());
        let tmp = dest.join(format!("{}.tmp", self.archive_name()));
        let written = write_archive(&self.dir_path, &tmp).and_then(|()| fs::rename(&tmp, &archive));
        written.map_err(|e| {
            let _ = fs::remove_file(&tmp);
            format!("backup dir: {e}").into()
        })
    }

    /// Replaces the directory with the archive's tree: extracted into a
    /// temporary directory beside it, then swapped in. A traversal, an
    /// unreadable archive or any other failure removes the temporary
    /// directory and leaves the original untouched.
    fn restore(&mut self, src: &Path) -> Result<(), BoxError> {
        let archive = src.join(self.archive_name());
        if !archive.exists() {
            return Err(format!("backup archive not found: {}", archive.display()).into());
        }
        let parent = match self.dir_path.parent() {
            Some(p) if !p.as_os_str().is_empty() => p.to_path_buf(),
            _ => PathBuf::from("."),
        };
        fs_util::create_dir_all(&parent).map_err(|e| format!("create parent dir: {e}"))?;
        let base = self
            .dir_path
            .file_name()
            .unwrap_or_default()
            .to_string_lossy();
        let stage = fresh_dir(&parent, &format!(".{base}.restore"))
            .map_err(|e| format!("create staging dir: {e}"))?;
        if let Err(e) = extract(&archive, &stage) {
            let _ = fs::remove_dir_all(&stage);
            return Err(e);
        }
        swap(&stage, &self.dir_path, &parent, &base).map_err(|e| {
            let _ = fs::remove_dir_all(&stage);
            format!("restore dir: {e}").into()
        })
    }
}

impl Locker for FsDirDriver {
    /// Exclusive flock on `<data dir>/<name>/.lock`.
    fn lock(&self, ctx: &Context) -> Result<Unlock, BoxError> {
        let dir = filelock::state_dir(self.data_dir.as_deref(), &self.tool, &self.name);
        filelock::lock_in(&dir, ctx)
    }
}

/// Creates a new empty directory `<parent>/<prefix>-<pid>-<n>`.
fn fresh_dir(parent: &Path, prefix: &str) -> io::Result<PathBuf> {
    static N: AtomicU32 = AtomicU32::new(0);
    loop {
        let n = N.fetch_add(1, Ordering::Relaxed);
        let p = parent.join(format!("{prefix}-{}-{n}", std::process::id()));
        match fs_util::create_dir(&p) {
            Ok(()) => return Ok(p),
            Err(e) if e.kind() == io::ErrorKind::AlreadyExists => {}
            Err(e) => return Err(e),
        }
    }
}

/// Moves `stage` to `target`, the old tree aside first and back if the
/// second rename fails.
fn swap(stage: &Path, target: &Path, parent: &Path, base: &str) -> io::Result<()> {
    if fs::symlink_metadata(target).is_err() {
        return fs::rename(stage, target);
    }
    let old = fresh_dir(parent, &format!(".{base}.old"))?;
    fs::remove_dir(&old)?; // the name is reserved; rename needs it free
    fs::rename(target, &old)?;
    if let Err(e) = fs::rename(stage, target) {
        let _ = fs::rename(&old, target);
        return Err(e);
    }
    let _ = fs::remove_dir_all(&old);
    Ok(())
}

// ---- backup ----

fn write_archive(root: &Path, archive: &Path) -> io::Result<()> {
    let gz = GzEncoder::new(File::create(archive)?, Compression::default());
    let mut tar = Builder::new(gz);
    append_tree(&mut tar, root, Path::new("."))?;
    let file = tar.into_inner()?.finish()?;
    file.sync_all()
}

/// Appends `path` (relative name `rel`), then, for a directory, its
/// entries in name order: Go's `filepath.Walk` order.
fn append_tree(tar: &mut Builder<GzEncoder<File>>, path: &Path, rel: &Path) -> io::Result<()> {
    let meta = fs::symlink_metadata(path)?;
    let mut h = Header::new_gnu();
    h.set_metadata(&meta);
    let ty = meta.file_type();
    if ty.is_dir() {
        tar.append_data(&mut h, rel, io::empty())?;
        let mut names = fs::read_dir(path)?
            .map(|e| e.map(|e| e.file_name()))
            .collect::<io::Result<Vec<_>>>()?;
        names.sort();
        for n in names {
            let child = if rel == Path::new(".") {
                PathBuf::from(&n)
            } else {
                rel.join(&n)
            };
            append_tree(tar, &path.join(&n), &child)?;
        }
    } else if ty.is_file() {
        tar.append_data(&mut h, rel, File::open(path)?)?;
    } else if ty.is_symlink() {
        h.set_size(0);
        tar.append_link(&mut h, rel, fs::read_link(path)?)?;
    } else {
        return Err(io::Error::other(format!(
            "unsupported file type: {}",
            path.display()
        )));
    }
    Ok(())
}

// ---- restore ----

/// A member name as a path relative to the root: `.` components dropped;
/// an absolute name, a drive prefix or `..` is a traversal.
fn clean_name(name: &Path) -> Result<PathBuf, BoxError> {
    let mut out = PathBuf::new();
    for c in name.components() {
        match c {
            Component::Normal(p) => out.push(p),
            Component::CurDir => {}
            _ => return Err(format!("path traversal blocked: {}", name.display()).into()),
        }
    }
    Ok(out)
}

/// Whether `link`, stored at `at` (relative to the root), points inside
/// the root: relative, and no `..` climbing above it.
fn link_stays_inside(at: &Path, link: &Path) -> bool {
    let mut depth = at.components().count().saturating_sub(1) as i64;
    for c in link.components() {
        match c {
            Component::Normal(_) => depth += 1,
            Component::CurDir => {}
            Component::ParentDir => depth -= 1,
            _ => return false,
        }
        if depth < 0 {
            return false;
        }
    }
    true
}

/// Fails when a parent of `rel` under `root` is a symlink: a member must
/// not be written through a link an earlier member planted.
fn parents_are_real(root: &Path, rel: &Path) -> Result<(), BoxError> {
    let mut cur = root.to_path_buf();
    let n = rel.components().count();
    for c in rel.components().take(n.saturating_sub(1)) {
        cur.push(c);
        if fs::symlink_metadata(&cur).is_ok_and(|m| m.file_type().is_symlink()) {
            return Err(format!("path traversal blocked: {}", rel.display()).into());
        }
    }
    Ok(())
}

fn extract(archive: &Path, root: &Path) -> Result<(), BoxError> {
    let file = File::open(archive).map_err(|e| format!("open archive: {e}"))?;
    let mut ar = Archive::new(GzDecoder::new(file));
    ar.set_preserve_permissions(false);
    let mut dir_modes: Vec<(PathBuf, u32)> = Vec::new();
    let mut links: Vec<PathBuf> = Vec::new();
    for entry in ar.entries().map_err(|e| format!("tar read: {e}"))? {
        let mut entry = entry.map_err(|e| format!("tar read: {e}"))?;
        let name = entry
            .path()
            .map_err(|e| format!("tar read: {e}"))?
            .into_owned();
        let rel = clean_name(&name)?;
        let target = root.join(&rel);
        let mode = entry.header().mode().unwrap_or(0o600);
        parents_are_real(root, &rel)?;
        let ty = entry.header().entry_type();
        match ty {
            EntryType::Directory => {
                fs_util::create_dir_all(&target)?;
                dir_modes.push((target, mode));
            }
            EntryType::Regular | EntryType::Continuous => {
                if let Some(p) = target.parent() {
                    fs_util::create_dir_all(p)?;
                }
                replace_with_file(&target, &mut entry, mode)?;
            }
            EntryType::Symlink | EntryType::Link => {
                let link = entry
                    .link_name()?
                    .ok_or_else(|| format!("link without target: {}", name.display()))?
                    .into_owned();
                if let Some(p) = target.parent() {
                    fs_util::create_dir_all(p)?;
                }
                if ty == EntryType::Link {
                    // The source must be a regular file reached through real
                    // directories: a link in its path could point outside.
                    let rel_from = clean_name(&link)?;
                    parents_are_real(root, &rel_from)?;
                    let from = root.join(&rel_from);
                    if !fs::symlink_metadata(&from).is_ok_and(|m| m.is_file()) {
                        return Err(format!("link escapes directory: {}", name.display()).into());
                    }
                    remove_existing(&target)?;
                    fs::hard_link(&from, &target)?;
                } else {
                    if !link_stays_inside(&rel, &link) {
                        return Err(format!("link escapes directory: {}", name.display()).into());
                    }
                    remove_existing(&target)?;
                    make_symlink(&link, &target)?;
                    links.push(target);
                }
            }
            other => {
                return Err(
                    format!("special member blocked: {} ({other:?})", name.display()).into(),
                )
            }
        }
    }
    // A link's target may cross links made later: what exists must resolve
    // inside the root.
    let canon = fs::canonicalize(root)?;
    for l in &links {
        match fs::canonicalize(l) {
            Ok(real) if !real.starts_with(&canon) => {
                return Err(format!("link escapes directory: {}", l.display()).into())
            }
            _ => {}
        }
    }
    apply_dir_modes(dir_modes)?;
    Ok(())
}

fn remove_existing(p: &Path) -> io::Result<()> {
    match fs::symlink_metadata(p) {
        Ok(m) if m.is_dir() => fs::remove_dir_all(p),
        Ok(_) => fs::remove_file(p),
        Err(_) => Ok(()),
    }
}

fn replace_with_file(target: &Path, from: &mut dyn io::Read, mode: u32) -> io::Result<()> {
    remove_existing(target)?;
    let mut o = fs::OpenOptions::new();
    o.write(true).create_new(true);
    #[cfg(unix)]
    std::os::unix::fs::OpenOptionsExt::mode(&mut o, 0o600);
    let mut out = o.open(target)?;
    io::copy(from, &mut out)?;
    set_mode(&out, mode)
}

#[cfg(unix)]
fn set_mode(f: &File, mode: u32) -> io::Result<()> {
    use std::os::unix::fs::PermissionsExt;
    f.set_permissions(fs::Permissions::from_mode(mode & 0o7777))
}

#[cfg(not(unix))]
fn set_mode(_: &File, _: u32) -> io::Result<()> {
    Ok(())
}

#[cfg(unix)]
fn make_symlink(link: &Path, at: &Path) -> io::Result<()> {
    std::os::unix::fs::symlink(link, at)
}

#[cfg(not(unix))]
fn make_symlink(_: &Path, _: &Path) -> io::Result<()> {
    Err(io::Error::other("symlink members are not supported here"))
}

/// Directory modes last, children first, so a read-only directory does
/// not stop the members inside it.
#[cfg(unix)]
fn apply_dir_modes(mut dirs: Vec<(PathBuf, u32)>) -> io::Result<()> {
    use std::os::unix::fs::PermissionsExt;
    dirs.reverse();
    for (p, mode) in dirs {
        fs::set_permissions(&p, fs::Permissions::from_mode(mode & 0o7777))?;
    }
    Ok(())
}

#[cfg(not(unix))]
fn apply_dir_modes(_: Vec<(PathBuf, u32)>) -> io::Result<()> {
    Ok(())
}
