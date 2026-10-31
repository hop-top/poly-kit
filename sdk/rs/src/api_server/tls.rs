//! TLS and mTLS serving, a port of Go's `mtls.go` and the listener half
//! of `serve_tls_files.go`. Feature `api-server-tls`.
//!
//! rustls terminates TLS in the listener's accept loop, on the ring
//! provider unless [`TlsConfig::crypto_provider`] names another (feature
//! `api-server-tls-aws-lc-rs` compiles in `aws_lc_rs`); the one
//! provider drives the handshake and the client-certificate verifier.
//! With a client CA, every client is asked for a certificate and
//! one that does not chain to the bundle ends the handshake. A client
//! that presents none still reaches the HTTP plane, where the auth layer
//! refuses it as `unauthenticated` (so health probes answer without a
//! certificate). The verified certificate's principal and tenant are the
//! caller, as [`Claims`].
//!
//! Not ported from Go: ACME, revocation lists, and reloading the files
//! when they change. A rotated certificate takes effect on restart.

use std::sync::Arc;

use regex::Regex;
use rustls::pki_types::pem::PemObject;
use rustls::pki_types::{CertificateDer, PrivateKeyDer};
use rustls::server::WebPkiClientVerifier;
use rustls::{RootCertStore, ServerConfig};

use super::error::ApiServerError;
use crate::authn::Claims;

#[cfg(feature = "api-server-tls-aws-lc-rs")]
pub use rustls::crypto::aws_lc_rs;
pub use rustls::crypto::{ring, CryptoProvider};

/// The lowest TLS version the listener accepts.
#[derive(Debug, Clone, Copy, PartialEq, Eq, Default)]
pub enum TlsVersion {
    /// TLS 1.2 and 1.3 (the default).
    #[default]
    V1_2,
    /// TLS 1.3 only.
    V1_3,
}

impl TlsVersion {
    /// Parses `1.2` or `1.3`; nothing lower is accepted.
    pub fn parse(s: &str) -> Result<Self, ApiServerError> {
        match s {
            "1.2" => Ok(Self::V1_2),
            "1.3" => Ok(Self::V1_3),
            other => Err(ApiServerError(format!(
                "tls min_version: {other:?} is not supported; use \"1.2\" or \"1.3\""
            ))),
        }
    }
}

/// Where in the client certificate the caller's principal is read from.
#[derive(Debug, Clone, Copy, PartialEq, Eq, Default)]
pub enum Principal {
    /// The first URI SAN, else the first DNS SAN, else the first email
    /// SAN (the default).
    #[default]
    San,
    /// The first URI SAN (a SPIFFE ID, say).
    SanUri,
    /// The first DNS SAN.
    SanDns,
    /// The first email SAN.
    SanEmail,
    /// The subject common name.
    Cn,
}

impl Principal {
    /// The names [`Principal::parse`] accepts.
    pub const SOURCES: [&'static str; 5] = ["san", "san_uri", "san_dns", "san_email", "cn"];

    /// Parses a source name; empty is `san`.
    pub fn parse(s: &str) -> Result<Self, ApiServerError> {
        match s {
            "" | "san" => Ok(Self::San),
            "san_uri" => Ok(Self::SanUri),
            "san_dns" => Ok(Self::SanDns),
            "san_email" => Ok(Self::SanEmail),
            "cn" => Ok(Self::Cn),
            other => Err(ApiServerError(format!(
                "unknown principal source {other:?}"
            ))),
        }
    }

    /// The source's name.
    pub fn as_str(self) -> &'static str {
        match self {
            Self::San => "san",
            Self::SanUri => "san_uri",
            Self::SanDns => "san_dns",
            Self::SanEmail => "san_email",
            Self::Cn => "cn",
        }
    }
}

/// How a verified client certificate becomes a caller.
#[derive(Debug, Clone, Default)]
pub struct ClientCertConfig {
    /// Where the principal is read from.
    pub principal: Principal,
    /// Dotted OID (`2.5.4.10` is O, `2.5.4.11` is OU) of the subject
    /// attribute the tenant is read from, else of a certificate
    /// extension holding a string.
    pub tenant_oid: Option<String>,
    /// A pattern matched against the URI, DNS and email SANs, in that
    /// order: the first capture group of the first match, or the whole
    /// match when the pattern has none. Not with `tenant_oid`.
    pub tenant_san_pattern: Option<String>,
}

