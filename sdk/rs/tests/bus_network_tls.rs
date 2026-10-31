//! `wss://` peers and the TLS option: a local rustls server whose
//! certificate chains to a test CA is refused under the default roots
//! and accepted when the CA comes in through `NetworkOptions::tls_config`.
//! The certificates in `tests/bus_network_tls/` are test-only (CN
//! `localhost`, SAN `localhost` and `127.0.0.1`, valid for a century).

#![cfg(feature = "bus-network")]

mod bus_network_support;

use std::net::TcpListener;
use std::sync::Arc;
use std::thread;
use std::time::Duration;

use bus_network_support::*;
use hop_top_kit::bus::network::{NetworkError, NetworkOptions};
use rustls::pki_types::pem::PemObject;
use rustls::pki_types::{CertificateDer, PrivateKeyDer};
use tokio_tungstenite::tungstenite::{self, Message};

const CA: &[u8] = include_bytes!("bus_network_tls/ca.pem");
const CERT: &[u8] = include_bytes!("bus_network_tls/server.pem");
const KEY: &[u8] = include_bytes!("bus_network_tls/server.key");

fn provider() -> Arc<rustls::crypto::CryptoProvider> {
    Arc::new(rustls::crypto::ring::default_provider())
}

/// A one-connection `wss` server on loopback: TLS, the WebSocket
/// upgrade, then it reads frames until the peer leaves. Returns the
/// port and a receiver of the first text frame (none on a failed
/// handshake).
fn wss_server() -> (u16, std::sync::mpsc::Receiver<Option<String>>) {
    let certs = vec![CertificateDer::from_pem_slice(CERT).unwrap()];
    let key = PrivateKeyDer::from_pem_slice(KEY).unwrap();
    let config = Arc::new(
        rustls::ServerConfig::builder_with_provider(provider())
            .with_safe_default_protocol_versions()
            .unwrap()
            .with_no_client_auth()
            .with_single_cert(certs, key)
            .unwrap(),
    );
    let ln = TcpListener::bind("127.0.0.1:0").unwrap();
    let port = ln.local_addr().unwrap().port();
    let (tx, rx) = std::sync::mpsc::channel();
    thread::spawn(move || {
        let (tcp, _) = ln.accept().unwrap();
        tcp.set_read_timeout(Some(Duration::from_secs(5))).unwrap();
        let conn = rustls::ServerConnection::new(config).unwrap();
        let tls = rustls::StreamOwned::new(conn, tcp);
        let Ok(mut ws) = tungstenite::accept(tls) else {
            let _ = tx.send(None);
            return;
        };
        let first = match ws.read() {
            Ok(Message::Text(t)) => Some(t.to_string()),
            _ => None,
        };
        let _ = tx.send(first);
    });
    (port, rx)
}

fn trusting_the_test_ca() -> Arc<rustls::ClientConfig> {
    let mut roots = rustls::RootCertStore::empty();
    roots
        .add(CertificateDer::from_pem_slice(CA).unwrap())
        .unwrap();
    Arc::new(
        rustls::ClientConfig::builder_with_provider(provider())
            .with_safe_default_protocol_versions()
            .unwrap()
            .with_root_certificates(roots)
            .with_no_client_auth(),
    )
}

#[test]
fn the_default_roots_refuse_an_unknown_ca() {
    let (port, rx) = wss_server();
    let b = Peer::new(NetworkOptions::new());
    let err = b
        .connect(&format!("wss://localhost:{port}/"))
        .expect_err("an untrusted certificate");
    assert!(matches!(err, NetworkError::Dial(_)), "{err:?}");
    assert!(
        err.to_string().to_lowercase().contains("certificate"),
        "{err}"
    );
    assert_eq!(rx.recv_timeout(Duration::from_secs(10)).unwrap(), None);
}

#[test]
fn tls_config_is_used_for_wss_peers() {
    let (port, rx) = wss_server();
    let b = Peer::new(
        NetworkOptions::new()
            .origin_id("B")
            .tls_config(trusting_the_test_ca()),
    );
    b.connect(&format!("wss://localhost:{port}/"))
        .expect("the configured CA is trusted");
    publish(
        &b.bus,
        ev("tls.test.event.sent", "B", serde_json::Value::Null),
    );
    let first = rx
        .recv_timeout(Duration::from_secs(10))
        .unwrap()
        .expect("a frame over TLS");
    assert!(
        first.contains(r#""topic":"tls.test.event.sent""#),
        "{first}"
    );
}
