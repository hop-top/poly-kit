use base64::engine::general_purpose::URL_SAFE_NO_PAD;
use base64::Engine;
use serde::Deserialize;
use sha2::{Digest, Sha256};

use super::error::AuthnError;
use super::provider;

/// The signature algorithms a token may use: asymmetric only. `none`
/// and HMAC are never accepted, so a shared secret cannot let every
/// verifier mint tokens.
#[derive(Debug, Clone, Copy, PartialEq, Eq, Hash)]
pub enum Alg {
    /// Ed25519 (`EdDSA`).
    EdDsa,
    /// ECDSA P-256 with SHA-256.
    Es256,
    /// ECDSA P-384 with SHA-384.
    Es384,
    /// ECDSA P-521 with SHA-512.
    Es512,
    /// RSASSA-PKCS1-v1_5 with SHA-256 (needs `authn-rsa`).
    Rs256,
    /// RSASSA-PKCS1-v1_5 with SHA-384 (needs `authn-rsa`).
    Rs384,
    /// RSASSA-PKCS1-v1_5 with SHA-512 (needs `authn-rsa`).
    Rs512,
    /// RSASSA-PSS with SHA-256 (needs `authn-rsa`).
    Ps256,
    /// RSASSA-PSS with SHA-384 (needs `authn-rsa`).
    Ps384,
    /// RSASSA-PSS with SHA-512 (needs `authn-rsa`).
    Ps512,
}

impl Alg {
    /// The `alg` header value.
    pub fn as_str(self) -> &'static str {
        match self {
            Self::EdDsa => "EdDSA",
            Self::Es256 => "ES256",
            Self::Es384 => "ES384",
            Self::Es512 => "ES512",
            Self::Rs256 => "RS256",
            Self::Rs384 => "RS384",
            Self::Rs512 => "RS512",
            Self::Ps256 => "PS256",
            Self::Ps384 => "PS384",
            Self::Ps512 => "PS512",
        }
    }

    /// Parses an `alg` header value; `None` for anything outside the
    /// accepted set, `none` and the HMAC family included.
    pub fn parse(s: &str) -> Option<Self> {
        Some(match s {
            "EdDSA" => Self::EdDsa,
            "ES256" => Self::Es256,
            "ES384" => Self::Es384,
            "ES512" => Self::Es512,
            "RS256" => Self::Rs256,
            "RS384" => Self::Rs384,
            "RS512" => Self::Rs512,
            "PS256" => Self::Ps256,
            "PS384" => Self::Ps384,
            "PS512" => Self::Ps512,
            _ => return None,
        })
    }
}

/// A public key a token's signature is checked against. Kit-owned:
/// the bytes are the standard encodings (a raw Ed25519 key, an SEC1
/// uncompressed EC point, big-endian RSA integers), so no crypto crate
/// type appears in the contract.
#[derive(Debug, Clone, PartialEq, Eq)]
pub enum VerifyKey {
    /// A raw 32-byte Ed25519 public key.
    Ed25519([u8; 32]),
    /// A P-256 point, SEC1 uncompressed (`0x04 || x || y`).
    P256(Vec<u8>),
    /// A P-384 point, SEC1 uncompressed.
    P384(Vec<u8>),
    /// A P-521 point, SEC1 uncompressed.
    P521(Vec<u8>),
    /// An RSA key: modulus and public exponent, big-endian, no leading
    /// zeros. Verifies only with the `authn-rsa` feature.
    Rsa {
        /// The modulus.
        n: Vec<u8>,
        /// The public exponent.
        e: Vec<u8>,
    },
}

impl VerifyKey {
    /// Whether `alg` is one this key can verify.
    pub fn supports(&self, alg: Alg) -> bool {
        matches!(
            (self, alg),
            (Self::Ed25519(_), Alg::EdDsa)
                | (Self::P256(_), Alg::Es256)
                | (Self::P384(_), Alg::Es384)
                | (Self::P521(_), Alg::Es512)
                | (
                    Self::Rsa { .. },
                    Alg::Rs256 | Alg::Rs384 | Alg::Rs512 | Alg::Ps256 | Alg::Ps384 | Alg::Ps512
                )
        )
    }