/// The listener's TLS setting.
#[derive(Debug, Clone)]
pub struct TlsConfig {
    cert_chain_pem: Vec<u8>,
    key_pem: Vec<u8>,
    /// The lowest version accepted.
    pub min_version: TlsVersion,
    mtls: Option<(Vec<u8>, ClientCertConfig)>,
    crypto_provider: Option<Arc<CryptoProvider>>,
}

impl TlsConfig {
    /// TLS with the PEM certificate chain and private key.
    pub fn from_pem(cert_chain_pem: impl Into<Vec<u8>>, key_pem: impl Into<Vec<u8>>) -> Self {
        Self {
            cert_chain_pem: cert_chain_pem.into(),
            key_pem: key_pem.into(),
            min_version: TlsVersion::default(),
            mtls: None,
            crypto_provider: None,
        }
    }

    /// Sets the lowest TLS version.
    pub fn min_version(mut self, v: TlsVersion) -> Self {
        self.min_version = v;
        self
    }

    /// Serves on `provider` rather than ring's default: its cipher
    /// suites, key exchange groups and key loading for the handshake,
    /// its signature algorithms for verifying client certificates.
    pub fn crypto_provider(mut self, provider: Arc<CryptoProvider>) -> Self {
        self.crypto_provider = Some(provider);
        self
    }

    /// Asks every client for a certificate that chains to the PEM
    /// bundle `client_ca_pem`, and makes the certificate the credential:
    /// the server's `auth` function is not consulted.
    pub fn mtls(mut self, client_ca_pem: impl Into<Vec<u8>>, identity: ClientCertConfig) -> Self {
        self.mtls = Some((client_ca_pem.into(), identity));
        self
    }
}

/// What a built TLS setting yields.
pub(crate) struct TlsSetup {
    pub(crate) server: Arc<ServerConfig>,
    pub(crate) identity: Option<Arc<CertIdentity>>,
}

pub(crate) fn build(cfg: &TlsConfig) -> Result<TlsSetup, ApiServerError> {
    let chain: Vec<CertificateDer<'static>> = CertificateDer::pem_slice_iter(&cfg.cert_chain_pem)
        .collect::<Result<_, _>>()
        .map_err(|e| ApiServerError(format!("tls certificate: {e}")))?;
    if chain.is_empty() {
        return Err(ApiServerError(
            "tls certificate: no PEM certificate found".into(),
        ));
    }
    let key = PrivateKeyDer::from_pem_slice(&cfg.key_pem)
        .map_err(|e| ApiServerError(format!("tls key: {e}")))?;

    let provider = cfg
        .crypto_provider
        .clone()
        .unwrap_or_else(|| Arc::new(ring::default_provider()));
    let versions: &[&'static rustls::SupportedProtocolVersion] = match cfg.min_version {
        TlsVersion::V1_2 => &[&rustls::version::TLS13, &rustls::version::TLS12],
        TlsVersion::V1_3 => &[&rustls::version::TLS13],
    };
    let builder = ServerConfig::builder_with_provider(provider.clone())
        .with_protocol_versions(versions)
        .map_err(|e| ApiServerError(format!("tls: {e}")))?;

    let (builder, identity) = match &cfg.mtls {
        None => (builder.with_no_client_auth(), None),
        Some((ca_pem, id)) => {
            let identity = CertIdentity::compile(id)?;
            let mut roots = RootCertStore::empty();
            let mut n = 0;
            for c in CertificateDer::pem_slice_iter(ca_pem) {
                let c = c.map_err(|e| ApiServerError(format!("tls client CA: {e}")))?;
                roots
                    .add(c)
                    .map_err(|e| ApiServerError(format!("tls client CA: {e}")))?;
                n += 1;
            }
            if n == 0 {
                return Err(ApiServerError(
                    "tls client CA: the bundle holds no PEM certificate".into(),
                ));
            }
            // Asked of every client; one with no certificate reaches the
            // HTTP plane and is refused there.
            let verifier = WebPkiClientVerifier::builder_with_provider(Arc::new(roots), provider)
                .allow_unauthenticated()
                .build()
                .map_err(|e| ApiServerError(format!("tls client CA: {e}")))?;
            (
                builder.with_client_cert_verifier(verifier),
                Some(Arc::new(identity)),
            )
        }
    };
    let mut server = builder
        .with_single_cert(chain, key)
        .map_err(|e| ApiServerError(format!("tls certificate and key: {e}")))?;
    server.alpn_protocols = vec![b"h2".to_vec(), b"http/1.1".to_vec()];
    Ok(TlsSetup {
        server: Arc::new(server),
        identity,
    })
}

