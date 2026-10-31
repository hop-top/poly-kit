//! EdDSA JWTs: `base64url(header).base64url(claims).base64url(sig)`, the
//! header `{"alg":"EdDSA","kid":..,"typ":"JWT"}`, no padding.
//!
//! Verification checks, in Go's order: three parts, signature, payload
//! decode and parse, then `exp` (expired only when `now > exp`, and only
//! when `exp > 0`). `iat`/`nbf`/`alg`/`typ`/`kid` are not checked, as in
//! Go. Issuer and audience are checked only through [`VerifyOptions`].
//!
//! The clock is a parameter on the `_at` functions (unix seconds), which
//! is the only form on wasm32; the clock-reading forms are compiled off
//! wasm32-unknown-unknown.

use base64::alphabet;
use base64::engine::{DecodePaddingMode, GeneralPurpose, GeneralPurposeConfig};
use base64::Engine;
use ed25519_dalek::{Signature, Verifier, VerifyingKey};
use serde::ser::{Serialize, Serializer};
use serde_json::Value;

use super::{IdentityError, Keypair, PublicKey};

/// RawURLEncoding: URL-safe alphabet, no padding written or accepted,
/// trailing bits tolerated (Go does not enforce them).
const B64: GeneralPurpose = GeneralPurpose::new(
    &alphabet::URL_SAFE,
    GeneralPurposeConfig::new()
        .with_encode_padding(false)
        .with_decode_padding_mode(DecodePaddingMode::RequireNone)
        .with_decode_allow_trailing_bits(true),
);

/// The JWT `aud` claim: one string or an array of strings (RFC 7519
/// §4.1.3). A single element serialises as a string, any other count as
/// an array; both forms parse.
#[derive(Debug, Clone, PartialEq, Eq, Default)]
pub struct Audience(pub Vec<String>);

impl Audience {
    /// True when there are no audiences (the claim is then omitted).
    pub fn is_empty(&self) -> bool {
        self.0.is_empty()
    }

    /// True when `aud` is one of the audiences.
    pub fn contains(&self, aud: &str) -> bool {
        self.0.iter().any(|a| a == aud)
    }
}

impl Serialize for Audience {
    fn serialize<S: Serializer>(&self, s: S) -> Result<S::Ok, S::Error> {
        match self.0.as_slice() {
            [one] => s.serialize_str(one),
            many => many.serialize(s),
        }
    }
}

/// JWT claims. `scopes` and `scope` carry the same set: `scopes` as a
/// list, `scope` space-delimited (RFC 8693), the form OAuth 2.0 resource
/// servers read; a signer sets either or both.
#[derive(Debug, Clone, PartialEq, Eq, Default, serde::Serialize)]
pub struct Claims {
    /// `sub`.
    #[serde(rename = "sub")]
    pub subject: String,
    /// `iss`.
    #[serde(rename = "iss", skip_serializing_if = "String::is_empty")]
    pub issuer: String,
    /// `kid`; [`Keypair::sign_jwt`] overwrites it with the key id.
    #[serde(rename = "kid", skip_serializing_if = "String::is_empty")]
    pub key_id: String,
    /// `aud`.
    #[serde(rename = "aud", skip_serializing_if = "Audience::is_empty")]
    pub audience: Audience,
    /// `tenant`.
    #[serde(skip_serializing_if = "String::is_empty")]
    pub tenant: String,
    /// `scopes`.
    #[serde(skip_serializing_if = "Vec::is_empty")]
    pub scopes: Vec<String>,
    /// `scope`.
    #[serde(skip_serializing_if = "String::is_empty")]
    pub scope: String,
    /// `iat`, unix seconds. Always written.
    #[serde(rename = "iat")]
    pub issued_at: i64,
    /// `exp`, unix seconds; 0 means none.
    #[serde(rename = "exp", skip_serializing_if = "is_zero")]
    pub expires_at: i64,
    /// `nonce`.
    #[serde(skip_serializing_if = "String::is_empty")]
    pub nonce: String,
}

fn is_zero(n: &i64) -> bool {
    *n == 0
}

/// Optional issuer/audience checks for [`verify_jwt_with_options`]; an
/// empty field is not checked.
#[derive(Debug, Clone, PartialEq, Eq, Default)]
pub struct VerifyOptions {
    /// Required `iss`.
    pub issuer: String,
    /// An audience `aud` must contain.
    pub audience: String,
}

impl Keypair {
    /// Signs `claims` into a JWT. `kid` is set to [`Keypair::public_key_id`]
    /// in the header and in the claims.
    pub fn sign_jwt(&self, mut claims: Claims) -> String {
        let kid = self.public_key_id();
        claims.key_id.clone_from(&kid);
        let header = format!(
            r#"{{"alg":"EdDSA","kid":{},"typ":"JWT"}}"#,
            Value::String(kid)
        );
        let payload = serde_json::to_vec(&claims).expect("claims serialise to JSON");
        let input = format!("{}.{}", B64.encode(header), B64.encode(payload));
        let sig = self.sign(input.as_bytes());
        format!("{input}.{}", B64.encode(sig))
    }
}