    /// RFC 7638 JWK thumbprint, base64url without padding.
    pub fn thumbprint(&self) -> String {
        let b64 = |b: &[u8]| URL_SAFE_NO_PAD.encode(b);
        let canonical = match self {
            Self::Ed25519(k) => {
                format!(r#"{{"crv":"Ed25519","kty":"OKP","x":"{}"}}"#, b64(k))
            }
            Self::P256(p) | Self::P384(p) | Self::P521(p) => {
                let crv = match self {
                    Self::P256(_) => "P-256",
                    Self::P384(_) => "P-384",
                    _ => "P-521",
                };
                let half = (p.len() - 1) / 2;
                format!(
                    r#"{{"crv":"{crv}","kty":"EC","x":"{}","y":"{}"}}"#,
                    b64(&p[1..1 + half]),
                    b64(&p[1 + half..])
                )
            }
            Self::Rsa { n, e } => {
                format!(r#"{{"e":"{}","kty":"RSA","n":"{}"}}"#, b64(e), b64(n))
            }
        };
        b64(&Sha256::digest(canonical.as_bytes()))
    }
}

/// One trusted public key and the `kid` tokens it signed carry.
#[derive(Debug, Clone, PartialEq, Eq)]
pub struct Key {
    /// The `kid`: a token naming it is checked against this key.
    pub id: String,
    /// The key itself.
    pub public: VerifyKey,
    /// The one algorithm this key may verify, when the JWK named it.
    pub algorithm: Option<Alg>,
}

impl Key {
    /// A key, checked: a point not on its curve, a short Ed25519 key or
    /// an empty RSA integer is refused here, not at first use.
    pub fn new(id: impl Into<String>, public: VerifyKey) -> Result<Self, AuthnError> {
        provider::validate(&public).map_err(|e| AuthnError::Config(format!("public key: {e}")))?;
        Ok(Self {
            id: id.into(),
            public,
            algorithm: None,
        })
    }

    /// The Ed25519 key a kit identity keypair publishes, with the kid
    /// `identity.Keypair.PublicKeyID` embeds in tokens it signs: the
    /// first 16 hex digits of the SHA-256 of the raw public key.
    pub fn from_identity(public: [u8; 32]) -> Result<Self, AuthnError> {
        Self::new(ed25519_kid(&public), VerifyKey::Ed25519(public))
    }

    /// Parses a PEM `PUBLIC KEY` block (PKIX): Ed25519, RSA or ECDSA
    /// P-256/384/521. The kid is the identity fingerprint for Ed25519,
    /// so a token another kit tool signed with its identity matches;
    /// for RSA and ECDSA it is the RFC 7638 thumbprint.
    pub fn from_public_key_pem(data: &[u8]) -> Result<Self, AuthnError> {
        let der = pem_block(data).ok_or_else(|| AuthnError::Config("no PEM block".into()))?;
        let public = spki(&der).map_err(AuthnError::Config)?;
        match public {
            VerifyKey::Ed25519(k) => Self::from_identity(k),
            other => {
                let id = other.thumbprint();
                Self::new(id, other)
            }
        }
    }
}

fn ed25519_kid(public: &[u8; 32]) -> String {
    let mut s = String::with_capacity(16);
    for b in &Sha256::digest(public)[..8] {
        s.push_str(&format!("{b:02x}"));
    }
    s
}

/// One JSON Web Key (RFC 7517), the members kit reads.
#[derive(Debug, Clone, Default, Deserialize)]
pub struct Jwk {
    /// Key type: `OKP`, `EC` or `RSA`.
    #[serde(default)]
    pub kty: String,
    /// The key id.
    #[serde(default)]
    pub kid: String,
    /// Intended use; empty or `sig` is a signing key.
    #[serde(default, rename = "use")]
    pub use_: String,
    /// The one algorithm the key is for, when named.
    #[serde(default)]
    pub alg: String,
    /// Curve (`OKP`, `EC`).
    #[serde(default)]
    pub crv: String,
    /// Base64url x coordinate or Ed25519 key.
    #[serde(default)]
    pub x: String,
    /// Base64url y coordinate (`EC`).
    #[serde(default)]
    pub y: String,
    /// Base64url modulus (`RSA`).
    #[serde(default)]
    pub n: String,
    /// Base64url public exponent (`RSA`).
    #[serde(default)]
    pub e: String,
}

impl Jwk {
    /// The key as a verification [`Key`]. A key carrying private
    /// members is read as its public half.
    pub fn to_key(&self) -> Result<Key, AuthnError> {
        let dec = |what: &str, v: &str| {
            URL_SAFE_NO_PAD
                .decode(v)
                .map_err(|_| AuthnError::Config(format!("jwk: bad base64url in {what}")))
        };
        let public = match (self.kty.as_str(), self.crv.as_str()) {
            ("OKP", "Ed25519") => {
                let x = dec("x", &self.x)?;
                let k: [u8; 32] = x
                    .try_into()
                    .map_err(|_| AuthnError::Config("jwk: Ed25519 key is not 32 bytes".into()))?;
                VerifyKey::Ed25519(k)
            }
            ("EC", crv @ ("P-256" | "P-384" | "P-521")) => {
                let size = match crv {
                    "P-256" => 32,
                    "P-384" => 48,
                    _ => 66,
                };
                let (x, y) = (dec("x", &self.x)?, dec("y", &self.y)?);
                if x.len() != size || y.len() != size {
                    return Err(AuthnError::Config(format!(
                        "jwk: {crv} coordinates are not {size} bytes"
                    )));
                }
                let mut p = Vec::with_capacity(1 + 2 * size);
                p.push(4);
                p.extend_from_slice(&x);
                p.extend_from_slice(&y);
                match crv {
                    "P-256" => VerifyKey::P256(p),
                    "P-384" => VerifyKey::P384(p),
                    _ => VerifyKey::P521(p),
                }
            }
            ("RSA", _) => VerifyKey::Rsa {
                n: strip_zeros(dec("n", &self.n)?),
                e: strip_zeros(dec("e", &self.e)?),
            },
            (kty, crv) => {
                return Err(AuthnError::Config(format!(
                    "jwk: unsupported key type {kty:?} {crv:?}"
                )))
            }
        };
        let mut key = Key::new(self.kid.clone(), public)?;
        if !self.alg.is_empty() {
            key.algorithm = Some(
                Alg::parse(&self.alg)
                    .ok_or_else(|| AuthnError::Config(format!("jwk: alg {:?}", self.alg)))?,
            );
        }
        Ok(key)
    }
}

/// The most a key set or discovery document may be before it is
/// refused unread.
pub const MAX_DOCUMENT_BYTES: usize = 1 << 20;

/// Reads the signing keys of a JSON Web Key Set document. A key this
/// package cannot use (an encryption key, an unsupported type or
/// curve) is skipped rather than failing the set; a set with none left
/// is an error.
pub fn keys_from_jwks(doc: &[u8]) -> Result<Vec<Key>, AuthnError> {
    if doc.len() > MAX_DOCUMENT_BYTES {
        return Err(AuthnError::Config(format!(
            "jwks: document larger than {MAX_DOCUMENT_BYTES} bytes"
        )));
    }
    let keys =
        signing_keys(jwks_members(doc).map_err(|e| AuthnError::Config(format!("jwks: {e}")))?);
    if keys.is_empty() {
        return Err(AuthnError::Config("jwks: holds no signing key".into()));
    }
    Ok(keys)
}

/// The `keys` members of a JWKS document, unread.
pub(crate) fn jwks_members(doc: &[u8]) -> Result<Vec<serde_json::Value>, serde_json::Error> {
    #[derive(Deserialize)]
    struct Set {
        keys: Vec<serde_json::Value>,
    }
    Ok(serde_json::from_slice::<Set>(doc)?.keys)
}

/// The usable signing keys among JWKS members: an encryption key or
/// one of an unsupported type is skipped.
pub(crate) fn signing_keys(members: Vec<serde_json::Value>) -> Vec<Key> {
    members
        .into_iter()
        .filter_map(|v| serde_json::from_value::<Jwk>(v).ok())
        .filter(|j| j.use_.is_empty() || j.use_ == "sig")
        .filter_map(|j| j.to_key().ok())
        .collect()
}

fn strip_zeros(mut v: Vec<u8>) -> Vec<u8> {
    let lead = v.iter().take_while(|b| **b == 0).count();
    v.drain(..lead);
    v
}

// --- PEM and SPKI ----------------------------------------------------

fn pem_block(data: &[u8]) -> Option<Vec<u8>> {
    let text = std::str::from_utf8(data).ok()?;
    let start = text.find("-----BEGIN ")?;
    let rest = &text[start + "-----BEGIN ".len()..];
    let label_end = rest.find("-----")?;
    let body = &rest[label_end + 5..];
    let end = body.find("-----END ")?;
    let b64: String = body[..end].chars().filter(|c| !c.is_whitespace()).collect();
    base64::engine::general_purpose::STANDARD.decode(b64).ok()
}

/// One DER element: its tag, content and what follows.
fn tlv(input: &[u8]) -> Option<(u8, &[u8], &[u8])> {
    let (&tag, rest) = input.split_first()?;
    let (&first, rest) = rest.split_first()?;
    let (len, rest) = if first < 0x80 {
        (first as usize, rest)
    } else {
        let n = (first & 0x7f) as usize;
        if n == 0 || n > 4 || rest.len() < n {
            return None;
        }
        let len = rest[..n].iter().fold(0usize, |a, b| (a << 8) | *b as usize);
        (len, &rest[n..])
    };
    if rest.len() < len {
        return None;
    }
    Some((tag, &rest[..len], &rest[len..]))
}

const OID_ED25519: &[u8] = &[0x2b, 0x65, 0x70];
const OID_EC: &[u8] = &[0x2a, 0x86, 0x48, 0xce, 0x3d, 0x02, 0x01];
const OID_P256: &[u8] = &[0x2a, 0x86, 0x48, 0xce, 0x3d, 0x03, 0x01, 0x07];
const OID_P384: &[u8] = &[0x2b, 0x81, 0x04, 0x00, 0x22];
const OID_P521: &[u8] = &[0x2b, 0x81, 0x04, 0x00, 0x23];
const OID_RSA: &[u8] = &[0x2a, 0x86, 0x48, 0x86, 0xf7, 0x0d, 0x01, 0x01, 0x01];

/// Reads a SubjectPublicKeyInfo.
fn spki(der: &[u8]) -> Result<VerifyKey, String> {
    let bad = || "malformed public key".to_string();
    let (tag, body, trailing) = tlv(der).ok_or_else(bad)?;
    if tag != 0x30 || !trailing.is_empty() {
        return Err(bad());
    }
    let (tag, algid, rest) = tlv(body).ok_or_else(bad)?;
    if tag != 0x30 {
        return Err(bad());
    }
    let (tag, bits, trailing) = tlv(rest).ok_or_else(bad)?;
    if tag != 0x03 || !trailing.is_empty() || bits.first() != Some(&0) {
        return Err(bad());
    }
    let key = &bits[1..];
    let (tag, oid, params) = tlv(algid).ok_or_else(bad)?;
    if tag != 0x06 {
        return Err(bad());
    }
    match oid {
        OID_ED25519 => {
            let k: [u8; 32] = key.try_into().map_err(|_| bad())?;
            Ok(VerifyKey::Ed25519(k))
        }
        OID_EC => {
            let (tag, curve, _) = tlv(params).ok_or_else(bad)?;
            if tag != 0x06 {
                return Err(bad());
            }
            let p = key.to_vec();
            match curve {
                OID_P256 => Ok(VerifyKey::P256(p)),
                OID_P384 => Ok(VerifyKey::P384(p)),
                OID_P521 => Ok(VerifyKey::P521(p)),
                _ => Err("unsupported public key type: EC curve".into()),
            }
        }
        OID_RSA => {
            let (tag, seq, trailing) = tlv(key).ok_or_else(bad)?;
            if tag != 0x30 || !trailing.is_empty() {
                return Err(bad());
            }
            let (t1, n, rest) = tlv(seq).ok_or_else(bad)?;
            let (t2, e, trailing) = tlv(rest).ok_or_else(bad)?;
            if t1 != 0x02 || t2 != 0x02 || !trailing.is_empty() {
                return Err(bad());
            }
            Ok(VerifyKey::Rsa {
                n: strip_zeros(n.to_vec()),
                e: strip_zeros(e.to_vec()),
            })
        }
        _ => Err("unsupported public key type".into()),
    }
}
