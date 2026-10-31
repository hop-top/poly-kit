//! Idempotency replay: the in-memory ledger behind `idempotency_key`
//! (go: `cmdsurface.IdempotencyLedger`).
//!
//! A keyed call from a caller is answered from the ledger when the same
//! caller already ran the same invocation under that key: the recorded
//! result comes back marked replayed and nothing runs. A key naming a
//! call still running refuses `idempotency_conflict`; a key used for a
//! different invocation, running or recorded, refuses
//! `idempotency_key_reused`. Only a run that ended without error and
//! with exit code zero is recorded, so a failed call can be retried.

use std::collections::HashMap;
use std::sync::{Arc, Mutex, PoisonError};
use std::time::{Duration, Instant};

use serde_json::Value;

use super::wire::{
    CommandResult, Established, Invocation, CODE_CONFLICT, IDEMPOTENCY_CONFLICT,
    IDEMPOTENCY_KEY_REUSED,
};

/// How long a recorded result replays, when the builder names none.
pub const DEFAULT_TTL: Duration = Duration::from_secs(24 * 60 * 60);

/// Most records kept. Memory is the only bound a local channel gets: at
/// the cap the oldest record goes.
const MAX_RECORDS: usize = 10_000;

/// The flag a command's own idempotency middleware reads; the key it
/// carries is not part of what the invocation is.
const KEY_FLAG: &str = "idempotency-key";

struct Record {
    fingerprint: String,
    result: CommandResult,
    at: Instant,
}

#[derive(Default)]
struct State {
    inflight: HashMap<String, String>,
    done: HashMap<String, Record>,
}

/// Shared by every connection of one service.
#[derive(Clone)]
pub(crate) struct Ledger {
    ttl: Duration,
    state: Arc<Mutex<State>>,
}

/// What admitting a keyed call decided.
pub(crate) enum Admission {
    /// Run it, then settle the claim.
    Run(Claim),
    /// Answer with the record; nothing runs.
    Replay(CommandResult),
    /// Refuse with this message (wire code `CONFLICT`).
    Refuse(String),
}

pub(crate) const REFUSE_CODE: &str = CODE_CONFLICT;

/// A reservation of a key for the run. Dropping it unsettled (the run
/// was abandoned, canceled or panicked) releases the key.
pub(crate) struct Claim {
    ledger: Ledger,
    key: String,
    fingerprint: String,
    settled: bool,
}

impl Ledger {
    pub(crate) fn new(ttl: Duration) -> Self {
        Self {
            ttl,
            state: Arc::default(),
        }
    }

    pub(crate) fn admit(&self, inv: &Invocation) -> Admission {
        let key = store_key(inv);
        let fingerprint = fingerprint(inv);
        let mut st = self.state.lock().unwrap_or_else(PoisonError::into_inner);
        if let Some(running) = st.inflight.get(&key) {
            return Admission::Refuse(if *running == fingerprint {
                format!("{IDEMPOTENCY_CONFLICT}: key in use by a call still running")
            } else {
                reused()
            });
        }
        if let Some(rec) = st.done.get(&key) {
            if rec.at.elapsed() <= self.ttl {
                if rec.fingerprint != fingerprint {
                    return Admission::Refuse(reused());
                }
                return Admission::Replay(rec.result.clone());
            }
            st.done.remove(&key);
        }
        st.inflight.insert(key.clone(), fingerprint.clone());
        Admission::Run(Claim {
            ledger: self.clone(),
            key,
            fingerprint,
            settled: false,
        })
    }
}

fn reused() -> String {
    format!("{IDEMPOTENCY_KEY_REUSED}: key already used for a different invocation")
}

impl Claim {
    /// The run ended: record a success, release the key either way.
    /// The record is written before the release, so a retry arriving
    /// after the release replays.
    pub(crate) fn settle(mut self, outcome: Option<&CommandResult>) {
        self.settled = true;
        let ledger = &self.ledger;
        let mut st = ledger.state.lock().unwrap_or_else(PoisonError::into_inner);
        if let Some(res) = outcome.filter(|r| r.exit_code == 0) {
            if st.done.len() >= MAX_RECORDS {
                if let Some(oldest) = st
                    .done
                    .iter()
                    .min_by_key(|(_, r)| r.at)
                    .map(|(k, _)| k.clone())
                {
                    st.done.remove(&oldest);
                }
            }
            st.done.insert(
                self.key.clone(),
                Record {
                    fingerprint: self.fingerprint.clone(),
                    result: res.clone(),
                    at: Instant::now(),
                },
            );
        }
        st.inflight.remove(&self.key);
    }
}

impl Drop for Claim {
    fn drop(&mut self) {
        if !self.settled {
            let mut st = self
                .ledger
                .state
                .lock()
                .unwrap_or_else(PoisonError::into_inner);
            st.inflight.remove(&self.key);
        }
    }
}

/// The key confined to the caller's scope, so one caller's key never
/// answers another: a verified principal to its tenant and principal;
/// a caller the owner-only socket vouches for to the transport, never
/// by the name it claims.
fn store_key(inv: &Invocation) -> String {
    let m = &inv.meta;
    match m.established {
        Established::Verified => format!(
            "verified\0{}\0{}\0{}",
            m.tenant, m.caller, m.idempotency_key
        ),
        Established::Transport => format!("transport\0socket\0{}", m.idempotency_key),
    }
}

/// What the key was used for: path, args and flags less the key flag,
/// with object keys ordered so the same call spelled in another key
/// order is the same call.
fn fingerprint(inv: &Invocation) -> String {
    let mut flags = inv.flags.clone();
    flags.remove(KEY_FLAG);
    let mut out = String::new();
    canonical(
        &Value::Array(vec![
            Value::from(inv.path.clone()),
            Value::from(inv.args.clone()),
            Value::Object(flags),
        ]),
        &mut out,
    );
    out
}

fn canonical(v: &Value, out: &mut String) {
    match v {
        Value::Object(m) => {
            let mut keys: Vec<&String> = m.keys().collect();
            keys.sort();
            out.push('{');
            for k in keys {
                out.push_str(&Value::String(k.clone()).to_string());
                out.push(':');
                canonical(&m[k], out);
                out.push(',');
            }
            out.push('}');
        }
        Value::Array(a) => {
            out.push('[');
            for x in a {
                canonical(x, out);
                out.push(',');
            }
            out.push(']');
        }
        other => out.push_str(&other.to_string()),
    }
}
