//! The usage ledger behind a [`ServedGate`](super::ServedGate)'s budgets:
//! Go's `cmdsurface.UsageLedger`, over a pluggable [`UsageStore`].
//!
//! The store defaults to memory ([`MemoryUsageStore`]): counts start
//! over with the process. Persistence is opt-in, with the SQLite file
//! `$XDG_STATE_HOME/<tool>/usage.db` ([`UsageLedger::open_file`], feature
//! `kv`) or any [`UsageStore`] of the adopter's.
//!
//! Entries are stored as Go stores them (`kit/usage/<query-escaped
//! key>/<window seconds>/<window start, Unix seconds>` holding
//! `{"ops":N,"bytes":N}`), so a file written by one SDK reads in the
//! other.

use std::collections::{BTreeSet, HashMap};
use std::sync::{Arc, Mutex, PoisonError};
use std::time::{Duration, SystemTime, UNIX_EPOCH};

use super::go_duration;

/// Seconds between the zero time Go aligns windows to (year 1) and the
/// Unix epoch.
const GO_ZERO_TO_UNIX_SECS: u128 = 62_135_596_800;

/// The kv key prefix every ledger entry is stored under.
const LEDGER_PREFIX: &str = "kit/usage/";

/// Where a ledger keeps its entries: the part of Go's `kv.Store` +
/// `kv.TTLStore` it reads and writes. Implementations are called one
/// operation at a time per ledger; an entry past its TTL must read as
/// absent and not be listed.
pub trait UsageStore: Send + Sync {
    /// The value under `key`, `None` when absent or expired.
    fn get(&self, key: &str) -> Result<Option<Vec<u8>>, String>;
    /// Stores `value` under `key`, expiring after `ttl`.
    fn put_with_ttl(&self, key: &str, value: &[u8], ttl: Duration) -> Result<(), String>;
    /// Removes `key`; a missing key is not an error.
    fn delete(&self, key: &str) -> Result<(), String>;
    /// Every live key starting with `prefix`.
    fn list(&self, prefix: &str) -> Result<Vec<String>, String>;
}

/// The default store: entries in a map, lost with the process, dropped
/// once expired.
#[derive(Default)]
pub struct MemoryUsageStore {
    entries: Mutex<HashMap<String, (Vec<u8>, SystemTime)>>,
}

impl MemoryUsageStore {
    /// An empty store.
    pub fn new() -> MemoryUsageStore {
        MemoryUsageStore::default()
    }
}

impl UsageStore for MemoryUsageStore {
    fn get(&self, key: &str) -> Result<Option<Vec<u8>>, String> {
        let m = self.entries.lock().unwrap_or_else(PoisonError::into_inner);
        Ok(m.get(key)
            .filter(|(_, exp)| *exp > SystemTime::now())
            .map(|(v, _)| v.clone()))
    }

    fn put_with_ttl(&self, key: &str, value: &[u8], ttl: Duration) -> Result<(), String> {
        let now = SystemTime::now();
        let exp = now
            .checked_add(ttl)
            .unwrap_or(now + Duration::from_secs(u32::MAX.into()));
        let mut m = self.entries.lock().unwrap_or_else(PoisonError::into_inner);
        m.retain(|_, (_, e)| *e > now);
        m.insert(key.to_string(), (value.to_vec(), exp));
        Ok(())
    }

    fn delete(&self, key: &str) -> Result<(), String> {
        self.entries
            .lock()
            .unwrap_or_else(PoisonError::into_inner)
            .remove(key);
        Ok(())
    }

    fn list(&self, prefix: &str) -> Result<Vec<String>, String> {
        let now = SystemTime::now();
        let m = self.entries.lock().unwrap_or_else(PoisonError::into_inner);
        Ok(m.iter()
            .filter(|(k, (_, e))| k.starts_with(prefix) && *e > now)
            .map(|(k, _)| k.clone())
            .collect())
    }
}

/// The SQLite file store ([`crate::kv::SqliteStore`]) as a
/// [`UsageStore`].
#[cfg(feature = "kv")]
#[cfg_attr(docsrs, doc(cfg(feature = "kv")))]
pub struct KvUsageStore {
    inner: Mutex<crate::kv::SqliteStore>,
}

#[cfg(feature = "kv")]
impl KvUsageStore {
    /// Counts in `store`.
    pub fn new(store: crate::kv::SqliteStore) -> KvUsageStore {
        KvUsageStore {
            inner: Mutex::new(store),
        }
    }

    fn lock(&self) -> std::sync::MutexGuard<'_, crate::kv::SqliteStore> {
        self.inner.lock().unwrap_or_else(PoisonError::into_inner)
    }
}

