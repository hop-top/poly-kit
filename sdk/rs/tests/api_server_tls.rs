//! TLS and mTLS serving on the api-server, over real sockets, with
//! certificates generated at run time. Go is the authority: `mtls.go`
//! and the listener half of `serve_tls_files.go`.
#![cfg(feature = "api-server-tls")]

mod api_server_support;

use std::sync::Arc;

use axum::extract::Extension;
use axum::http::{HeaderMap, HeaderValue};
use axum::routing::get;
use axum::Router;
use hop_top_kit::api_server::tls::{
    client_cert_claims, ClientCertConfig, Principal, TlsConfig, TlsVersion,
};
use hop_top_kit::api_server::{ApiServerConfig, ApiService, PeerCertificate};
use hop_top_kit::authn::{AuthFunc, Claims};
use hop_top_kit::serve::Service;
use rustls::pki_types::ServerName;
use serde_json::Value;
use tokio::io::{AsyncReadExt, AsyncWriteExt};
use tokio::net::TcpStream;
use tokio_rustls::TlsConnector;

use api_server_support::pki::{ca, Ca, Issued, Leaf};
use api_server_support::tls_client::{
    all_versions, body, client_config, header, https_get, status,
};
use api_server_support::{config, start};

fn app() -> Router {
    Router::new()
        .route("/hello", get(|| async { "hi" }))
        .route(
            "/who",
            get(|claims: Option<Extension<Claims>>, cert: Option<Extension<PeerCertificate>>| async move {
                let mut h = HeaderMap::new();
                if let Some(Extension(c)) = claims {
                    h.insert("x-subject", HeaderValue::from_str(&c.subject).unwrap());
                    h.insert("x-tenant", HeaderValue::from_str(&c.tenant).unwrap());
                }
                if let Some(Extension(c)) = cert {
                    h.insert("x-cert-len", HeaderValue::from(c.0.len()));
                }
                (h, "who")
            }),
        )
}

fn server_tls(ca: &Ca) -> (TlsConfig, Issued) {
    let s = ca.server();
    (
        TlsConfig::from_pem(s.cert_pem.clone(), s.key_pem.clone()),
        s,
    )
}

// --- TLS ---------------------------------------------------------------

#[tokio::test]
async fn the_listener_serves_https_with_hsts_and_a_scheme() {
    let ca = ca("test ca");
    let (tls, _) = server_tls(&ca);
    let mut cfg = config(app());
    cfg.tls = Some(tls);
    let run = start(cfg).await;
    assert_eq!(run.svc.scheme(), "https");

    let raw = https_get(
        &run,
        client_config(&ca, &[b"http/1.1"], &all_versions(), None),
        "/hello",
    )
    .await
    .unwrap();
    assert_eq!(status(&raw), 200, "{raw}");
    assert_eq!(body(&raw), "hi");
    assert_eq!(
        header(&raw, "strict-transport-security"),
        Some("max-age=31536000"),
        "HSTS over TLS: {raw}"
    );
    run.stop().await;
}

#[tokio::test]
async fn hsts_is_never_sent_on_a_plain_listener() {
    let run = start(config(app())).await;
    assert_eq!(run.svc.scheme(), "http");
    let raw = api_server_support::plain_get(&run.addr, "/hello").await;
    assert_eq!(status(&raw), 200);
    assert_eq!(header(&raw, "strict-transport-security"), None, "{raw}");
    run.stop().await;
}

#[tokio::test]
async fn plaintext_to_a_tls_listener_is_not_served() {
    let ca = ca("test ca");
    let (tls, _) = server_tls(&ca);
    let mut cfg = config(app());
    cfg.tls = Some(tls);
    let run = start(cfg).await;
    let mut s = TcpStream::connect(&run.addr).await.unwrap();
    s.write_all(b"GET /hello HTTP/1.1\r\nhost: localhost\r\nconnection: close\r\n\r\n")
        .await
        .unwrap();
    let mut buf = Vec::new();
    let _ = tokio::time::timeout(std::time::Duration::from_secs(3), s.read_to_end(&mut buf)).await;
    assert!(
        !String::from_utf8_lossy(&buf).contains("hi"),
        "no plaintext answer"
    );
    run.stop().await;
}