/// A [`ClientCertConfig`] checked and compiled.
#[derive(Debug)]
pub(crate) struct CertIdentity {
    principal: Principal,
    tenant_oid: Option<Vec<u8>>,
    tenant_san: Option<Regex>,
}

impl CertIdentity {
    pub(crate) fn compile(cfg: &ClientCertConfig) -> Result<Self, ApiServerError> {
        if cfg.tenant_oid.is_some() && cfg.tenant_san_pattern.is_some() {
            return Err(ApiServerError(
                "client certificate: set one tenant source, tenant_oid or tenant_san_pattern"
                    .into(),
            ));
        }
        let tenant_oid = cfg
            .tenant_oid
            .as_deref()
            .map(|s| {
                encode_oid(s)
                    .ok_or_else(|| ApiServerError(format!("tenant_oid: {s:?} is not a dotted OID")))
            })
            .transpose()?;
        let tenant_san = cfg
            .tenant_san_pattern
            .as_deref()
            .map(|p| Regex::new(p).map_err(|e| ApiServerError(format!("tenant_san_pattern: {e}"))))
            .transpose()?;
        Ok(Self {
            principal: cfg.principal,
            tenant_oid,
            tenant_san,
        })
    }

    /// The caller a certificate names.
    pub(crate) fn claims(&self, der: &[u8]) -> Result<Claims, String> {
        let cert = Cert::parse(der).ok_or("client certificate cannot be read")?;
        let first = |v: &[String]| v.first().cloned().unwrap_or_default();
        let p = match self.principal {
            Principal::San => [&cert.uris, &cert.dns, &cert.emails]
                .iter()
                .map(|v| first(v))
                .find(|s| !s.is_empty())
                .unwrap_or_default(),
            Principal::SanUri => first(&cert.uris),
            Principal::SanDns => first(&cert.dns),
            Principal::SanEmail => first(&cert.emails),
            Principal::Cn => cert.common_name.clone().unwrap_or_default(),
        };
        if p.is_empty() {
            return Err(format!(
                "client certificate carries no principal ({})",
                self.principal.as_str()
            ));
        }
        Ok(Claims {
            subject: p,
            tenant: self.tenant(&cert),
            scopes: Vec::new(),
        })
    }

    fn tenant(&self, cert: &Cert) -> String {
        if let Some(oid) = &self.tenant_oid {
            if let Some((_, v)) = cert.subject.iter().find(|(o, _)| o == oid) {
                return v.clone();
            }
            if let Some((_, v)) = cert.ext_strings.iter().find(|(o, _)| o == oid) {
                return v.clone();
            }
        }
        if let Some(re) = &self.tenant_san {
            for san in cert.uris.iter().chain(&cert.dns).chain(&cert.emails) {
                if let Some(c) = re.captures(san) {
                    let m = c.get(1).or_else(|| c.get(0));
                    return m.map(|m| m.as_str().to_string()).unwrap_or_default();
                }
            }
        }
        String::new()
    }
}

/// The caller a verified client certificate (DER) names, as
/// [`Claims`]. For code that holds a [`PeerCertificate`](super::PeerCertificate)
/// and wants the identity the server's own mTLS layer would give it.
///
/// # Errors
///
/// Returns the reason when the certificate cannot be read, or carries no
/// principal where `cfg` says to look, or `cfg` does not compile.
pub fn client_cert_claims(der: &[u8], cfg: &ClientCertConfig) -> Result<Claims, String> {
    CertIdentity::compile(cfg).map_err(|e| e.0)?.claims(der)
}

