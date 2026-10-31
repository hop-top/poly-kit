//! Kit-issued opaque API keys held hashed in a `kv` store, a port of
//! Go's `authn.APIKeys`.
//!
//! A key is `kit_<id>_<secret>`: id 8 random bytes, secret 32, both
//! lowercase hex. The store keeps, per id, the principal, tenant,
//! scopes, expiry, revocation and a hash of the secret, never the
//! secret. The hash is SHA-256 over a domain-separated encoding of the
//! id and the secret, compared in constant time; a slow password KDF
//! would only add latency, since a 256-bit random secret cannot be
//! guessed at any speed. The record is the JSON Go writes, so one
//! store serves both ports.

use std::collections::BTreeMap;
use std::sync::{Arc, Mutex};
use std::time::{Duration, SystemTime};

use serde::{Deserialize, Serialize};
use sha2::{Digest, Sha256};
use subtle::ConstantTimeEq;

use super::claims::Claims;
use super::error::AuthnError;
use super::rfc3339;
use super::verifier::{CheckFn, NowFn, Token};
use super::{bearer_token, AuthFunc};
use crate::kv::Store;

/// Begins every API key, so secret scanners and people recognize one.
pub const API_KEY_PREFIX: &str = "kit";
/// The header an API key may ride in instead of `Authorization:
/// Bearer`.
pub const API_KEY_HEADER: &str = "X-API-Key";
/// The default hash domain; a branded deployment keeping existing
/// hashes names its own with [`ApiKeys::with_hash_domain`].
pub const API_KEY_HASH_DOMAIN: &str = "kit-apikey-v1";

const STORE_PREFIX: &str = "apikey/";
const ID_BYTES: usize = 8;
const SECRET_BYTES: usize = 32;

/// One stored key: everything but the secret.
#[derive(Debug, Clone, PartialEq, Eq)]
pub struct ApiKey {
    /// The key's public identifier, the middle part of the key.
    pub id: String,
    /// The caller the key stands for.
    pub principal: String,
    /// The tenant the key acts within; empty for none.
    pub tenant: String,
    /// The entitlements the key carries.
    pub scopes: Vec<String>,
    /// When the key was issued.
    pub created_at: SystemTime,
    /// When the key stops working, if it does.
    pub expires_at: Option<SystemTime>,
    /// When the key was revoked, if it was.
    pub revoked_at: Option<SystemTime>,
    /// A human label.
    pub name: String,
    /// Free-form key/value metadata.
    pub labels: BTreeMap<String, String>,
    /// The last successful verification, when tracking is on
    /// ([`ApiKeys::with_last_used`]).
    pub last_used_at: Option<SystemTime>,
    /// Hex SHA-256 of the domain-separated id and secret.
    pub hash: String,
}

impl ApiKey {
    /// The claims an [`AuthFunc`] hands the transports.
    pub fn claims(&self) -> Claims {
        Claims {
            subject: self.principal.clone(),
            tenant: self.tenant.clone(),
            scopes: self.scopes.clone(),
        }
    }

    /// The key presented as a verified token: its principal, tenant
    /// and scopes, its id as `jti`, issued at its creation, expiring
    /// when it does.
    pub fn token(&self) -> Token {
        Token {
            subject: self.principal.clone(),
            tenant: self.tenant.clone(),
            scopes: self.scopes.clone(),
            issuer: String::new(),
            audience: Vec::new(),
            id: self.id.clone(),
            key_id: String::new(),
            issued_at: Some(self.created_at),
            not_before: None,
            expiry: self.expires_at,
            raw: serde_json::Map::new(),
        }
    }
}

/// The JSON record, byte-compatible with Go's `APIKey`.
#[derive(Serialize, Deserialize)]
struct Record {
    id: String,
    principal: String,
    #[serde(default, skip_serializing_if = "String::is_empty")]
    tenant: String,
    #[serde(default, skip_serializing_if = "Vec::is_empty")]
    scopes: Vec<String>,
    created_at: String,
    #[serde(default, skip_serializing_if = "Option::is_none")]
    expires_at: Option<String>,
    #[serde(default, skip_serializing_if = "Option::is_none")]
    revoked_at: Option<String>,
    #[serde(default, skip_serializing_if = "String::is_empty")]
    name: String,
    #[serde(default, skip_serializing_if = "BTreeMap::is_empty")]
    labels: BTreeMap<String, String>,
    #[serde(default, skip_serializing_if = "Option::is_none")]
    last_used_at: Option<String>,
    hash: String,
}