#[tokio::test]
async fn a_client_that_does_not_trust_the_server_fails_the_handshake() {
    let server_ca = ca("server ca");
    let other = ca("other ca");
    let (tls, _) = server_tls(&server_ca);
    let mut cfg = config(app());
    cfg.tls = Some(tls);
    let run = start(cfg).await;
    let err = https_get(
        &run,
        client_config(&other, &[], &all_versions(), None),
        "/hello",
    )
    .await
    .unwrap_err();
    assert!(err.starts_with("handshake"), "{err}");
    run.stop().await;
}

#[tokio::test]
async fn min_version_1_3_refuses_a_tls_1_2_client() {
    let ca = ca("test ca");
    let s = ca.server();
    for (min, v12_ok) in [(TlsVersion::V1_2, true), (TlsVersion::V1_3, false)] {
        let mut cfg = config(app());
        cfg.tls = Some(TlsConfig::from_pem(s.cert_pem.clone(), s.key_pem.clone()).min_version(min));
        let run = start(cfg).await;
        let r = https_get(
            &run,
            client_config(&ca, &[], &[&rustls::version::TLS12], None),
            "/hello",
        )
        .await;
        assert_eq!(r.is_ok(), v12_ok, "min {min:?}: {r:?}");
        let r13 = https_get(
            &run,
            client_config(&ca, &[], &[&rustls::version::TLS13], None),
            "/hello",
        )
        .await;
        assert!(r13.is_ok(), "1.3 is always served: {r13:?}");
        run.stop().await;
    }
    assert_eq!(TlsVersion::parse("1.2").unwrap(), TlsVersion::V1_2);
    assert_eq!(TlsVersion::parse("1.3").unwrap(), TlsVersion::V1_3);
    for bad in ["1.1", "1.0", "", "TLS1.3", "2"] {
        assert!(TlsVersion::parse(bad).is_err(), "{bad}");
    }
}

#[tokio::test]
async fn alpn_offers_h2_and_http_1_1_and_h2_is_served() {
    let ca = ca("test ca");
    let (tls, _) = server_tls(&ca);
    let mut cfg = config(app());
    cfg.tls = Some(tls);
    let run = start(cfg).await;

    for (offer, want) in [
        (vec![&b"h2"[..], b"http/1.1"], Some(&b"h2"[..])),
        (vec![&b"http/1.1"[..]], Some(&b"http/1.1"[..])),
    ] {
        let tcp = TcpStream::connect(&run.addr).await.unwrap();
        let tls = TlsConnector::from(client_config(&ca, &offer, &all_versions(), None))
            .connect(ServerName::try_from("localhost").unwrap(), tcp)
            .await
            .unwrap();
        assert_eq!(tls.get_ref().1.alpn_protocol(), want);
    }

    // A real h2 exchange on the negotiated stream.
    let tcp = TcpStream::connect(&run.addr).await.unwrap();
    let tls = TlsConnector::from(client_config(&ca, &[b"h2"], &all_versions(), None))
        .connect(ServerName::try_from("localhost").unwrap(), tcp)
        .await
        .unwrap();
    let io = hyper_util::rt::TokioIo::new(tls);
    let (mut send, conn) =
        hyper::client::conn::http2::handshake(hyper_util::rt::TokioExecutor::new(), io)
            .await
            .unwrap();
    tokio::spawn(conn);
    let resp = send
        .send_request(
            hyper::Request::builder()
                .uri("https://localhost/who")
                .body(axum::body::Body::empty())
                .unwrap(),
        )
        .await
        .unwrap();
    assert_eq!(resp.status(), 200);
    assert_eq!(resp.version(), hyper::Version::HTTP_2);
    assert!(resp.headers().contains_key("strict-transport-security"));
    run.stop().await;
}

// --- mTLS --------------------------------------------------------------

fn mtls_cfg(ca: &Ca, id: ClientCertConfig) -> (ApiServerConfig, Issued) {
    let s = ca.server();
    let mut cfg = config(app());
    cfg.tls =
        Some(TlsConfig::from_pem(s.cert_pem.clone(), s.key_pem.clone()).mtls(ca.pem.clone(), id));
    (cfg, s)
}