#[cfg(feature = "kv")]
impl UsageStore for KvUsageStore {
    fn get(&self, key: &str) -> Result<Option<Vec<u8>>, String> {
        crate::kv::Store::get(&*self.lock(), key.as_bytes()).map_err(|e| e.to_string())
    }

    fn put_with_ttl(&self, key: &str, value: &[u8], ttl: Duration) -> Result<(), String> {
        crate::kv::TtlStore::put_with_ttl(&*self.lock(), key.as_bytes(), value, ttl)
            .map_err(|e| e.to_string())
    }

    fn delete(&self, key: &str) -> Result<(), String> {
        crate::kv::Store::delete(&*self.lock(), key.as_bytes()).map_err(|e| e.to_string())
    }

    fn list(&self, prefix: &str) -> Result<Vec<String>, String> {
        let keys =
            crate::kv::Store::list(&*self.lock(), prefix.as_bytes()).map_err(|e| e.to_string())?;
        Ok(keys
            .into_iter()
            .filter_map(|k| String::from_utf8(k).ok())
            .collect())
    }
}

/// Which store a ledger counts in: the values of the
/// `services.usage_store` key Go reads.
#[derive(Clone, Copy, Debug, Default, PartialEq, Eq)]
pub enum UsageStoreKind {
    /// In memory; counts die with the process. The default.
    #[default]
    Memory,
    /// The SQLite file `$XDG_STATE_HOME/<tool>/usage.db` (feature `kv`).
    File,
}

impl UsageStoreKind {
    /// Parses `memory` or `file`, case-insensitively; empty is the
    /// default (`memory`).
    pub fn parse(s: &str) -> Result<UsageStoreKind, String> {
        match s.trim().to_ascii_lowercase().as_str() {
            "" | "memory" => Ok(UsageStoreKind::Memory),
            "file" => Ok(UsageStoreKind::File),
            v => Err(format!(
                "services.usage_store: unknown store \"{v}\"; use memory or file"
            )),
        }
    }
}

/// What a key has used in one window of a [`UsageLedger`].
#[derive(Clone, Copy, Debug, PartialEq, Eq)]
pub struct Usage {
    /// Calls counted.
    pub ops: i64,
    /// Bytes counted.
    pub bytes: i64,
    /// When the window began.
    pub start: SystemTime,
    /// When the window ends and the count starts over.
    pub reset: SystemTime,
}

type Clock = Arc<dyn Fn() -> SystemTime + Send + Sync>;

/// Counts calls and bytes per key over fixed windows, on a
/// [`UsageStore`] (memory unless given another). Windows are aligned as
/// Go's `time.Truncate` aligns them (a one-hour window runs from the top
/// of one hour to the next, a day from midnight UTC); a count belongs to
/// the window it was made in and is never carried into the next. A
/// ledger serializes its own operations, so check and count are atomic
/// within one process; processes sharing a store may overshoot a limit
/// by the calls they admit at the same instant.
#[derive(Clone)]
pub struct UsageLedger {
    now: Clock,
    store: Arc<dyn UsageStore>,
    lock: Arc<Mutex<()>>,
}

impl Default for UsageLedger {
    fn default() -> Self {
        UsageLedger::new()
    }
}

impl std::fmt::Debug for UsageLedger {
    fn fmt(&self, f: &mut std::fmt::Formatter<'_>) -> std::fmt::Result {
        f.write_str("UsageLedger")
    }
}

impl UsageLedger {
    /// A ledger on the system clock, counting in memory.
    pub fn new() -> UsageLedger {
        UsageLedger::with_store(Arc::new(MemoryUsageStore::new()))
    }

    /// A ledger on the system clock counting in `store`.
    pub fn with_store(store: Arc<dyn UsageStore>) -> UsageLedger {
        UsageLedger {
            now: Arc::new(SystemTime::now),
            store,
            lock: Arc::default(),
        }
    }

    /// A ledger counting in the SQLite file at `path`.
    #[cfg(feature = "kv")]
    #[cfg_attr(docsrs, doc(cfg(feature = "kv")))]
    pub fn with_file(path: &str) -> Result<UsageLedger, String> {
        let store = crate::kv::SqliteStore::new(path).map_err(|e| format!("usage store: {e}"))?;
        Ok(UsageLedger::with_store(Arc::new(KvUsageStore::new(store))))
    }

