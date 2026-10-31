//! The rustls crypto provider behind the api-server's TLS: ring unless
//! the config names another, and the named one drives both the server
//! handshake and the mTLS client-certificate verifier.
#![cfg(feature = "api-server-tls")]

mod api_server_support;

use std::sync::Arc;

use axum::routing::get;
use axum::Router;
use hop_top_kit::api_server::tls::{ClientCertConfig, CryptoProvider, TlsConfig};
use rustls::crypto::{ring, WebPkiSupportedAlgorithms};
use rustls::{CipherSuite, SignatureScheme};

use api_server_support::pki::{ca, Ca, Leaf};
use api_server_support::tls_client::{
    all_versions, client_config, handshake, header, https_get, status,
};
use api_server_support::{config, start, Running};

fn app() -> Router {
    Router::new().route("/who", get(|| async { "who" }))
}

async fn serve(tls: TlsConfig) -> Running {
    let mut cfg = config(app());
    cfg.tls = Some(tls);
    start(cfg).await
}

fn server_tls(ca: &Ca) -> TlsConfig {
    let s = ca.server();
    TlsConfig::from_pem(s.cert_pem, s.key_pem)
}

/// ring, serving TLS 1.3 with ChaCha20-Poly1305 only.
fn chacha_only() -> Arc<CryptoProvider> {
    Arc::new(CryptoProvider {
        cipher_suites: vec![ring::cipher_suite::TLS13_CHACHA20_POLY1305_SHA256],
        ..ring::default_provider()
    })
}

/// ring, accepting only Ed25519 signatures from a peer.
fn ed25519_peers_only() -> Arc<CryptoProvider> {
    let base = ring::default_provider();
    let algs = base.signature_verification_algorithms;
    let mapping: Vec<_> = algs
        .mapping
        .iter()
        .filter(|(s, _)| *s == SignatureScheme::ED25519)
        .copied()
        .collect();
    assert_eq!(mapping.len(), 1, "ring verifies Ed25519");
    Arc::new(CryptoProvider {
        signature_verification_algorithms: WebPkiSupportedAlgorithms {
            all: algs.all,
            mapping: Box::leak(mapping.into_boxed_slice()),
        },
        ..base
    })
}

#[tokio::test]
async fn the_named_provider_sets_the_server_suites() {
    let ca = ca("test ca");
    let client = client_config(&ca, &[b"http/1.1"], &all_versions(), None);

    let run = serve(server_tls(&ca)).await;
    let n = handshake(&run, client.clone()).await.unwrap();
    assert_eq!(
        n.suite,
        CipherSuite::TLS13_AES_256_GCM_SHA384,
        "ring's suites, the client's order"
    );
    run.stop().await;

    let run = serve(server_tls(&ca).crypto_provider(chacha_only())).await;
    let n = handshake(&run, client).await.unwrap();
    assert_eq!(n.suite, CipherSuite::TLS13_CHACHA20_POLY1305_SHA256);
    run.stop().await;
}

#[tokio::test]
async fn the_named_provider_verifies_client_certificates() {
    let ca = ca("test ca");
    let leaf = ca.issue(&Leaf {
        dns: vec!["alice.example.org".into()],
        client: true,
        ..Leaf::default()
    });
    let client = client_config(&ca, &[b"http/1.1"], &all_versions(), Some(&leaf));
    let mtls = |tls: TlsConfig| tls.mtls(ca.pem.clone(), ClientCertConfig::default());

    let run = serve(mtls(server_tls(&ca))).await;
    let raw = https_get(&run, client.clone(), "/who").await.unwrap();
    assert_eq!(status(&raw), 200, "ring verifies the ECDSA leaf: {raw}");
    run.stop().await;

    // The verifier asks for Ed25519 only, so the ECDSA leaf cannot sign
    // and the client goes on without a certificate.
    let run = serve(mtls(server_tls(&ca)).crypto_provider(ed25519_peers_only())).await;
    let raw = https_get(&run, client, "/who").await.unwrap();
    assert_eq!(status(&raw), 401, "{raw}");
    assert_eq!(header(&raw, "www-authenticate"), Some("Bearer"));
    assert!(raw.contains("client certificate required"), "{raw}");
    run.stop().await;
}

#[cfg(feature = "api-server-tls-aws-lc-rs")]
mod aws_lc_rs {
    use super::*;
    use api_server_support::tls_client::client_config_with;
    use hop_top_kit::api_server::tls::aws_lc_rs;
    use rustls::NamedGroup;

    /// An aws-lc-rs client offering X25519MLKEM768 alone: ring has no
    /// such group, aws-lc-rs does.
    fn mlkem_client(ca: &Ca) -> Arc<rustls::ClientConfig> {
        let provider = CryptoProvider {
            kx_groups: vec![aws_lc_rs::kx_group::X25519MLKEM768],
            ..aws_lc_rs::default_provider()
        };
        client_config_with(
            Arc::new(provider),
            ca,
            &[b"http/1.1"],
            &[&rustls::version::TLS13],
            None,
        )
    }

    #[tokio::test]
    async fn ring_stays_the_default_with_aws_lc_rs_compiled_in() {
        let ca = ca("test ca");
        let run = serve(server_tls(&ca)).await;
        let err = handshake(&run, mlkem_client(&ca)).await.unwrap_err();
        assert!(err.starts_with("handshake"), "{err}");
        run.stop().await;
    }

    #[tokio::test]
    async fn aws_lc_rs_serves_when_named() {
        let ca = ca("test ca");
        let tls = server_tls(&ca).crypto_provider(Arc::new(aws_lc_rs::default_provider()));
        let run = serve(tls).await;
        let n = handshake(&run, mlkem_client(&ca)).await.unwrap();
        assert_eq!(n.group, Some(NamedGroup::X25519MLKEM768));
        run.stop().await;
    }
}
