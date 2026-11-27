//! Backup directories per schema: `<data dir>/backups/<schema>/pre-<version>/`,
//! each with the version the run started at saved beside it in
//! `pre-<version>.version`. Go `backupOrchestrator`.

use std::fs;
use std::io;
use std::path::{Path, PathBuf};

use super::compare_versions;

pub(super) const DEFAULT_RETENTION: usize = 5;

pub(super) struct Backups {
    dir: PathBuf,
    schema: String,
    retention: usize,
}

impl Backups {
    pub(super) fn new(data_dir: &Path, schema: &str, retention: usize) -> Backups {
        Backups {
            dir: data_dir.join("backups").join(schema),
            schema: schema.to_string(),
            retention: if retention == 0 {
                DEFAULT_RETENTION
            } else {
                retention
            },
        }
    }

    /// The backup directory taken before `version`.
    pub(super) fn path(&self, version: &str) -> PathBuf {
        self.dir.join(format!("pre-{version}"))
    }

    /// Creates and returns the backup directory taken before `version`.
    pub(super) fn create(&self, version: &str) -> io::Result<PathBuf> {
        let dir = self.path(version);
        super::fs_util::create_dir_all(&dir)?;
        Ok(dir)
    }

    /// Saves, beside `backup`, the version the run that took it started
    /// at (`""` is an empty file).
    pub(super) fn save_pre_run(backup: &Path, version: &str) -> io::Result<()> {
        super::fs_util::write_private(&sidecar(backup), version.as_bytes())
    }

    /// The version saved beside `backup`; `None` for a backup taken
    /// before it was saved.
    pub(super) fn read_pre_run(backup: &Path) -> io::Result<Option<String>> {
        match fs::read_to_string(sidecar(backup)) {
            Ok(s) => Ok(Some(s.trim().to_string())),
            Err(e) if e.kind() == io::ErrorKind::NotFound => Ok(None),
            Err(e) => Err(e),
        }
    }

    /// `pre-*` directories, ascending by the version they name.
    fn sorted(&self) -> io::Result<Vec<String>> {
        let mut dirs: Vec<String> = Vec::new();
        for entry in fs::read_dir(&self.dir)? {
            let entry = entry?;
            if !entry.file_type()?.is_dir() {
                continue;
            }
            let Ok(name) = entry.file_name().into_string() else {
                continue;
            };
            if name.starts_with("pre-") {
                dirs.push(name);
            }
        }
        dirs.sort_by(|a, b| compare_versions(&a["pre-".len()..], &b["pre-".len()..]));
        Ok(dirs)
    }

    /// The backup with the highest version.
    pub(super) fn latest(&self) -> Result<PathBuf, LatestError> {
        let dirs = match self.sorted() {
            Ok(d) => d,
            Err(e) if e.kind() == io::ErrorKind::NotFound => return Err(self.empty()),
            Err(e) => return Err(LatestError::Read(e)),
        };
        match dirs.last() {
            Some(name) => Ok(self.dir.join(name)),
            None => Err(self.empty()),
        }
    }

    fn empty(&self) -> LatestError {
        LatestError::Empty(self.schema.clone())
    }

    /// Removes all but the `retention` newest backups. A missing
    /// directory is nothing to prune.
    pub(super) fn prune(&self) -> io::Result<()> {
        let dirs = match self.sorted() {
            Ok(d) => d,
            Err(e) if e.kind() == io::ErrorKind::NotFound => return Ok(()),
            Err(e) => return Err(e),
        };
        let excess = dirs.len().saturating_sub(self.retention);
        for name in &dirs[..excess] {
            fs::remove_dir_all(self.dir.join(name))?;
            match fs::remove_file(sidecar(&self.dir.join(name))) {
                Err(e) if e.kind() != io::ErrorKind::NotFound => return Err(e),
                _ => {}
            }
        }
        Ok(())
    }
}

/// The file holding a backup's pre-run version: `<backup dir>.version`.
fn sidecar(backup: &Path) -> PathBuf {
    let mut s = backup.as_os_str().to_owned();
    s.push(".version");
    PathBuf::from(s)
}

#[derive(Debug, thiserror::Error)]
pub(super) enum LatestError {
    #[error("read backup dir: {0}")]
    Read(#[source] io::Error),
    #[error("no backups found for {0}")]
    Empty(String),
}