    /// A ledger counting in `$XDG_STATE_HOME/<tool>/usage.db`, the file
    /// Go's `service.WithUsageFile` opens.
    #[cfg(all(feature = "kv", not(target_arch = "wasm32")))]
    #[cfg_attr(docsrs, doc(cfg(feature = "kv")))]
    pub fn open_file(tool: &str) -> Result<UsageLedger, String> {
        let path = usage_path(tool);
        let dir = path.parent().unwrap_or(std::path::Path::new("."));
        std::fs::create_dir_all(dir).map_err(|e| format!("usage store: {e}"))?;
        #[cfg(unix)]
        {
            use std::os::unix::fs::PermissionsExt;
            std::fs::set_permissions(dir, std::fs::Permissions::from_mode(0o700))
                .map_err(|e| format!("usage store: {e}"))?;
        }
        UsageLedger::with_file(&path.to_string_lossy())
    }

    /// The ledger `kind` names for `tool`: memory, or the usage file.
    /// `File` fails without feature `kv`.
    pub fn open(kind: UsageStoreKind, tool: &str) -> Result<UsageLedger, String> {
        match kind {
            UsageStoreKind::Memory => Ok(UsageLedger::new()),
            #[cfg(all(feature = "kv", not(target_arch = "wasm32")))]
            UsageStoreKind::File => UsageLedger::open_file(tool),
            #[cfg(not(all(feature = "kv", not(target_arch = "wasm32"))))]
            UsageStoreKind::File => {
                let _ = tool;
                Err("usage store: the file store needs feature \"kv\"".into())
            }
        }
    }

    /// The same ledger on `now`; for tests that cross a window boundary.
    #[must_use]
    pub fn with_clock_of<F>(mut self, now: F) -> UsageLedger
    where
        F: Fn() -> SystemTime + Send + Sync + 'static,
    {
        self.now = Arc::new(now);
        self
    }

    /// A memory ledger on `now`; for tests that cross a window boundary.
    pub fn with_clock<F>(now: F) -> UsageLedger
    where
        F: Fn() -> SystemTime + Send + Sync + 'static,
    {
        UsageLedger::new().with_clock_of(now)
    }

    /// The window of length `w` holding now: its start, as nanoseconds
    /// since Go's zero time.
    fn window(&self, w: Duration) -> Result<u128, String> {
        if w < Duration::from_secs(1) {
            return Err(format!(
                "usage ledger: window {} is under a second",
                go_duration(w)
            ));
        }
        let since_unix = (self.now)()
            .duration_since(UNIX_EPOCH)
            .map_err(|_| "usage ledger: clock is before 1970".to_string())?
            .as_nanos();
        let t = since_unix + GO_ZERO_TO_UNIX_SECS * 1_000_000_000;
        let w_ns = w.as_nanos();
        Ok(t - t % w_ns)
    }

    fn time_of(go_ns: u128) -> SystemTime {
        let unix_ns = go_ns - GO_ZERO_TO_UNIX_SECS * 1_000_000_000;
        UNIX_EPOCH
            + Duration::new(
                (unix_ns / 1_000_000_000) as u64,
                (unix_ns % 1_000_000_000) as u32,
            )
    }

    /// Loads `key`'s current window; `fresh` is true when nothing is
    /// stored for it yet.
    fn load(&self, key: &str, w: Duration) -> Result<(Usage, bool), String> {
        let start = self.window(w)?;
        let mut u = Usage {
            ops: 0,
            bytes: 0,
            start: Self::time_of(start),
            reset: Self::time_of(start + w.as_nanos()),
        };
        let raw = self
            .store
            .get(&entry_key(key, w, u.start))
            .map_err(|e| format!("usage ledger: {e}"))?;
        let Some(raw) = raw else {
            return Ok((u, true));
        };
        // A corrupt entry is not a reason to refuse a caller; it counts
        // from zero again.
        if let Ok(v) = serde_json::from_slice::<serde_json::Value>(&raw) {
            u.ops = v.get("ops").and_then(|n| n.as_i64()).unwrap_or(0);
            u.bytes = v.get("bytes").and_then(|n| n.as_i64()).unwrap_or(0);
        }
        Ok((u, false))
    }