impl Record {
    fn from_key(k: &ApiKey) -> Self {
        let f = |t: &Option<SystemTime>| t.map(rfc3339::format);
        Self {
            id: k.id.clone(),
            principal: k.principal.clone(),
            tenant: k.tenant.clone(),
            scopes: k.scopes.clone(),
            created_at: rfc3339::format(k.created_at),
            expires_at: f(&k.expires_at),
            revoked_at: f(&k.revoked_at),
            name: k.name.clone(),
            labels: k.labels.clone(),
            last_used_at: f(&k.last_used_at),
            hash: k.hash.clone(),
        }
    }

    fn into_key(self) -> Result<ApiKey, AuthnError> {
        let bad = |what: &str| AuthnError::Config(format!("api key {}: bad {what}", self.id));
        let opt = |s: &Option<String>, what: &str| match s.as_deref() {
            None | Some("") => Ok(None),
            Some(v) => rfc3339::parse(v).ok_or_else(|| bad(what)),
        };
        let created_at = rfc3339::parse(&self.created_at)
            .ok_or_else(|| bad("created_at"))?
            .unwrap_or(SystemTime::UNIX_EPOCH);
        Ok(ApiKey {
            expires_at: opt(&self.expires_at, "expires_at")?,
            revoked_at: opt(&self.revoked_at, "revoked_at")?,
            last_used_at: opt(&self.last_used_at, "last_used_at")?,
            created_at,
            id: self.id,
            principal: self.principal,
            tenant: self.tenant,
            scopes: self.scopes,
            name: self.name,
            labels: self.labels,
            hash: self.hash,
        })
    }
}

/// What [`ApiKeys::create`] issues a key for.
#[derive(Debug, Clone, Default)]
pub struct NewApiKey {
    /// The caller the key stands for; required.
    pub principal: String,
    /// The tenant the key acts within.
    pub tenant: String,
    /// The entitlements; each one non-empty word without spaces.
    pub scopes: Vec<String>,
    /// When non-zero, sets the key's expiry; zero never expires.
    pub ttl: Duration,
    /// A human label.
    pub name: String,
    /// Free-form key/value metadata.
    pub labels: BTreeMap<String, String>,
}

type DynStore = Box<dyn Store + Send>;

/// Issues, lists, revokes and verifies API keys held in a kv store.
/// Cheap to clone; the clones share the store.
#[derive(Clone)]
pub struct ApiKeys {
    store: Arc<Mutex<DynStore>>,
    now: Option<NowFn>,
    check: Option<CheckFn>,
    prefix: String,
    domain: String,
    track_last_used: bool,
}

impl ApiKeys {
    /// API keys over `store`. The store is shared with whatever else
    /// opened it: `token key create` in one process, the serving
    /// process in another.
    pub fn new(store: impl Store + Send + 'static) -> Self {
        Self {
            store: Arc::new(Mutex::new(Box::new(store))),
            now: None,
            check: None,
            prefix: API_KEY_PREFIX.to_string(),
            domain: API_KEY_HASH_DOMAIN.to_string(),
            track_last_used: false,
        }
    }

    /// Uses `now` as the clock instead of the system clock.
    pub fn with_clock(mut self, now: NowFn) -> Self {
        self.now = Some(now);
        self
    }

    /// Runs `check` on every key that passed verification, as
    /// `Options::check` runs on a verified token: the key is presented
    /// as its [`ApiKey::token`], and an error refuses it. The
    /// revocation hook a deny list shared with the token verifiers
    /// plugs into.
    pub fn with_check(mut self, check: CheckFn) -> Self {
        self.check = Some(check);
        self
    }

    /// A branded key prefix (`acme` for `acme_<id>_<secret>`). It must
    /// be a single word without `_`.
    pub fn with_prefix(mut self, prefix: &str) -> Result<Self, AuthnError> {
        if prefix.is_empty() || prefix.contains(['_', ' ']) {
            return Err(AuthnError::Config(format!(
                "api key prefix {prefix:?}: one non-empty word without '_'"
            )));
        }
        self.prefix = prefix.to_string();
        Ok(self)
    }

    /// A hash domain other than [`API_KEY_HASH_DOMAIN`], so existing
    /// hashes keep verifying when a deployment moves onto these keys.
    pub fn with_hash_domain(mut self, domain: &str) -> Self {
        self.domain = domain.to_string();
        self
    }

