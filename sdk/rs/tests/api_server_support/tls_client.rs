//! A TLS client for the api-server tests: a rustls config trusting a
//! test CA, one HTTP/1.1 exchange, and what a handshake negotiated.
#![allow(dead_code)]

use std::sync::Arc;

use rustls::crypto::CryptoProvider;
use rustls::pki_types::pem::PemObject;
use rustls::pki_types::{CertificateDer, PrivateKeyDer, ServerName};
use rustls::{ClientConfig, NamedGroup, RootCertStore};
use tokio::io::{AsyncReadExt, AsyncWriteExt};
use tokio::net::TcpStream;
use tokio_rustls::TlsConnector;

use super::pki::{Ca, Issued};
use super::Running;

/// A ring client trusting `ca`, offering `alpn` and `versions`, with
/// `identity` as its certificate when given.
pub fn client_config(
    ca: &Ca,
    alpn: &[&[u8]],
    versions: &[&'static rustls::SupportedProtocolVersion],
    identity: Option<&Issued>,
) -> Arc<ClientConfig> {
    client_config_with(
        Arc::new(rustls::crypto::ring::default_provider()),
        ca,
        alpn,
        versions,
        identity,
    )
}

/// [`client_config`] on `provider`.
pub fn client_config_with(
    provider: Arc<CryptoProvider>,
    ca: &Ca,
    alpn: &[&[u8]],
    versions: &[&'static rustls::SupportedProtocolVersion],
    identity: Option<&Issued>,
) -> Arc<ClientConfig> {
    let mut roots = RootCertStore::empty();
    for c in CertificateDer::pem_slice_iter(ca.pem.as_bytes()) {
        roots.add(c.unwrap()).unwrap();
    }
    let b = ClientConfig::builder_with_provider(provider)
        .with_protocol_versions(versions)
        .unwrap()
        .with_root_certificates(roots);
    let mut cfg = match identity {
        None => b.with_no_client_auth(),
        Some(i) => b
            .with_client_auth_cert(
                CertificateDer::pem_slice_iter(i.cert_pem.as_bytes())
                    .collect::<Result<_, _>>()
                    .unwrap(),
                PrivateKeyDer::from_pem_slice(i.key_pem.as_bytes()).unwrap(),
            )
            .unwrap(),
    };
    cfg.alpn_protocols = alpn.iter().map(|a| a.to_vec()).collect();
    Arc::new(cfg)
}

pub fn all_versions() -> Vec<&'static rustls::SupportedProtocolVersion> {
    vec![&rustls::version::TLS13, &rustls::version::TLS12]
}

/// What a completed handshake settled on.
#[derive(Debug)]
pub struct Negotiated {
    pub suite: rustls::CipherSuite,
    pub group: Option<NamedGroup>,
}

/// A handshake only: what it negotiated, or why it failed.
pub async fn handshake(run: &Running, cfg: Arc<ClientConfig>) -> Result<Negotiated, String> {
    let tcp = TcpStream::connect(&run.addr)
        .await
        .map_err(|e| e.to_string())?;
    let name = ServerName::try_from("localhost").unwrap();
    let tls = TlsConnector::from(cfg)
        .connect(name, tcp)
        .await
        .map_err(|e| format!("handshake: {e}"))?;
    let (_, conn) = tls.get_ref();
    Ok(Negotiated {
        suite: conn.negotiated_cipher_suite().expect("suite").suite(),
        group: conn.negotiated_key_exchange_group().map(|g| g.name()),
    })
}

/// One HTTP/1.1 exchange over TLS: the raw response, or why there is none.
pub async fn https_get(
    run: &Running,
    cfg: Arc<ClientConfig>,
    path: &str,
) -> Result<String, String> {
    let tcp = TcpStream::connect(&run.addr)
        .await
        .map_err(|e| e.to_string())?;
    let name = ServerName::try_from("localhost").unwrap();
    let mut tls = TlsConnector::from(cfg)
        .connect(name, tcp)
        .await
        .map_err(|e| format!("handshake: {e}"))?;
    tls.write_all(
        format!("GET {path} HTTP/1.1\r\nhost: localhost\r\nconnection: close\r\n\r\n").as_bytes(),
    )
    .await
    .map_err(|e| e.to_string())?;
    let mut buf = Vec::new();
    // In TLS 1.3 a client-certificate refusal arrives as an alert on the
    // first read, after the client believes the handshake done.
    tls.read_to_end(&mut buf)
        .await
        .map_err(|e| format!("read: {e}"))?;
    if buf.is_empty() {
        return Err("empty".into());
    }
    Ok(String::from_utf8_lossy(&buf).into_owned())
}

pub fn status(raw: &str) -> u16 {
    raw[9..12].parse().unwrap()
}

pub fn header<'a>(raw: &'a str, name: &str) -> Option<&'a str> {
    raw.split("\r\n\r\n").next()?.lines().find_map(|l| {
        let (k, v) = l.split_once(':')?;
        k.eq_ignore_ascii_case(name).then(|| v.trim())
    })
}

pub fn body(raw: &str) -> &str {
    raw.split_once("\r\n\r\n").map_or("", |x| x.1)
}
