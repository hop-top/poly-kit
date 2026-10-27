//! A [`SchemaDriver`] for one or more files rewritten in place (config,
//! saved layouts, caches). Go `upgrade/driver/configfile`.

use std::fs;
use std::io;
use std::path::{Path, PathBuf, MAIN_SEPARATOR};

use super::{default_data_dir, fs_util, read_version_file_in, BoxError, SchemaDriver};

/// Backs up and restores a set of files; the version lives in the
/// runner's version file.
#[derive(Debug, Clone)]
pub struct ConfigFileDriver {
    name: String,
    tool: String,
    data_dir: Option<PathBuf>,
    paths: Vec<PathBuf>,
}

impl ConfigFileDriver {
    /// A driver named `config` over `paths`. Go `New`.
    pub fn new<P: AsRef<Path>>(paths: impl IntoIterator<Item = P>) -> ConfigFileDriver {
        ConfigFileDriver {
            name: "config".to_string(),
            tool: String::new(),
            data_dir: None,
            paths: paths
                .into_iter()
                .map(|p| p.as_ref().to_path_buf())
                .collect(),
        }
    }

    /// Overrides the schema name. Go `WithName`.
    pub fn with_name(mut self, name: impl Into<String>) -> ConfigFileDriver {
        self.name = name.into();
        self
    }

    /// The tool whose [`default_data_dir`] holds the version file. Go
    /// `WithTool`.
    pub fn with_tool(mut self, tool: impl Into<String>) -> ConfigFileDriver {
        self.tool = tool.into();
        self
    }

    /// Reads the version file under `dir`; pass the migrator's
    /// [`data_dir`](super::Migrator::data_dir) when it sets one.
    pub fn with_data_dir(mut self, dir: impl Into<PathBuf>) -> ConfigFileDriver {
        self.data_dir = Some(dir.into());
        self
    }
}

/// A collision-safe file name for `path`: its separators replaced by
/// `_`, so `/etc/foo/config.yaml` is `_etc_foo_config.yaml`. Go
/// `backupFileName` (which also cleans the path first).
pub fn backup_file_name(path: &Path) -> String {
    let clean: PathBuf = path.components().collect();
    clean.to_string_lossy().replace(MAIN_SEPARATOR, "_")
}

fn copy_file(src: &Path, dst: &Path) -> io::Result<()> {
    let mut input = fs::File::open(src)?;
    if let Some(dir) = dst.parent().filter(|d| !d.as_os_str().is_empty()) {
        fs_util::create_dir_all(dir)?;
    }
    let mut out = fs::File::create(dst)?;
    io::copy(&mut input, &mut out)?;
    out.sync_all()
}

impl SchemaDriver for ConfigFileDriver {
    fn name(&self) -> &str {
        &self.name
    }

    fn version(&self) -> Result<String, BoxError> {
        let dir = self
            .data_dir
            .clone()
            .unwrap_or_else(|| default_data_dir(&self.tool));
        Ok(read_version_file_in(&dir, &self.name)?)
    }

    /// The runner has written the version file already.
    fn set_version(&mut self, _version: &str) -> Result<(), BoxError> {
        Ok(())
    }

    /// Copies each file into `dest`; a missing file is skipped.
    fn backup(&self, dest: &Path) -> Result<(), BoxError> {
        fs_util::create_dir_all(dest).map_err(|e| format!("create backup dir: {e}"))?;
        for p in &self.paths {
            let name = backup_file_name(p);
            match copy_file(p, &dest.join(&name)) {
                Ok(()) => {}
                Err(e) if e.kind() == io::ErrorKind::NotFound => {}
                Err(e) => return Err(format!("backup {name}: {e}").into()),
            }
        }
        Ok(())
    }

    /// Copies each backed-up file back; a file absent from the backup is
    /// left alone.
    fn restore(&mut self, src: &Path) -> Result<(), BoxError> {
        for p in &self.paths {
            let name = backup_file_name(p);
            let saved = src.join(&name);
            if !saved.exists() {
                continue;
            }
            copy_file(&saved, p).map_err(|e| format!("restore {name}: {e}"))?;
        }
        Ok(())
    }
}