#[tokio::test]
async fn a_verified_client_certificate_is_the_caller() {
    let ca = ca("test ca");
    let (cfg, _) = mtls_cfg(
        &ca,
        ClientCertConfig {
            principal: Principal::SanDns,
            tenant_oid: Some("2.5.4.10".into()),
            ..Default::default()
        },
    );
    let run = start(cfg).await;
    let client = ca.issue(&Leaf {
        dns: vec!["alice.example.org".into()],
        org: vec!["acme".into()],
        client: true,
        ..Leaf::default()
    });
    let raw = https_get(
        &run,
        client_config(&ca, &[b"http/1.1"], &all_versions(), Some(&client)),
        "/who",
    )
    .await
    .unwrap();
    assert_eq!(status(&raw), 200, "{raw}");
    assert_eq!(header(&raw, "x-subject"), Some("alice.example.org"));
    assert_eq!(header(&raw, "x-tenant"), Some("acme"));
    assert_eq!(
        header(&raw, "x-cert-len"),
        Some(client.cert_der.len().to_string().as_str()),
        "the handler can read the verified certificate"
    );
    run.stop().await;
}

#[tokio::test]
async fn no_certificate_reaches_the_plane_and_is_refused_but_health_answers() {
    let ca = ca("test ca");
    let (cfg, _) = mtls_cfg(&ca, ClientCertConfig::default());
    let run = start(cfg).await;
    let c = client_config(&ca, &[b"http/1.1"], &all_versions(), None);

    let raw = https_get(&run, c.clone(), "/who").await.unwrap();
    assert_eq!(status(&raw), 401, "{raw}");
    assert_eq!(header(&raw, "www-authenticate"), Some("Bearer"));
    let v: Value = serde_json::from_str(body(&raw)).unwrap();
    assert_eq!(v["code"], "unauthenticated");
    assert_eq!(v["message"], "client certificate required");

    let probe = https_get(&run, c, "/healthz").await.unwrap();
    assert_eq!(status(&probe), 200, "probes carry no certificate: {probe}");
    run.stop().await;
}

#[tokio::test]
async fn a_certificate_from_another_ca_ends_the_handshake() {
    let ca = ca("test ca");
    let rogue = self::ca("rogue ca");
    let (cfg, _) = mtls_cfg(&ca, ClientCertConfig::default());
    let run = start(cfg).await;
    let forged = rogue.client("mallory.example.org");
    let r = https_get(
        &run,
        client_config(&ca, &[b"http/1.1"], &all_versions(), Some(&forged)),
        "/who",
    )
    .await;
    match r {
        Err(_) => {}
        Ok(raw) => panic!("a certificate from another CA was served: {raw}"),
    }
    run.stop().await;
}

#[tokio::test]
async fn a_certificate_with_no_principal_where_asked_is_refused() {
    let ca = ca("test ca");
    let (cfg, _) = mtls_cfg(
        &ca,
        ClientCertConfig {
            principal: Principal::SanUri,
            ..Default::default()
        },
    );
    let run = start(cfg).await;
    let dns_only = ca.client("bob.example.org");
    let raw = https_get(
        &run,
        client_config(&ca, &[b"http/1.1"], &all_versions(), Some(&dns_only)),
        "/who",
    )
    .await
    .unwrap();
    assert_eq!(status(&raw), 401, "{raw}");
    let v: Value = serde_json::from_str(body(&raw)).unwrap();
    assert_eq!(
        v["message"],
        "client certificate carries no principal (san_uri)"
    );
    run.stop().await;
}

#[tokio::test]
async fn mtls_replaces_the_auth_function() {
    let ca = ca("test ca");
    let (mut cfg, _) = mtls_cfg(&ca, ClientCertConfig::default());
    let called = Arc::new(std::sync::atomic::AtomicBool::new(false));
    let c2 = called.clone();
    let f: AuthFunc = Arc::new(move |_| {
        c2.store(true, std::sync::atomic::Ordering::SeqCst);
        Ok(Claims {
            subject: "token".into(),
            ..Default::default()
        })
    });
    cfg.auth = Some(f);
    let run = start(cfg).await;
    let client = ca.client("carol.example.org");
    let raw = https_get(
        &run,
        client_config(&ca, &[b"http/1.1"], &all_versions(), Some(&client)),
        "/who",
    )
    .await
    .unwrap();
    assert_eq!(header(&raw, "x-subject"), Some("carol.example.org"));
    assert!(!called.load(std::sync::atomic::Ordering::SeqCst));
    run.stop().await;
}

#[test]
fn mtls_counts_as_authentication_for_a_remote_bind() {
    let ca = ca("test ca");
    let s = ca.server();
    let build = |mtls: bool| {
        let mut cfg = ApiServerConfig::new("0.0.0.0:0", app());
        let mut t = TlsConfig::from_pem(s.cert_pem.clone(), s.key_pem.clone());
        if mtls {
            t = t.mtls(ca.pem.clone(), ClientCertConfig::default());
        }
        cfg.tls = Some(t);
        ApiService::build(cfg).unwrap().validate()
    };
    assert!(build(false).unwrap().contains("no authentication"));
    assert_eq!(build(true), None);
}