    /// Records [`ApiKey::last_used_at`] on each successful
    /// verification. Off by default: it turns every read into a write,
    /// and the read-modify-write is not atomic against a concurrent
    /// revoke from another process (the key is re-read first and left
    /// alone when revoked, narrowing but not closing the window).
    pub fn with_last_used(mut self, on: bool) -> Self {
        self.track_last_used = on;
        self
    }

    fn now(&self) -> Result<SystemTime, AuthnError> {
        match &self.now {
            Some(n) => Ok(n()),
            #[cfg(not(target_arch = "wasm32"))]
            None => Ok(SystemTime::now()),
            #[cfg(target_arch = "wasm32")]
            None => Err(AuthnError::Config("no clock".into())),
        }
    }

    /// Issues a key and returns it with its record. The key is shown
    /// once: only its hash is stored.
    pub fn create(&self, spec: NewApiKey) -> Result<(String, ApiKey), AuthnError> {
        if spec.principal.trim().is_empty() {
            return Err(AuthnError::Config("an api key needs a principal".into()));
        }
        for s in &spec.scopes {
            if s.is_empty() || s.contains([' ', '\t', '\r', '\n']) {
                return Err(AuthnError::Config(format!(
                    "scope {s:?}: a scope is one non-empty word with no spaces"
                )));
            }
        }
        let mut idb = [0u8; ID_BYTES];
        let mut secret = [0u8; SECRET_BYTES];
        getrandom::fill(&mut idb)
            .and_then(|()| getrandom::fill(&mut secret))
            .map_err(|e| AuthnError::Config(format!("no random source: {e}")))?;
        let (id, sec) = (hex(&idb), hex(&secret));
        let now = self.now()?;
        let rec = ApiKey {
            hash: self.hash(&id, &sec),
            id: id.clone(),
            principal: spec.principal,
            tenant: spec.tenant,
            scopes: spec.scopes,
            created_at: now,
            expires_at: (!spec.ttl.is_zero()).then(|| now + spec.ttl),
            revoked_at: None,
            name: spec.name,
            labels: spec.labels,
            last_used_at: None,
        };
        self.put(&rec)?;
        Ok((format!("{}_{id}_{sec}", self.prefix), rec))
    }

    /// The stored key with `id`, `None` when the store lacks it.
    pub fn get(&self, id: &str) -> Result<Option<ApiKey>, AuthnError> {
        let data = {
            let store = self.lock();
            store
                .get(format!("{STORE_PREFIX}{id}").as_bytes())
                .map_err(|e| AuthnError::KeySetUnavailable(e.to_string()))?
        };
        let Some(data) = data else { return Ok(None) };
        let rec: Record = serde_json::from_slice(&data)
            .map_err(|e| AuthnError::Config(format!("api key {id}: {e}")))?;
        rec.into_key().map(Some)
    }

    /// Every stored key, newest first.
    pub fn list(&self) -> Result<Vec<ApiKey>, AuthnError> {
        let ids = {
            let store = self.lock();
            store
                .list(STORE_PREFIX.as_bytes())
                .map_err(|e| AuthnError::KeySetUnavailable(e.to_string()))?
        };
        let mut out = Vec::with_capacity(ids.len());
        for key in ids {
            let key = String::from_utf8_lossy(&key).into_owned();
            if let Some(rec) = self.get(key.trim_start_matches(STORE_PREFIX))? {
                out.push(rec);
            }
        }
        out.sort_by(|a, b| {
            b.created_at
                .cmp(&a.created_at)
                .then_with(|| a.id.cmp(&b.id))
        });
        Ok(out)
    }

    /// Marks the key with `id` revoked; it stops working at once. The
    /// record stays, so the audit trail still names whose key it was.
    /// Revoking a revoked key is not an error.
    pub fn revoke(&self, id: &str) -> Result<ApiKey, AuthnError> {
        let mut rec = self
            .get(id)?
            .ok_or_else(|| AuthnError::ApiKeyNotFound(id.to_string()))?;
        if rec.revoked_at.is_none() {
            rec.revoked_at = Some(self.now()?);
            self.put(&rec)?;
        }
        Ok(rec)
    }

