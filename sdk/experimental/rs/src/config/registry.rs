//! Named registry sources. Ports Go `source.go`.

use serde::{Deserialize, Serialize};

/// A named source. Kind, endpoint and dirs are loose strings so the
/// config layer stays agnostic of what consumers implement.
#[derive(Clone, Debug, Default, PartialEq, Eq, Serialize, Deserialize)]
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

/// Writes `r` inside a top-level `registry:` block, creating parent
/// directories.
#[cfg(not(target_arch = "wasm32"))]
pub fn save_registry(path: impl AsRef<std::path::Path>, r: &Registry) -> Result<(), super::Error> {
    let path = path.as_ref();
    if let Some(dir) = path.parent().filter(|d| !d.as_os_str().is_empty()) {
        std::fs::create_dir_all(dir).map_err(|e| super::error::io_error("mkdir", dir, &e))?;
    }
    let node = super::yaml::ser::to_node(&Wrap {
        registry: r.clone(),
    })?;
    let text = super::yaml::emit::document(&node, 4);
    std::fs::write(path, text).map_err(|e| super::error::io_error("open", path, &e))
}
