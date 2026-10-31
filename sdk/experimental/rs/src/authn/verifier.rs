use std::fmt;
use std::sync::Arc;
use std::time::{Duration, SystemTime, UNIX_EPOCH};

use base64::engine::general_purpose::URL_SAFE_NO_PAD;
use base64::Engine;
use serde_json::{Map, Value};

use super::claims::{scopes_of, Claims};
use super::error::AuthnError;
use super::key::{keys_from_jwks, Alg, Key};
use super::provider;
use super::AuthFunc;

/// The tolerance [`Options::clock_skew`] defaults to: how far the
/// verifier's clock may disagree with the issuer's on `exp`, `nbf` and
/// `iat`.
pub const DEFAULT_CLOCK_SKEW: Duration = Duration::from_secs(60);

/// The claim the tenant is read from unless [`Options::tenant_claim`]
/// names another.
pub const DEFAULT_TENANT_CLAIM: &str = "tenant";

/// The revocation hook: look the token's `jti`, subject or issue time
/// up in a deny list. A returned message refuses the token.
pub type CheckFn = Arc<dyn Fn(&Token) -> Result<(), String> + Send + Sync>;

/// The clock verification reads.
pub type NowFn = Arc<dyn Fn() -> SystemTime + Send + Sync>;

/// The checks every verifier applies beyond the signature.
#[derive(Clone, Default)]
pub struct Options {
    /// When set, must equal the token's `iss`.
    pub issuer: Option<String>,
    /// When non-empty, must intersect the token's `aud`.
    pub audience: Vec<String>,
    /// Tolerance on `exp`, `nbf` and `iat`. `None` means
    /// [`DEFAULT_CLOCK_SKEW`]; `Some(Duration::ZERO)` tolerates none.
    pub clock_skew: Option<Duration>,
    /// The claim holding the tenant; `None` means
    /// [`DEFAULT_TENANT_CLAIM`].
    pub tenant_claim: Option<String>,
    /// Runs after every other check passed; an error refuses the token.
    pub check: Option<CheckFn>,
    /// The clock. Default `SystemTime::now`, except on
    /// wasm32-unknown-unknown, which has none: a verifier without one
    /// refuses every token there.
    pub now: Option<NowFn>,
}

impl fmt::Debug for Options {
    fn fmt(&self, f: &mut fmt::Formatter<'_>) -> fmt::Result {
        f.debug_struct("Options")
            .field("issuer", &self.issuer)
            .field("audience", &self.audience)
            .field("clock_skew", &self.clock_skew)
            .field("tenant_claim", &self.tenant_claim)
            .field("check", &self.check.as_ref().map(|_| ".."))
            .field("now", &self.now.as_ref().map(|_| ".."))
            .finish()
    }
}

impl Options {
    pub(crate) fn now(&self) -> Result<SystemTime, AuthnError> {
        if let Some(now) = &self.now {
            return Ok(now());
        }
        #[cfg(not(target_arch = "wasm32"))]
        {
            Ok(SystemTime::now())
        }
        #[cfg(target_arch = "wasm32")]
        {
            Err(AuthnError::invalid("no clock: set Options::now on wasm32"))
        }
    }

    fn skew(&self) -> Duration {
        self.clock_skew.unwrap_or(DEFAULT_CLOCK_SKEW)
    }

    fn tenant_claim(&self) -> &str {
        self.tenant_claim.as_deref().unwrap_or(DEFAULT_TENANT_CLAIM)
    }
}

/// A verified token.
#[derive(Debug, Clone, PartialEq)]
pub struct Token {
    /// `sub`: the principal.
    pub subject: String,
    /// The tenant claim's value, empty when absent.
    pub tenant: String,
    /// The entitlements the token carries.
    pub scopes: Vec<String>,
    /// `iss`.
    pub issuer: String,
    /// `aud`.
    pub audience: Vec<String>,
    /// `jti`.
    pub id: String,
    /// The kid of the key that verified the signature.
    pub key_id: String,
    /// `iat`; `None` when absent.
    pub issued_at: Option<SystemTime>,
    /// `nbf`; `None` when absent.
    pub not_before: Option<SystemTime>,
    /// `exp`; a token without one is refused.
    pub expiry: Option<SystemTime>,
    /// Every claim the payload carries.
    pub raw: Map<String, Value>,
}