// --- DER ---------------------------------------------------------------
//
// The certificate was verified by the handshake; this reads only the
// fields the identity needs: subject attributes, the string-valued
// extensions and the SANs. A minimal definite-length DER reader.

const TAG_SEQ: u8 = 0x30;
const TAG_SET: u8 = 0x31;
const TAG_OID: u8 = 0x06;
const TAG_OCTETS: u8 = 0x04;
const TAG_CTX_0: u8 = 0xA0;
const TAG_EXTENSIONS: u8 = 0xA3;

const OID_CN: &[u8] = &[0x55, 0x04, 0x03];
const OID_SAN: &[u8] = &[0x55, 0x1D, 0x11];

/// One TLV: tag, content, rest. Tags above 30 and indefinite lengths
/// are not DER certificates' business.
fn tlv(b: &[u8]) -> Option<(u8, &[u8], &[u8])> {
    let (&tag, b) = b.split_first()?;
    if tag & 0x1F == 0x1F {
        return None;
    }
    let (&l0, b) = b.split_first()?;
    let (len, b) = if l0 < 0x80 {
        (l0 as usize, b)
    } else {
        let n = (l0 & 0x7F) as usize;
        if n == 0 || n > 4 || b.len() < n {
            return None;
        }
        let len = b[..n].iter().fold(0usize, |a, &x| (a << 8) | x as usize);
        (len, &b[n..])
    };
    if b.len() < len {
        return None;
    }
    Some((tag, &b[..len], &b[len..]))
}

fn items(mut b: &[u8]) -> Vec<(u8, &[u8])> {
    let mut out = Vec::new();
    while !b.is_empty() {
        match tlv(b) {
            Some((t, c, rest)) => {
                out.push((t, c));
                b = rest;
            }
            None => break,
        }
    }
    out
}

/// A string-typed value: UTF8String, PrintableString, T61String or
/// IA5String.
fn string_value(tag: u8, c: &[u8]) -> Option<String> {
    match tag {
        0x0C | 0x13 | 0x14 | 0x16 => Some(String::from_utf8_lossy(c).into_owned()),
        _ => None,
    }
}

#[derive(Default)]
struct Cert {
    subject: Vec<(Vec<u8>, String)>,
    common_name: Option<String>,
    ext_strings: Vec<(Vec<u8>, String)>,
    uris: Vec<String>,
    dns: Vec<String>,
    emails: Vec<String>,
}

impl Cert {
    fn parse(der: &[u8]) -> Option<Cert> {
        let (t, cert, _) = tlv(der)?;
        (t == TAG_SEQ).then_some(())?;
        let (t, tbs, _) = tlv(cert)?;
        (t == TAG_SEQ).then_some(())?;
        let mut fields = items(tbs).into_iter();
        let mut next = fields.next()?;
        if next.0 == TAG_CTX_0 {
            next = fields.next()?; // serial
        }
        // next: serial. Then signature, issuer, validity, subject, spki.
        let _serial = next;
        let _sig = fields.next()?;
        let _issuer = fields.next()?;
        let _validity = fields.next()?;
        let (st, subject) = fields.next()?;
        (st == TAG_SEQ).then_some(())?;
        let _spki = fields.next()?;

        let mut out = Cert::default();
        for (t, rdn) in items(subject) {
            if t != TAG_SET {
                continue;
            }
            for (t, atv) in items(rdn) {
                if t != TAG_SEQ {
                    continue;
                }
                let mut parts = items(atv).into_iter();
                let (ot, oid) = parts.next()?;
                let (vt, val) = parts.next()?;
                if ot != TAG_OID {
                    continue;
                }
                if let Some(s) = string_value(vt, val) {
                    if oid == OID_CN && out.common_name.is_none() {
                        out.common_name = Some(s.clone());
                    }
                    out.subject.push((oid.to_vec(), s));
                }
            }
        }
        for (t, c) in fields {
            if t != TAG_EXTENSIONS {
                continue;
            }
            let (st, exts, _) = tlv(c)?;
            (st == TAG_SEQ).then_some(())?;
            for (t, ext) in items(exts) {
                if t != TAG_SEQ {
                    continue;
                }
                let parts = items(ext);
                let Some((_, oid)) = parts.first().filter(|(t, _)| *t == TAG_OID) else {
                    continue;
                };
                let Some((_, value)) = parts.iter().find(|(t, _)| *t == TAG_OCTETS) else {
                    continue;
                };
                if *oid == OID_SAN {
                    out.sans(value);
                } else if let Some((vt, v, rest)) = tlv(value) {
                    if rest.is_empty() {
                        if let Some(s) = string_value(vt, v) {
                            out.ext_strings.push((oid.to_vec(), s));
                        }
                    }
                }
            }
        }
        Some(out)
    }