#[test]
fn build_refuses_what_cannot_serve() {
    let ca = ca("test ca");
    let s = ca.server();
    let other = ca.server();
    let build = |t: TlsConfig| {
        let mut cfg = config(app());
        cfg.tls = Some(t);
        ApiService::build(cfg).map(|_| ()).unwrap_err().0
    };
    assert!(build(TlsConfig::from_pem("", s.key_pem.clone())).contains("no PEM certificate"));
    assert!(build(TlsConfig::from_pem(s.cert_pem.clone(), "")).contains("tls key"));
    assert!(
        build(TlsConfig::from_pem(
            s.cert_pem.clone(),
            other.key_pem.clone()
        ))
        .contains("certificate and key"),
        "a key that does not match its certificate"
    );
    let ok = || TlsConfig::from_pem(s.cert_pem.clone(), s.key_pem.clone());
    assert!(build(ok().mtls("", ClientCertConfig::default())).contains("no PEM certificate"));
    assert!(build(ok().mtls(
        ca.pem.clone(),
        ClientCertConfig {
            tenant_oid: Some("2.5.4.10".into()),
            tenant_san_pattern: Some("x".into()),
            ..Default::default()
        }
    ))
    .contains("one tenant source"));
    assert!(build(ok().mtls(
        ca.pem.clone(),
        ClientCertConfig {
            tenant_oid: Some("not-an-oid".into()),
            ..Default::default()
        }
    ))
    .contains("dotted OID"));
    assert!(build(ok().mtls(
        ca.pem.clone(),
        ClientCertConfig {
            tenant_san_pattern: Some("(".into()),
            ..Default::default()
        }
    ))
    .contains("tenant_san_pattern"));
}

// --- the client-certificate corpus ---------------------------------------

#[test]
fn client_cert_corpus() {
    let path = std::path::PathBuf::from(env!("CARGO_MANIFEST_DIR"))
        .join("../../contracts/parity/api.json");
    let corpus: Value = serde_json::from_slice(&std::fs::read(path).unwrap()).unwrap();
    let strs = |v: &Value| -> Vec<String> {
        v.as_array()
            .map(|a| a.iter().map(|s| s.as_str().unwrap().to_string()).collect())
            .unwrap_or_default()
    };
    let ca = ca("corpus ca");
    for case in corpus["client_cert"].as_array().unwrap() {
        let name = case["name"].as_str().unwrap();
        let leaf = Leaf {
            cn: case["cn"].as_str().map(str::to_string),
            uris: strs(&case["uris"]),
            dns: strs(&case["dns"]),
            emails: strs(&case["emails"]),
            org: strs(&case["org"]),
            org_unit: strs(&case["org_unit"]),
            ext: case["extension"].as_object().map(|e| {
                (
                    e["oid"]
                        .as_str()
                        .unwrap()
                        .split('.')
                        .map(|a| a.parse().unwrap())
                        .collect(),
                    e["value"].as_str().unwrap().to_string(),
                )
            }),
            client: true,
            ..Leaf::default()
        };
        let der = ca.issue(&leaf).cert_der;
        let principal = Principal::parse(case["principal"].as_str().unwrap_or(""));
        let got = principal.map_err(|e| e.0).and_then(|principal| {
            client_cert_claims(
                &der,
                &ClientCertConfig {
                    principal,
                    tenant_oid: case["tenant_oid"].as_str().map(str::to_string),
                    tenant_san_pattern: case["tenant_san_pattern"].as_str().map(str::to_string),
                },
            )
        });
        if case["error"].as_bool().unwrap_or(false) {
            assert!(got.is_err(), "{name}: {got:?}");
            continue;
        }
        let c = got.unwrap_or_else(|e| panic!("{name}: {e}"));
        assert_eq!(
            c.subject,
            case["want"]["subject"].as_str().unwrap(),
            "{name}"
        );
        assert_eq!(c.tenant, case["want"]["tenant"].as_str().unwrap(), "{name}");
    }
    assert!(Principal::SOURCES
        .iter()
        .all(|s| Principal::parse(s).unwrap().as_str() == *s));
}
