//! Named registry sources. Ports Go `source.go`.

use std::fmt;

use serde::{Deserialize, Serialize};

/// A named source. Kind, endpoint and dirs are loose strings so the
/// config layer stays agnostic of what consumers implement.
#[derive(Clone, Default, PartialEq, Eq, Serialize, Deserialize)]
#[serde(default)]
pub struct Source {
    pub name: String,
    #[serde(rename = "type")]
    pub kind: String,
    #[serde(skip_serializing_if = "String::is_empty")]
    pub endpoint: String,
    #[serde(skip_serializing_if = "String::is_empty")]
    pub token: String,
    #[serde(skip_serializing_if = "Vec::is_empty")]
    pub dirs: Vec<String>,
}

/// Redacts `token`, so logging a source never leaks the credential.
impl fmt::Debug for Source {
    fn fmt(&self, f: &mut fmt::Formatter<'_>) -> fmt::Result {
        let token = if self.token.is_empty() {
            ""
        } else {
            "[REDACTED]"
        };
        f.debug_struct("Source")
            .field("name", &self.name)
            .field("kind", &self.kind)
            .field("endpoint", &self.endpoint)
            .field("token", &token)
            .field("dirs", &self.dirs)
            .finish()
    }
}

/// Sources in insertion order, looked up by name.
#[derive(Clone, Debug, Default, PartialEq, Eq, Serialize, Deserialize)]
#[serde(default)]
pub struct Registry {
    #[serde(skip_serializing_if = "Vec::is_empty")]
    pub sources: Vec<Source>,
}

impl Registry {
    pub fn get(&self, name: &str) -> Option<&Source> {
        self.sources.iter().find(|s| s.name == name)
    }

    /// Appends `s`; an existing name is replaced in place.
    pub fn add(&mut self, s: Source) {
        match self.sources.iter_mut().find(|x| x.name == s.name) {
            Some(slot) => *slot = s,
            None => self.sources.push(s),
        }
    }

    /// Drops `name`; false when absent.
    pub fn remove(&mut self, name: &str) -> bool {
        match self.sources.iter().position(|s| s.name == name) {
            Some(i) => {
                self.sources.remove(i);
                true
            }
            None => false,
        }
    }

    /// Names in insertion order.
    pub fn names(&self) -> Vec<String> {
        self.sources.iter().map(|s| s.name.clone()).collect()
    }

    /// Folds `other` in: new names append, existing names are replaced.
    pub fn merge(&mut self, other: Registry) {
        for s in other.sources {
            self.add(s);
        }
    }
}

#[cfg(not(target_arch = "wasm32"))]
#[derive(Default, Serialize, Deserialize)]
#[serde(default)]
struct Wrap {
    registry: Registry,
}

/// Reads a top-level `registry:` block. An empty path or a missing file
/// is an empty registry.
#[cfg(not(target_arch = "wasm32"))]
pub fn load_registry(path: impl AsRef<std::path::Path>) -> Result<Registry, super::Error> {
    let path = path.as_ref();
    if path.as_os_str().is_empty() {
        return Ok(Registry::default());
    }
    let data = match std::fs::read_to_string(path) {
        Ok(d) => d,
        Err(e) if e.kind() == std::io::ErrorKind::NotFound => return Ok(Registry::default()),
        Err(e) => return Err(super::error::io_error("open", path, &e)),
    };
    let Some(node) = super::yaml::parse(&data)? else {
        return Ok(Registry::default());
    };
    let w: Wrap = super::yaml::de::from_node(&node)?;
    Ok(w.registry)
}

/// Writes `r` inside a top-level `registry:` block. Sources may carry
/// tokens, so the file is private: missing parent directories are
/// created 0o700, and the file is written 0o600 to a temp file in the
/// same directory, then renamed into place (on Unix; elsewhere default
/// permissions apply).
#[cfg(not(target_arch = "wasm32"))]
pub fn save_registry(path: impl AsRef<std::path::Path>, r: &Registry) -> Result<(), super::Error> {
    use std::io::Write;

    let path = path.as_ref();
    let dir = match path.parent().filter(|d| !d.as_os_str().is_empty()) {
        Some(d) => d.to_path_buf(),
        None => std::path::PathBuf::from("."),
    };
    let mut builder = std::fs::DirBuilder::new();
    builder.recursive(true);
    #[cfg(unix)]
    std::os::unix::fs::DirBuilderExt::mode(&mut builder, 0o700);
    builder
        .create(&dir)
        .map_err(|e| super::error::io_error("mkdir", &dir, &e))?;
    let node = super::yaml::ser::to_node(&Wrap {
        registry: r.clone(),
    })?;
    let text = super::yaml::emit::document(&node, 4);

    let (tmp_path, mut file) = create_private_temp(&dir)?;
    let written = file
        .write_all(text.as_bytes())
        .and_then(|()| file.sync_all())
        .map_err(|e| super::error::io_error("write", &tmp_path, &e));
    drop(file);
    let renamed = written.and_then(|()| {
        std::fs::rename(&tmp_path, path).map_err(|e| super::error::io_error("rename", path, &e))
    });
    if renamed.is_err() {
        let _ = std::fs::remove_file(&tmp_path);
    }
    renamed
}

/// A fresh `0o600` file next to the target (exclusive create).
#[cfg(not(target_arch = "wasm32"))]
fn create_private_temp(
    dir: &std::path::Path,
) -> Result<(std::path::PathBuf, std::fs::File), super::Error> {
    use std::sync::atomic::{AtomicU64, Ordering};
    static SEQ: AtomicU64 = AtomicU64::new(0);
    let nanos = std::time::SystemTime::now()
        .duration_since(std::time::UNIX_EPOCH)
        .map_or(0, |d| d.subsec_nanos());
    loop {
        let name = format!(
            ".registry-{}-{nanos}-{}.tmp",
            std::process::id(),
            SEQ.fetch_add(1, Ordering::Relaxed)
        );
        let p = dir.join(name);
        let mut opts = std::fs::OpenOptions::new();
        opts.write(true).create_new(true);
        #[cfg(unix)]
        std::os::unix::fs::OpenOptionsExt::mode(&mut opts, 0o600);
        match opts.open(&p) {
            Ok(f) => return Ok((p, f)),
            Err(e) if e.kind() == std::io::ErrorKind::AlreadyExists => continue,
            Err(e) => return Err(super::error::io_error("open", &p, &e)),
        }
    }
}