    fn sans(&mut self, value: &[u8]) {
        let Some((TAG_SEQ, names, _)) = tlv(value) else {
            return;
        };
        for (t, n) in items(names) {
            let s = String::from_utf8_lossy(n).into_owned();
            match t {
                0x81 => self.emails.push(s),
                0x82 => self.dns.push(s),
                0x86 => self.uris.push(s),
                _ => {}
            }
        }
    }
}

/// A dotted OID as its DER content bytes.
fn encode_oid(s: &str) -> Option<Vec<u8>> {
    let arcs: Vec<u64> = s
        .split('.')
        .map(|a| a.parse::<u64>().ok())
        .collect::<Option<_>>()?;
    if arcs.len() < 2 || arcs[0] > 2 || (arcs[0] < 2 && arcs[1] >= 40) {
        return None;
    }
    let mut out = Vec::new();
    let mut push = |mut v: u64| {
        let mut tmp = vec![(v & 0x7F) as u8];
        v >>= 7;
        while v > 0 {
            tmp.push(((v & 0x7F) as u8) | 0x80);
            v >>= 7;
        }
        tmp.reverse();
        out.extend(tmp);
    };
    push(arcs[0] * 40 + arcs[1]);
    for &a in &arcs[2..] {
        push(a);
    }
    Some(out)
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn oid_encoding() {
        assert_eq!(encode_oid("2.5.4.3").unwrap(), OID_CN);
        assert_eq!(encode_oid("2.5.29.17").unwrap(), OID_SAN);
        assert_eq!(
            encode_oid("1.3.6.1.4.1.311").unwrap(),
            [0x2B, 6, 1, 4, 1, 0x82, 0x37]
        );
        assert!(encode_oid("3.1").is_none());
        assert!(encode_oid("1.40").is_none());
        assert!(encode_oid("2").is_none());
        assert!(encode_oid("a.b").is_none());
    }

    fn self_signed() -> TlsConfig {
        let key = rcgen::KeyPair::generate().unwrap();
        let cert = rcgen::CertificateParams::new(vec!["localhost".into()])
            .unwrap()
            .self_signed(&key)
            .unwrap();
        TlsConfig::from_pem(cert.pem(), key.serialize_pem())
    }

    fn shape(p: &CryptoProvider) -> (Vec<rustls::NamedGroup>, Vec<rustls::CipherSuite>) {
        (
            p.kx_groups.iter().map(|g| g.name()).collect(),
            p.cipher_suites.iter().map(|s| s.suite()).collect(),
        )
    }

    #[test]
    fn an_unset_provider_is_ring() {
        let setup = build(&self_signed()).unwrap();
        assert_eq!(
            shape(setup.server.crypto_provider()),
            shape(&ring::default_provider())
        );
    }

    #[test]
    fn a_named_provider_is_the_one_served() {
        let p = Arc::new(ring::default_provider());
        let setup = build(&self_signed().crypto_provider(p.clone())).unwrap();
        assert!(Arc::ptr_eq(setup.server.crypto_provider(), &p));
    }

    #[test]
    fn tlv_lengths() {
        assert_eq!(
            tlv(&[0x04, 0x01, 0xAA, 0xFF]),
            Some((0x04, &[0xAA][..], &[0xFF][..]))
        );
        let mut long = vec![0x04, 0x81, 0x80];
        long.extend(vec![7u8; 128]);
        assert_eq!(tlv(&long).unwrap().1.len(), 128);
        assert!(tlv(&[0x04, 0x05, 1]).is_none());
        assert!(tlv(&[0x04, 0x80]).is_none());
        assert!(tlv(&[0x1F, 0x01, 0]).is_none());
    }
}