impl Token {
    /// The claims an [`AuthFunc`] hands the transports.
    pub fn claims(&self) -> Claims {
        Claims {
            subject: self.subject.clone(),
            tenant: self.tenant.clone(),
            scopes: self.scopes.clone(),
        }
    }
}

/// Yields the candidate keys for a token's `kid`; an empty kid yields
/// every signing key. A remote key set (JWKS, OIDC) implements this
/// with its own fetch and cache and answers
/// [`AuthnError::KeySetUnavailable`] when it has no set to look in.
pub trait KeySource: Send + Sync {
    /// The keys `kid` selects.
    fn lookup(&self, kid: &str) -> Result<Vec<Key>, AuthnError>;
}

/// A fixed key set: the tool's own identity keypair plus any key
/// files. A retired key left in the set keeps the tokens it signed
/// valid while new ones carry the new kid.
struct StaticKeys(Vec<Key>);

impl KeySource for StaticKeys {
    fn lookup(&self, kid: &str) -> Result<Vec<Key>, AuthnError> {
        let by_kid = select(&self.0, kid);
        if !by_kid.is_empty() {
            return Ok(by_kid);
        }
        Ok(select(&self.0, ""))
    }
}

/// The keys among `keys` that `kid` selects: those with that kid, or
/// every one when `kid` is empty.
pub fn select(keys: &[Key], kid: &str) -> Vec<Key> {
    keys.iter()
        .filter(|k| kid.is_empty() || k.id == kid)
        .cloned()
        .collect()
}

/// Verifies bearer tokens against one key source.
#[derive(Clone)]
pub struct Verifier {
    keys: Arc<dyn KeySource>,
    opts: Options,
}

impl fmt::Debug for Verifier {
    fn fmt(&self, f: &mut fmt::Formatter<'_>) -> fmt::Result {
        f.debug_struct("Verifier")
            .field("opts", &self.opts)
            .finish()
    }
}

impl Verifier {
    /// A verifier for tokens signed by one of `keys`: the token's kid
    /// selects the key, and a token whose kid no key has (an external
    /// signer's own naming) or that names none is tried against each;
    /// the signature decides. Rotation is adding the new key and, until
    /// the tokens the old one signed have expired, keeping the old one.
    pub fn new(keys: Vec<Key>, opts: Options) -> Result<Self, AuthnError> {
        if keys.is_empty() {
            return Err(AuthnError::Config("no key to verify with".into()));
        }
        Ok(Self::with_source(Arc::new(StaticKeys(keys)), opts))
    }

    /// A verifier over the signing keys of a JWKS document the caller
    /// fetched.
    pub fn from_jwks(doc: &[u8], opts: Options) -> Result<Self, AuthnError> {
        Self::new(keys_from_jwks(doc)?, opts)
    }

    /// A verifier over any [`KeySource`].
    pub fn with_source(keys: Arc<dyn KeySource>, opts: Options) -> Self {
        Self { keys, opts }
    }

