//! Registry — holds Formatter implementations keyed by [`Formatter::key`].
//!
//! Mirrors py `Registry`, ts `Registry`, go `*Registry`, php `Registry`.

use std::collections::BTreeMap;
use std::sync::{Arc, OnceLock, RwLock};

use thiserror::Error;

use super::formatter::Formatter;

/// Errors returned by [`Registry::register`].
#[derive(Debug, Error)]
pub enum RegisterError {
    #[error("output: formatter key is empty")]
    EmptyKey,
    #[error("output: formatter '{0}' already registered (use override to replace)")]
    Duplicate(String),
}

/// Holds Formatter implementations keyed by [`Formatter::key`].
///
/// `register` returns an error on duplicate; adopters intentionally
/// replacing a built-in must call `override_with`.
pub struct Registry {
    inner: RwLock<BTreeMap<&'static str, Arc<dyn Formatter>>>,
}

impl Registry {
    pub fn new() -> Self {
        Self {
            inner: RwLock::new(BTreeMap::new()),
        }
    }

    pub fn register(&self, f: Arc<dyn Formatter>) -> Result<(), RegisterError> {
        let key = f.key();
        if key.is_empty() {
            return Err(RegisterError::EmptyKey);
        }
        let mut g = self.inner.write().expect("registry rwlock poisoned");
        if g.contains_key(key) {
            return Err(RegisterError::Duplicate(key.to_string()));
        }
        g.insert(key, f);
        Ok(())
    }

    /// Replace (or insert) the formatter under `f.key()`. Used by
    /// adopters intentionally swapping out a built-in.
    pub fn override_with(&self, f: Arc<dyn Formatter>) -> Result<(), RegisterError> {
        let key = f.key();
        if key.is_empty() {
            return Err(RegisterError::EmptyKey);
        }
        self.inner
            .write()
            .expect("registry rwlock poisoned")
            .insert(key, f);
        Ok(())
    }

    pub fn lookup(&self, key: &str) -> Option<Arc<dyn Formatter>> {
        self.inner
            .read()
            .expect("registry rwlock poisoned")
            .get(key)
            .cloned()
    }

    /// All registered keys, sorted for stable output.
    pub fn keys(&self) -> Vec<&'static str> {
        self.inner
            .read()
            .expect("registry rwlock poisoned")
            .keys()
            .copied()
            .collect()
    }

    /// All registered formatters in key order.
    pub fn formatters(&self) -> Vec<Arc<dyn Formatter>> {
        self.inner
            .read()
            .expect("registry rwlock poisoned")
            .values()
            .cloned()
            .collect()
    }

    /// `ext` (lowercase, no leading dot) → formatter key.
    pub fn extension_map(&self) -> BTreeMap<String, &'static str> {
        let mut out = BTreeMap::new();
        let g = self.inner.read().expect("registry rwlock poisoned");
        for (k, f) in g.iter() {
            for ext in f.extensions() {
                out.insert(ext.trim_start_matches('.').to_ascii_lowercase(), *k);
            }
        }
        out
    }
}

impl Default for Registry {
    fn default() -> Self {
        Self::new()
    }
}

/// Process-wide default registry. Built-ins (JSON, YAML) are registered
/// against it lazily on first access.
pub fn default_registry() -> Arc<Registry> {
    static DEFAULT: OnceLock<Arc<Registry>> = OnceLock::new();
    DEFAULT
        .get_or_init(|| {
            let r = Arc::new(Registry::new());
            super::builtins::register_all(&r);
            r
        })
        .clone()
}