/// Verifies `token` under `pub_key` at unix time `now`, returning its
/// claims.
///
/// # Errors
///
/// [`IdentityError::MalformedJwt`], [`DecodeSignature`], [`InvalidSignature`],
/// [`DecodePayload`], [`UnmarshalClaims`], [`AudienceFormat`] or
/// [`Expired`], in that order of checking.
///
/// [`DecodeSignature`]: IdentityError::DecodeSignature
/// [`InvalidSignature`]: IdentityError::InvalidSignature
/// [`DecodePayload`]: IdentityError::DecodePayload
/// [`UnmarshalClaims`]: IdentityError::UnmarshalClaims
/// [`AudienceFormat`]: IdentityError::AudienceFormat
/// [`Expired`]: IdentityError::Expired
pub fn verify_jwt_at(token: &str, pub_key: &PublicKey, now: i64) -> Result<Claims, IdentityError> {
    // Go SplitN(token, ".", 3): the third part keeps any further dots.
    let mut parts = token.splitn(3, '.');
    let (Some(h), Some(p), Some(s)) = (parts.next(), parts.next(), parts.next()) else {
        return Err(IdentityError::MalformedJwt);
    };

    let sig = decode(s).map_err(IdentityError::DecodeSignature)?;
    let input = &token[..h.len() + 1 + p.len()];
    if !verify_sig(pub_key, input.as_bytes(), &sig) {
        return Err(IdentityError::InvalidSignature);
    }

    let payload = decode(p).map_err(IdentityError::DecodePayload)?;
    let claims = parse_claims(&payload)?;

    if claims.expires_at > 0 && now > claims.expires_at {
        return Err(IdentityError::Expired);
    }
    Ok(claims)
}

/// [`verify_jwt_at`] at the system clock. Compiled off
/// wasm32-unknown-unknown, which has no clock.
///
/// # Errors
///
/// As [`verify_jwt_at`].
#[cfg(not(all(target_arch = "wasm32", target_os = "unknown")))]
pub fn verify_jwt(token: &str, pub_key: &PublicKey) -> Result<Claims, IdentityError> {
    verify_jwt_at(token, pub_key, unix_now())
}

/// [`verify_jwt_at`], then the issuer and audience checks of `opts`
/// (`None` checks nothing).
///
/// # Errors
///
/// As [`verify_jwt_at`], then [`IdentityError::IssuerMismatch`] or
/// [`IdentityError::AudienceMismatch`].
pub fn verify_jwt_with_options_at(
    token: &str,
    pub_key: &PublicKey,
    opts: Option<&VerifyOptions>,
    now: i64,
) -> Result<Claims, IdentityError> {
    let claims = verify_jwt_at(token, pub_key, now)?;
    let Some(opts) = opts else {
        return Ok(claims);
    };
    if !opts.issuer.is_empty() && claims.issuer != opts.issuer {
        return Err(IdentityError::IssuerMismatch {
            got: claims.issuer,
            want: opts.issuer.clone(),
        });
    }
    if !opts.audience.is_empty() && !claims.audience.contains(&opts.audience) {
        return Err(IdentityError::AudienceMismatch {
            got: format!("[{}]", claims.audience.0.join(" ")),
            want: opts.audience.clone(),
        });
    }
    Ok(claims)
}

/// [`verify_jwt_with_options_at`] at the system clock. Compiled off
/// wasm32-unknown-unknown.
///
/// # Errors
///
/// As [`verify_jwt_with_options_at`].
#[cfg(not(all(target_arch = "wasm32", target_os = "unknown")))]
pub fn verify_jwt_with_options(
    token: &str,
    pub_key: &PublicKey,
    opts: Option<&VerifyOptions>,
) -> Result<Claims, IdentityError> {
    verify_jwt_with_options_at(token, pub_key, opts, unix_now())
}

#[cfg(not(all(target_arch = "wasm32", target_os = "unknown")))]
fn unix_now() -> i64 {
    use std::time::{SystemTime, UNIX_EPOCH};
    match SystemTime::now().duration_since(UNIX_EPOCH) {
        Ok(d) => i64::try_from(d.as_secs()).unwrap_or(i64::MAX),
        Err(e) => -i64::try_from(e.duration().as_secs()).unwrap_or(i64::MAX),
    }
}

/// Ed25519 verification. A malformed key or signature is a failed
/// verification, never a panic (Go's `ed25519.Verify` returns false for a
/// wrong-length signature).
fn verify_sig(pub_key: &PublicKey, msg: &[u8], sig: &[u8]) -> bool {
    let Ok(key) = VerifyingKey::from_bytes(pub_key) else {
        return false;
    };
    let Ok(sig) = Signature::from_slice(sig) else {
        return false;
    };
    key.verify(msg, &sig).is_ok()
}