    /// Checks `raw`, a compact JWS, and returns the token it carries.
    /// Every refusal is [`AuthnError::InvalidToken`], except an empty
    /// token ([`AuthnError::NoToken`]) and a key source that could not
    /// be read ([`AuthnError::KeySetUnavailable`]).
    pub fn verify(&self, raw: &str) -> Result<Token, AuthnError> {
        let raw = raw.trim();
        if raw.is_empty() {
            return Err(AuthnError::NoToken(None));
        }
        let jws = Jws::parse(raw)?;
        let keys = self.keys.lookup(&jws.kid)?;
        if keys.is_empty() {
            return Err(AuthnError::invalid(if jws.kid.is_empty() {
                "no trusted key".to_string()
            } else {
                format!("no trusted key has kid {:?}", jws.kid)
            }));
        }
        let used = keys
            .iter()
            .filter(|k| k.algorithm.is_none_or(|a| a == jws.alg))
            .find(|k| provider::verify(jws.alg, &k.public, jws.signing_input, &jws.signature))
            .ok_or_else(|| AuthnError::invalid("signature verifies with no trusted key"))?;

        let payload = jws.payload()?;
        let std = StdClaims::read(&payload)?;
        let exp = std
            .exp
            .ok_or_else(|| AuthnError::invalid("no exp: a token must expire"))?;

        if let Some(want) = &self.opts.issuer {
            if *want != std.iss {
                return Err(AuthnError::invalid(format!(
                    "issuer {:?} is not {:?}",
                    std.iss, want
                )));
            }
        }
        let now = unix_secs(self.opts.now()?);
        let skew = self.opts.skew().as_secs_f64();
        if std.nbf.is_some_and(|nbf| now + skew < nbf) {
            return Err(AuthnError::invalid("token not valid yet (nbf)"));
        }
        if now - skew > exp {
            return Err(AuthnError::invalid("token is expired (exp)"));
        }
        if std.iat.is_some_and(|iat| now + skew < iat) {
            return Err(AuthnError::invalid("token issued in the future (iat)"));
        }
        if !self.opts.audience.is_empty() && !audience_matches(&std.aud, &self.opts.audience) {
            return Err(AuthnError::invalid(format!(
                "audience [{}] does not include {}",
                std.aud.join(" "),
                self.opts.audience.join(" or ")
            )));
        }
        if std.sub.is_empty() {
            return Err(AuthnError::invalid("no sub: the token names no principal"));
        }

        let value = Value::Object(payload);
        let mut token = Token {
            subject: std.sub,
            tenant: tenant_of(&value, self.opts.tenant_claim()),
            scopes: scopes_of(&value),
            issuer: std.iss,
            audience: std.aud,
            id: std.jti,
            key_id: if used.id.is_empty() {
                jws.kid
            } else {
                used.id.clone()
            },
            issued_at: std.iat.map(from_unix_secs),
            not_before: std.nbf.map(from_unix_secs),
            expiry: Some(from_unix_secs(exp)),
            raw: Map::new(),
        };
        if let Value::Object(m) = value {
            token.raw = m;
        }
        if let Some(check) = &self.opts.check {
            check(&token).map_err(AuthnError::InvalidToken)?;
        }
        Ok(token)
    }

    /// The verifier as an [`AuthFunc`]: it reads the bearer token from
    /// the `Authorization` header and returns the token's [`Claims`].
    pub fn auth_func(&self) -> AuthFunc {
        let v = self.clone();
        Arc::new(move |creds| {
            let raw = super::bearer_token(creds.authorization)?;
            Ok(v.verify(raw)?.claims())
        })
    }
}

fn tenant_of(claims: &Value, name: &str) -> String {
    match claims.get(name) {
        Some(Value::String(s)) => s.clone(),
        Some(Value::Number(n)) => n.to_string(),
        _ => String::new(),
    }
}

/// Whether a token's `aud` names one of `want`. Values compare
/// exactly, except that a trailing slash is ignored: an RFC 8707
/// resource indicator and the `aud` an issuer mints from it disagree
/// on it routinely, while scheme, host, port and the rest of the path
/// stay strict.
fn audience_matches(aud: &[String], want: &[String]) -> bool {
    want.iter().any(|w| {
        aud.iter()
            .any(|a| a == w || a.trim_end_matches('/') == w.trim_end_matches('/'))
    })
}

fn unix_secs(t: SystemTime) -> f64 {
    match t.duration_since(UNIX_EPOCH) {
        Ok(d) => d.as_secs_f64(),
        Err(e) => -e.duration().as_secs_f64(),
    }
}

fn from_unix_secs(s: f64) -> SystemTime {
    if s >= 0.0 {
        UNIX_EPOCH + Duration::from_secs_f64(s)
    } else {
        UNIX_EPOCH - Duration::from_secs_f64(-s)
    }
}

/// The registered claims verification reads.
struct StdClaims {
    iss: String,
    sub: String,
    aud: Vec<String>,
    jti: String,
    exp: Option<f64>,
    nbf: Option<f64>,
    iat: Option<f64>,
}

