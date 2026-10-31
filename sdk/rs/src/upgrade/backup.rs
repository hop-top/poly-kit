//! Backup directories per schema: `<data dir>/backups/<schema>/pre-<version>/`.
//! Go `backupOrchestrator`.

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

    /// Creates and returns the backup directory taken before `version`.
    pub(super) fn create(&self, version: &str) -> io::Result<PathBuf> {
        let dir = self.dir.join(format!("pre-{version}"));
        super::fs_util::create_dir_all(&dir)?;
        Ok(dir)
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
        let dirs = self.sorted().map_err(LatestError::Read)?;
        match dirs.last() {
            Some(name) => Ok(self.dir.join(name)),
            None => Err(LatestError::Empty(self.schema.clone())),
        }
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
        }
        Ok(())
    }
}

#[derive(Debug, thiserror::Error)]
pub(super) enum LatestError {
    #[error("read backup dir: {0}")]
    Read(#[source] io::Error),
    #[error("no backups found for {0}")]
    Empty(String),
}