/// base64url without padding. Go's decoder skips `\r` and `\n`.
fn decode(s: &str) -> Result<Vec<u8>, String> {
    let r = if s.contains(['\r', '\n']) {
        let clean: String = s.chars().filter(|c| !matches!(c, '\r' | '\n')).collect();
        B64.decode(clean)
    } else {
        B64.decode(s)
    };
    r.map_err(|e| e.to_string())
}

/// Parses the claims object the way Go's `encoding/json` fills `Claims`:
/// keys match exactly or case-insensitively, unknown keys are ignored,
/// `null` leaves a field at its zero value, and a wrong JSON type is an
/// error. `null` for the whole payload is the zero `Claims`.
fn parse_claims(payload: &[u8]) -> Result<Claims, IdentityError> {
    let bad = |e: String| IdentityError::UnmarshalClaims(e);
    let v: Value = serde_json::from_slice(payload).map_err(|e| bad(e.to_string()))?;
    let obj = match v {
        Value::Null => return Ok(Claims::default()),
        Value::Object(o) => o,
        other => {
            return Err(bad(format!(
                "json: cannot unmarshal {} into Go value of type identity.Claims",
                kind(&other)
            )))
        }
    };

    // Go prefers an exact-case key over a case-folded one, whatever the
    // document order: apply the folded matches first, the exact ones last.
    let mut c = Claims::default();
    for exact in [false, true] {
        for (key, val) in &obj {
            let field = key.to_ascii_lowercase();
            if (*key == field) == exact {
                set(&mut c, &field, key, val)?;
            }
        }
    }
    Ok(c)
}

fn set(c: &mut Claims, field: &str, key: &str, val: &Value) -> Result<(), IdentityError> {
    match field {
        "sub" => c.subject = string(key, val)?,
        "iss" => c.issuer = string(key, val)?,
        "kid" => c.key_id = string(key, val)?,
        "aud" => c.audience = audience(val)?,
        "tenant" => c.tenant = string(key, val)?,
        "scopes" => c.scopes = strings(key, val)?,
        "scope" => c.scope = string(key, val)?,
        "iat" => c.issued_at = int(key, val)?,
        "exp" => c.expires_at = int(key, val)?,
        "nonce" => c.nonce = string(key, val)?,
        _ => {}
    }
    Ok(())
}

fn kind(v: &Value) -> &'static str {
    match v {
        Value::Null => "null",
        Value::Bool(_) => "bool",
        Value::Number(_) => "number",
        Value::String(_) => "string",
        Value::Array(_) => "array",
        Value::Object(_) => "object",
    }
}

fn type_err(key: &str, want: &str, v: &Value) -> IdentityError {
    IdentityError::UnmarshalClaims(format!(
        "json: cannot unmarshal {} into Go struct field Claims.{key} of type {want}",
        kind(v)
    ))
}

fn string(key: &str, v: &Value) -> Result<String, IdentityError> {
    match v {
        Value::Null => Ok(String::new()),
        Value::String(s) => Ok(s.clone()),
        other => Err(type_err(key, "string", other)),
    }
}

fn int(key: &str, v: &Value) -> Result<i64, IdentityError> {
    match v {
        Value::Null => Ok(0),
        Value::Number(n) => n.as_i64().ok_or_else(|| type_err(key, "int64", v)),
        other => Err(type_err(key, "int64", other)),
    }
}

fn strings(key: &str, v: &Value) -> Result<Vec<String>, IdentityError> {
    match v {
        Value::Null => Ok(Vec::new()),
        Value::Array(items) => items.iter().map(|i| string(key, i)).collect(),
        other => Err(type_err(key, "[]string", other)),
    }
}

/// A string, else an array of strings; anything else is the aud error.
/// Go quirk, kept: `null` unmarshals into the single-string form without
/// error, so `"aud":null` is `[""]`.
fn audience(v: &Value) -> Result<Audience, IdentityError> {
    match v {
        Value::Null => Ok(Audience(vec![String::new()])),
        Value::String(s) => Ok(Audience(vec![s.clone()])),
        Value::Array(items) => items
            .iter()
            .map(|i| match i {
                Value::Null => Ok(String::new()),
                Value::String(s) => Ok(s.clone()),
                other => Err(IdentityError::AudienceFormat(format!(
                    "json: cannot unmarshal {} into Go value of type string",
                    kind(other)
                ))),
            })
            .collect::<Result<_, _>>()
            .map(Audience),
        other => Err(IdentityError::AudienceFormat(format!(
            "json: cannot unmarshal {} into Go value of type []string",
            kind(other)
        ))),
    }
}