/// Beyond this many seconds (2^62) a NumericDate is refused: far past
/// any real timestamp, and safe against overflow in later arithmetic.
const MAX_NUMERIC_DATE: f64 = 4_611_686_018_427_387_904.0;

impl StdClaims {
    fn read(p: &Map<String, Value>) -> Result<Self, AuthnError> {
        let malformed = |what: &str| AuthnError::invalid(format!("malformed claims: {what}"));
        let string = |k: &str| match p.get(k) {
            None | Some(Value::Null) => Ok(String::new()),
            Some(Value::String(s)) => Ok(s.clone()),
            _ => Err(malformed(&format!("{k} is not a string"))),
        };
        let date = |k: &str| match p.get(k) {
            None | Some(Value::Null) => Ok(None),
            Some(Value::Number(n)) => match n.as_f64() {
                Some(f) if f.is_finite() && f.abs() < MAX_NUMERIC_DATE => Ok(Some(f)),
                _ => Err(malformed(&format!("{k} is out of range"))),
            },
            _ => Err(malformed(&format!("{k} is not a number"))),
        };
        let aud = match p.get("aud") {
            None | Some(Value::Null) => Vec::new(),
            Some(Value::String(s)) => vec![s.clone()],
            Some(Value::Array(a)) => a
                .iter()
                .map(|v| {
                    v.as_str()
                        .map(str::to_string)
                        .ok_or_else(|| malformed("aud holds a non-string"))
                })
                .collect::<Result<_, _>>()?,
            _ => return Err(malformed("aud is not a string or list")),
        };
        Ok(Self {
            iss: string("iss")?,
            sub: string("sub")?,
            aud,
            jti: string("jti")?,
            exp: date("exp")?,
            nbf: date("nbf")?,
            iat: date("iat")?,
        })
    }
}

/// A compact JWS, split and decoded but not yet verified.
struct Jws<'a> {
    alg: Alg,
    kid: String,
    signing_input: &'a [u8],
    payload_b64: &'a str,
    signature: Vec<u8>,
}

impl<'a> Jws<'a> {
    fn parse(raw: &'a str) -> Result<Self, AuthnError> {
        let mut parts = raw.split('.');
        let (Some(h), Some(p), Some(s), None) =
            (parts.next(), parts.next(), parts.next(), parts.next())
        else {
            return Err(AuthnError::invalid(
                "compact JWS format must have three parts",
            ));
        };
        let header = decode_b64(h, "header")?;
        let header: Map<String, Value> = serde_json::from_slice(&header)
            .map_err(|_| AuthnError::invalid("malformed header: not a JSON object"))?;
        let alg_name = match header.get("alg") {
            Some(Value::String(a)) => a.as_str(),
            _ => return Err(AuthnError::invalid("header names no alg")),
        };
        let alg = Alg::parse(alg_name).ok_or_else(|| {
            AuthnError::invalid(format!("unexpected signature algorithm {alg_name:?}"))
        })?;
        // RFC 7515 4.1.11: a verifier that does not understand a
        // critical extension must refuse the token. None are understood.
        if header.contains_key("crit") {
            return Err(AuthnError::invalid("unsupported critical header"));
        }
        let kid = match header.get("kid") {
            None | Some(Value::Null) => String::new(),
            Some(Value::String(k)) => k.clone(),
            _ => return Err(AuthnError::invalid("kid is not a string")),
        };
        let signature = decode_b64(s, "signature")?;
        let signing_input = &raw.as_bytes()[..h.len() + 1 + p.len()];
        Ok(Self {
            alg,
            kid,
            signing_input,
            payload_b64: p,
            signature,
        })
    }

    fn payload(&self) -> Result<Map<String, Value>, AuthnError> {
        let bytes = decode_b64(self.payload_b64, "payload")?;
        serde_json::from_slice(&bytes)
            .map_err(|_| AuthnError::invalid("malformed claims: payload is not a JSON object"))
    }
}

fn decode_b64(s: &str, what: &str) -> Result<Vec<u8>, AuthnError> {
    URL_SAFE_NO_PAD
        .decode(s)
        .map_err(|_| AuthnError::invalid(format!("malformed {what}: not base64url")))
}
