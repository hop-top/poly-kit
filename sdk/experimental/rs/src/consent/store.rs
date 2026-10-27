//! The store seam for persisted (until and always) grants.

use std::collections::BTreeMap;
use std::sync::Mutex;

use super::error::Error;
use super::grant::{sort_grants, Grant, Key};
use super::telemetry::TelemetryDecision;

/// Persists until and always grants; once and session grants never
/// reach a store.
pub trait GrantStore: Send + Sync {
    /// Every persisted grant sorted by capability, then subject. A store
    /// that cannot be read whole returns a `malformed_store` error;
    /// callers fail closed on it.
    fn grants(&self) -> Result<Vec<Grant>, Error>;

    /// Replaces the grant for `g`'s capability and subject.
    fn put_grant(&self, g: &Grant) -> Result<(), Error>;

    /// Removes the grant for the key and reports whether one was there.
    fn delete_grant(&self, capability: &str, subject: &str) -> Result<bool, Error>;

    /// The telemetry record combined over every source this store reads
    /// (the file store: grants file and legacy records, deny-wins).
    /// `None` when the store keeps telemetry only as a grant, as the
    /// memory store does; then the `telemetry` grant is the record.
    fn telemetry(&self) -> Option<Result<TelemetryDecision, Error>> {
        None
    }
}

/// A store held in memory: tests and hosts with no filesystem.
#[derive(Default)]
pub struct MemoryGrantStore {
    m: Mutex<BTreeMap<Key, Grant>>,
}

impl MemoryGrantStore {
    pub fn new() -> Self {
        Self::default()
    }

    fn lock(&self) -> std::sync::MutexGuard<'_, BTreeMap<Key, Grant>> {
        self.m.lock().unwrap_or_else(|e| e.into_inner())
    }
}

impl GrantStore for MemoryGrantStore {
    fn grants(&self) -> Result<Vec<Grant>, Error> {
        let mut out: Vec<Grant> = self.lock().values().cloned().collect();
        sort_grants(&mut out);
        Ok(out)
    }

    fn put_grant(&self, g: &Grant) -> Result<(), Error> {
        g.check_persistable()?;
        self.lock().insert(g.key(), g.clone());
        Ok(())
    }

    fn delete_grant(&self, capability: &str, subject: &str) -> Result<bool, Error> {
        Ok(self
            .lock()
            .remove(&(capability.to_string(), subject.to_string()))
            .is_some())
    }
}