    /// Checks `raw`, a full API key, and returns its record. Refusals
    /// are [`AuthnError::InvalidToken`]: a malformed key, an unknown
    /// id, a secret that does not match, a revoked or expired key, one
    /// the [`with_check`](Self::with_check) hook refuses. A store that
    /// cannot be read is [`AuthnError::KeySetUnavailable`]: no verdict.
    pub fn verify(&self, raw: &str) -> Result<ApiKey, AuthnError> {
        let raw = raw.trim();
        if raw.is_empty() {
            return Err(AuthnError::NoToken(None));
        }
        let parts: Vec<&str> = raw.split('_').collect();
        let well_formed = parts.len() == 3
            && parts[0] == self.prefix
            && is_hex(parts[1], ID_BYTES)
            && is_hex(parts[2], SECRET_BYTES);
        if !well_formed {
            return Err(AuthnError::invalid(format!(
                "not a {} api key",
                self.prefix
            )));
        }
        let (id, sec) = (parts[1], parts[2]);
        let rec = self.get(id).map_err(|e| match e {
            AuthnError::KeySetUnavailable(_) => e,
            other => AuthnError::KeySetUnavailable(other.to_string()),
        })?;
        let want = self.hash(id, sec);
        let Some(mut rec) = rec else {
            // Spend the comparison anyway, so an unknown id and a wrong
            // secret take the same time.
            let _ = want.as_bytes().ct_eq(want.as_bytes());
            return Err(AuthnError::invalid("unknown api key"));
        };
        if !bool::from(want.as_bytes().ct_eq(rec.hash.as_bytes())) {
            return Err(AuthnError::invalid("unknown api key"));
        }
        let now = self.now()?;
        if rec.revoked_at.is_some() {
            return Err(AuthnError::invalid(format!("api key {id} was revoked")));
        }
        if rec.expires_at.is_some_and(|exp| now >= exp) {
            return Err(AuthnError::invalid(format!("api key {id} expired")));
        }
        if let Some(check) = &self.check {
            check(&rec.token()).map_err(AuthnError::InvalidToken)?;
        }
        if self.track_last_used {
            // Re-read so a revoke that landed since is not overwritten.
            if let Some(mut fresh) = self.get(id)? {
                if fresh.revoked_at.is_none() {
                    fresh.last_used_at = Some(now);
                    self.put(&fresh)?;
                    rec.last_used_at = Some(now);
                }
            }
        }
        Ok(rec)
    }

    /// The keys as an [`AuthFunc`]: the key is read from the
    /// `X-API-Key` header, else from `Authorization: Bearer`, and the
    /// caller is its principal, tenant and scopes.
    pub fn auth_func(&self) -> AuthFunc {
        let keys = self.clone();
        Arc::new(move |creds| {
            let header = creds.api_key.map(str::trim).filter(|s| !s.is_empty());
            let raw = match header {
                Some(h) => h,
                None => bearer_token(creds.authorization).map_err(|e| match e {
                    AuthnError::NoToken(d) => {
                        let hint = format!("send it as {API_KEY_HEADER} or Authorization: Bearer");
                        AuthnError::NoToken(Some(match d {
                            Some(d) => format!("{d}: {hint}"),
                            None => hint,
                        }))
                    }
                    other => other,
                })?,
            };
            Ok(keys.verify(raw)?.claims())
        })
    }

    fn lock(&self) -> std::sync::MutexGuard<'_, DynStore> {
        self.store.lock().unwrap_or_else(|p| p.into_inner())
    }

    fn put(&self, rec: &ApiKey) -> Result<(), AuthnError> {
        let data = serde_json::to_vec(&Record::from_key(rec))
            .map_err(|e| AuthnError::Config(e.to_string()))?;
        self.lock()
            .put(format!("{STORE_PREFIX}{}", rec.id).as_bytes(), &data)
            .map_err(|e| AuthnError::KeySetUnavailable(e.to_string()))
    }

    /// The stored hash of a key's secret.
    fn hash(&self, id: &str, secret: &str) -> String {
        let mut h = Sha256::new();
        h.update(self.domain.as_bytes());
        h.update([0]);
        h.update(id.as_bytes());
        h.update([0]);
        h.update(secret.as_bytes());
        hex(&h.finalize())
    }
}

fn hex(b: &[u8]) -> String {
    let mut s = String::with_capacity(b.len() * 2);
    for x in b {
        s.push_str(&format!("{x:02x}"));
    }
    s
}

/// Whether `s` is `n` bytes of lowercase hex.
fn is_hex(s: &str, n: usize) -> bool {
    s.len() == 2 * n && s.bytes().all(|c| matches!(c, b'0'..=b'9' | b'a'..=b'f'))
}