    fn save(&self, key: &str, w: Duration, u: &Usage) -> Result<(), String> {
        // Go's field order, whatever the JSON map preserves.
        let raw = format!(r#"{{"ops":{},"bytes":{}}}"#, u.ops, u.bytes);
        // Kept a whole window past its end, so a clock a little behind
        // still finds it.
        let left = u
            .reset
            .duration_since((self.now)())
            .unwrap_or(Duration::ZERO);
        self.store
            .put_with_ttl(&entry_key(key, w, u.start), raw.as_bytes(), left + w)
            .map_err(|e| format!("usage ledger: {e}"))
    }

    /// What `key` has used in the current window of length `w`.
    pub fn usage(&self, key: &str, w: Duration) -> Result<Usage, String> {
        let _g = self.lock.lock().unwrap_or_else(PoisonError::into_inner);
        Ok(self.load(key, w)?.0)
    }

    /// Counts one call against `key` in the current window when the
    /// window has counted fewer than `max_ops`; reports whether it did,
    /// with the window's usage after.
    pub fn take(&self, key: &str, w: Duration, max_ops: i64) -> Result<(Usage, bool), String> {
        let _g = self.lock.lock().unwrap_or_else(PoisonError::into_inner);
        let (mut u, _) = self.load(key, w)?;
        if u.ops >= max_ops {
            return Ok((u, false));
        }
        u.ops += 1;
        self.save(key, w, &u)?;
        Ok((u, true))
    }

    /// Counts `ops` calls and `bytes` bytes against `key` in the current
    /// window, and returns the window's usage after them.
    pub fn add(&self, key: &str, w: Duration, ops: i64, bytes: i64) -> Result<Usage, String> {
        let _g = self.lock.lock().unwrap_or_else(PoisonError::into_inner);
        let (mut u, _) = self.load(key, w)?;
        u.ops += ops;
        u.bytes += bytes;
        self.save(key, w, &u)?;
        Ok(u)
    }

    /// Forgets every window `key` has counted.
    pub fn reset(&self, key: &str) -> Result<(), String> {
        let _g = self.lock.lock().unwrap_or_else(PoisonError::into_inner);
        let stored = self
            .store
            .list(&entry_prefix(key))
            .map_err(|e| format!("usage ledger: {e}"))?;
        for s in stored {
            self.store
                .delete(&s)
                .map_err(|e| format!("usage ledger: {e}"))?;
        }
        Ok(())
    }

    /// Every key starting with `prefix` that has a count stored, sorted.
    pub fn keys(&self, prefix: &str) -> Result<Vec<String>, String> {
        let _g = self.lock.lock().unwrap_or_else(PoisonError::into_inner);
        let stored = self
            .store
            .list(&format!("{LEDGER_PREFIX}{}", query_escape(prefix)))
            .map_err(|e| format!("usage ledger: {e}"))?;
        let mut out = BTreeSet::new();
        for s in stored {
            let rest = s.strip_prefix(LEDGER_PREFIX).unwrap_or(&s);
            if let Some((esc, _)) = rest.split_once('/') {
                if let Some(k) = query_unescape(esc) {
                    out.insert(k);
                }
            }
        }
        Ok(out.into_iter().collect())
    }
}

/// `$XDG_STATE_HOME/<tool>/usage.db`.
#[cfg(all(feature = "kv", not(target_arch = "wasm32")))]
fn usage_path(tool: &str) -> std::path::PathBuf {
    crate::xdg::raw_state_dir(tool).join("usage.db")
}

/// The storage key of `key`'s window starting at `start`.
fn entry_key(key: &str, w: Duration, start: SystemTime) -> String {
    let secs = start.duration_since(UNIX_EPOCH).map_or(0, |d| d.as_secs());
    format!("{}{}/{}", entry_prefix(key), w.as_secs(), secs)
}

/// The storage prefix of every window of `key`.
fn entry_prefix(key: &str) -> String {
    format!("{LEDGER_PREFIX}{}/", query_escape(key))
}

/// Go's `url.QueryEscape`.
fn query_escape(s: &str) -> String {
    let mut out = String::with_capacity(s.len());
    for b in s.bytes() {
        match b {
            b'A'..=b'Z' | b'a'..=b'z' | b'0'..=b'9' | b'-' | b'_' | b'.' | b'~' => {
                out.push(b as char)
            }
            b' ' => out.push('+'),
            _ => out.push_str(&format!("%{b:02X}")),
        }
    }
    out
}

/// Go's `url.QueryUnescape`; `None` on a malformed escape.
fn query_unescape(s: &str) -> Option<String> {
    let b = s.as_bytes();
    let mut out = Vec::with_capacity(b.len());
    let mut i = 0;
    while i < b.len() {
        match b[i] {
            b'%' => {
                let h = s.get(i + 1..i + 3)?;
                out.push(u8::from_str_radix(h, 16).ok()?);
                i += 3;
            }
            b'+' => {
                out.push(b' ');
                i += 1;
            }
            c => {
                out.push(c);
                i += 1;
            }
        }
    }
    String::from_utf8(out).ok()
}

#[cfg(test)]
mod tests;
