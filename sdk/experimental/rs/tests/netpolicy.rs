// Mirrors go/core/netpolicy's suite for the reqwest client. Cargo
// compiles every file under tests/ unconditionally, so the whole body is
// gated on the `api` feature — the client lives beneath it and the
// unresolved imports would otherwise break `cargo test` under default
// features. The policy core's tables are in tests/netpolicy_core.rs.
#![cfg(feature = "api")]

use hop_top_kit::netpolicy::{GuardedClient, NetError, NetPolicy, OfflineError, Redirects};

// A policy-marked client must stop an external request before it opens
// a socket. The destination is deliberately unroutable: loopback is
// exempt by design, so a mockito server would legitimately be allowed
// through and prove nothing.
#[tokio::test]
async fn blocks_external_when_offline() {
    let client = GuardedClient::new(NetPolicy::offline()).expect("build client");
    let err = client
        .get("https://example.invalid/v1/thing")
        .send()
        .await
        .expect_err("expected offline refusal");

    let offline = err
        .as_offline()
        .expect("error did not carry the typed offline variant");
    assert_eq!(offline.method, "GET");
    assert_eq!(offline.url, "https://example.invalid/v1/thing");
}

// Loopback stays reachable when offline: --offline means "no network",
// not "cannot talk to myself". A real server on 127.0.0.1 proves the
// request reached the wire rather than being refused.
#[tokio::test]
async fn allows_loopback_when_offline() {
    let mut server = mockito::Server::new_async().await;
    let mock = server
        .mock("GET", "/health")
        .with_status(204)
        .expect(1)
        .create_async()
        .await;

    let client = GuardedClient::new(NetPolicy::offline()).expect("build client");
    let resp = client
        .get(format!("{}/health", server.url()))
        .send()
        .await
        .expect("loopback request was refused while offline");

    assert_eq!(resp.status().as_u16(), 204);
    mock.assert_async().await;
}

// Every loopback spelling Go exempts must be exempt here too. Whether
// anything is listening on these ports is the host's business: the
// assertion is only that the guard did not refuse the request, so a
// connection-refused transport error and a real response both pass.
#[tokio::test]
async fn loopback_spellings_are_exempt() {
    let client = GuardedClient::new(NetPolicy::offline()).expect("build client");
    for target in [
        "http://127.0.0.1:8080/health",
        "http://localhost:9000/health",
        "http://[::1]:9000/health",
    ] {
        if let Err(err) = client.get(target).send().await {
            assert!(
                err.as_offline().is_none(),
                "{target}: loopback was refused by the offline guard"
            );
        }
    }
}

// `NetPolicy::allows` over URLs, Go's Guard rows: loopback in every
// spelling stays reachable offline, bracketed hosts without a port
// included; anything else is refused.
#[test]
fn allows_url_table() {
    let off = NetPolicy::offline();
    for raw in [
        "http://127.0.0.1:8080/health",
        "http://localhost:9000/health",
        "http://[::1]:9000/health",
        "http://[::1]/health",
        "http://[::1]:80/health",
        "http://[::ffff:127.0.0.1]/health",
    ] {
        let url: reqwest::Url = raw.parse().unwrap();
        assert!(off.allows(&url), "{raw} refused while offline");
    }
    for raw in [
        "https://example.invalid/v1/thing",
        "http://my-host.internal/health",
        "http://[2001:db8::1]/health",
        "http://10.0.0.5:443/",
        "mailto:alice@example.invalid",
    ] {
        let url: reqwest::Url = raw.parse().unwrap();
        assert!(!off.allows(&url), "{raw} allowed while offline");
        assert!(NetPolicy::online().allows(&url), "{raw} refused online");
    }
}

// A DNS name is remote even if it might resolve to loopback: resolving
// it is itself network access.
#[tokio::test]
async fn blocks_dns_names_when_offline() {
    let client = GuardedClient::new(NetPolicy::offline()).expect("build client");
    let err = client
        .get("http://my-host.internal/health")
        .send()
        .await
        .expect_err("expected offline refusal");

    assert!(
        err.as_offline().is_some(),
        "DNS-named host was allowed through: {err}"
    );
}

// An online policy must leave traffic entirely unaffected.
#[tokio::test]
async fn allows_external_when_online() {
    let mut server = mockito::Server::new_async().await;
    let mock = server
        .mock("GET", "/health")
        .with_status(204)
        .expect(1)
        .create_async()
        .await;

    let client = GuardedClient::new(NetPolicy::online()).expect("build client");
    let resp = client
        .get(format!("{}/health", server.url()))
        .send()
        .await
        .expect("online request failed");

    assert_eq!(resp.status().as_u16(), 204);
    mock.assert_async().await;
}

// The typed variant must be matchable through the std error chain, the
// way callers match Go's ErrOffline with errors.Is.
#[tokio::test]
async fn offline_error_is_matchable_via_source_chain() {
    let client = GuardedClient::new(NetPolicy::offline()).expect("build client");
    let err = client
        .get("https://example.invalid/x")
        .send()
        .await
        .expect_err("expected offline refusal");

    // The message names the flag, the way Go's ErrOffline does.
    assert!(
        err.to_string().contains("network disabled by --offline"),
        "message did not name the flag: {err}"
    );
    // And the typed refusal survives boxing as a std error, so a caller
    // holding a `Box<dyn Error>` can still recover it.
    let boxed: Box<dyn std::error::Error> = Box::new(err);
    let recovered = boxed
        .downcast_ref::<NetError>()
        .and_then(NetError::as_offline)
        .expect("offline variant lost through Box<dyn Error>");
    assert_eq!(recovered.method, "GET");
    let _: &OfflineError = recovered;
}

// Enforcement must sit beneath a naive caller: code that constructs the
// client through the port's shared path and sends a request with no
// offline awareness at all is still refused. This is the guarantee
// test — a marker with no chokepoint fails here.
#[tokio::test]
async fn naive_caller_is_refused() {
    async fn naive(client: &GuardedClient) -> Result<u16, NetError> {
        // No policy check anywhere in this function.
        Ok(client
            .get("https://api.example.com/v1/items")
            .send()
            .await?
            .status()
            .as_u16())
    }

    let client = GuardedClient::new(NetPolicy::offline()).expect("build client");
    let err = naive(&client)
        .await
        .expect_err("naive caller reached the wire");
    assert!(
        err.as_offline().is_some(),
        "naive caller was not refused: {err}"
    );
}

// ApiClient is the port's public HTTP surface: constructing it under an
// offline policy must refuse external calls, and callers must be able
// to tell a refusal apart from an ordinary transport failure.
#[tokio::test]
async fn api_client_honours_offline_policy() {
    use hop_top_kit::api::{ApiClient, OFFLINE_CODE};

    let client = ApiClient::with_policy("https://api.example.com", NetPolicy::offline())
        .expect("build api client");
    let err = client
        .get::<serde_json::Value>("abc")
        .await
        .expect_err("api client reached the wire while offline");

    assert_eq!(err.code, OFFLINE_CODE);
    assert_eq!(err.status, 0);
    assert!(err.message.contains("network disabled by --offline"));
}

// The default ApiClient constructor stays online, so existing callers
// are unaffected until the CLI layer wires the flag.
#[tokio::test]
async fn api_client_default_is_online() {
    let mut server = mockito::Server::new_async().await;
    let mock = server
        .mock("GET", "/abc")
        .with_status(200)
        .with_header("content-type", "application/json")
        .with_body(r#"{"ok":true}"#)
        .expect(1)
        .create_async()
        .await;

    let client = hop_top_kit::api::ApiClient::new(&server.url());
    let v: serde_json::Value = client.get("abc").await.expect("online request failed");
    assert_eq!(v["ok"], true);
    mock.assert_async().await;
}

// Telemetry is logging-class egress: `--offline` stops traffic the user
// asked for, it is not a second consent gate on diagnostics. Consent and
// telemetry mode already govern whether anything is emitted at all.
//
// The sink builds a plain `reqwest::Client`, never a `GuardedClient`, so
// there is no policy knob a CLI could thread through to mute it. This
// pins the construction path: an Https sink must build without any
// network policy being supplied. Gated on `telemetry`, which `api` alone
// does not enable.
#[cfg(feature = "telemetry")]
#[tokio::test]
async fn telemetry_https_sink_builds_without_a_net_policy() {
    use hop_top_kit::telemetry::{Client, ClientOptions, SinkKind};

    let built = Client::new(ClientOptions {
        sink: SinkKind::Https,
        endpoint: Some("https://telemetry.example.com/v1/events".into()),
        queue_size: 4,
        ..Default::default()
    });

    assert!(
        built.is_ok(),
        "https telemetry sink must construct with no net policy: \
         --offline must not be able to suppress logging-class egress"
    );
}

// The refusal names where the request was going, never what it carried:
// scheme, host and path only. Query, fragment and userinfo may hold
// credentials (an API key param, basic-auth userinfo) and are dropped.
// Mirrors Go's TestGuard_RefusalOmitsQueryFragmentUserinfo.
const SECRET_URL: &str = "https://alice:pw-secret@example.invalid/v1/models/m:generate\
                          ?alt=sse&key=q-secret#frag-secret";
const LEAKS: [&str; 9] = [
    "alice",
    "pw-secret",
    "q-secret",
    "key=",
    "alt=sse",
    "frag-secret",
    "?",
    "#",
    "@",
];

fn assert_no_leak(msg: &str) {
    for leak in LEAKS {
        assert!(!msg.contains(leak), "refusal {msg:?} carries {leak:?}");
    }
}

#[tokio::test]
async fn refusal_omits_query_fragment_userinfo() {
    let client = GuardedClient::new(NetPolicy::offline()).expect("build client");
    let err = client
        .post(SECRET_URL)
        .query(&[("extra", "q-secret")])
        .send()
        .await
        .expect_err("expected offline refusal");

    assert!(err.is_offline(), "not an offline refusal: {err}");
    let offline = err.as_offline().expect("typed offline variant");
    assert_eq!(offline.url, "https://example.invalid/v1/models/m:generate");
    let msg = err.to_string();
    assert_eq!(
        msg,
        "POST https://example.invalid/v1/models/m:generate: network disabled by --offline"
    );
    assert_no_leak(&msg);
    assert_no_leak(&format!("{err:?}"));
}

// The port is part of where the request was going, so it stays.
#[tokio::test]
async fn refusal_keeps_port() {
    let client = GuardedClient::new(NetPolicy::offline()).expect("build client");
    let err = client
        .get("http://u:p@example.invalid:8443/x?key=q-secret")
        .send()
        .await
        .expect_err("expected offline refusal");

    assert_eq!(
        err.to_string(),
        "GET http://example.invalid:8443/x: network disabled by --offline"
    );
}

// ApiClient surfaces the refusal as its message; the same stripping holds.
#[tokio::test]
async fn api_client_refusal_omits_query() {
    use hop_top_kit::api::{ApiClient, OFFLINE_CODE};

    let client = ApiClient::with_policy(
        "https://alice:pw-secret@api.example.com",
        NetPolicy::offline(),
    )
    .expect("build api client");
    let err = client
        .get::<serde_json::Value>("v1/items?key=q-secret#frag-secret")
        .await
        .expect_err("api client reached the wire while offline");

    assert_eq!(err.code, OFFLINE_CODE);
    assert_no_leak(&err.message);
    assert!(err.message.contains("network disabled by --offline"));
}

// A genuine transport failure carries the same stripped destination as a
// refusal. reqwest quotes its error URL in Display and Debug, so the
// guard swaps it for scheme, host (with port) and path before handing
// the error back. Nothing the request carried may surface through
// Display, Debug or the source chain.
fn closed_port() -> u16 {
    let l = std::net::TcpListener::bind("127.0.0.1:0").expect("bind");
    l.local_addr().expect("addr").port()
}

fn assert_chain_no_leak(err: &NetError) {
    assert_no_leak(&format!("{err}"));
    assert_no_leak(&format!("{err:?}"));
    let mut src = std::error::Error::source(err);
    while let Some(s) = src {
        assert_no_leak(&format!("{s}"));
        assert_no_leak(&format!("{s:?}"));
        src = s.source();
    }
}

#[tokio::test]
async fn transport_error_omits_query_fragment_userinfo() {
    let port = closed_port();
    let client = GuardedClient::new(NetPolicy::offline()).expect("build client");
    let err = client
        .post(format!(
            "http://alice:pw-secret@127.0.0.1:{port}/v1/models/m:generate\
             ?alt=sse&key=q-secret#frag-secret"
        ))
        .query(&[("extra", "q-secret")])
        .send()
        .await
        .expect_err("closed port answered");

    assert!(!err.is_offline(), "loopback refused: {err}");
    let NetError::Transport(t) = &err else {
        panic!("not a transport error: {err:?}");
    };
    assert!(t.is_connect() || t.is_request(), "unexpected kind: {t:?}");
    assert_eq!(
        t.url().map(|u| u.as_str()),
        Some(format!("http://127.0.0.1:{port}/v1/models/m:generate").as_str())
    );
    assert_eq!(
        err.to_string(),
        format!("error sending request for url (http://127.0.0.1:{port}/v1/models/m:generate)")
    );
    assert_chain_no_leak(&err);
}

// reqwest rejects a non-HTTP scheme before sending and quotes the URL in
// that error too. Nothing reaches the wire.
#[tokio::test]
async fn builder_error_omits_query_fragment_userinfo() {
    let client = GuardedClient::new(NetPolicy::online()).expect("build client");
    let err = client
        .get("ftp://alice:pw-secret@example.invalid/x?key=q-secret#frag-secret")
        .send()
        .await
        .expect_err("ftp request succeeded");

    assert!(!err.is_offline());
    assert_eq!(
        err.to_string(),
        "builder error for url (ftp://example.invalid/x)"
    );
    assert_chain_no_leak(&err);
}

// The allowlist applies to the guarded client: an unlisted host is
// refused before a socket opens, with the typed variant and an audit
// record; a listed one (here the loopback mock) is sent.
#[tokio::test]
async fn allowlist_refuses_unlisted_hosts_and_audits() {
    use std::sync::{Arc, Mutex};

    let seen: Arc<Mutex<Vec<String>>> = Arc::default();
    let sink = seen.clone();
    let policy = NetPolicy::online()
        .with_allow_hosts(["graph.microsoft.com"])
        .with_audit(move |r| sink.lock().unwrap().push(format!("{} {}", r.op, r.dest)));
    let client = GuardedClient::new(policy).expect("build client");

    let err = client
        .post("https://u:p@evil.example.com/v1/x?key=q-secret")
        .send()
        .await
        .expect_err("unlisted host was sent");
    let h = err.as_host_not_allowed().expect("typed variant");
    assert_eq!(h.method, "POST");
    assert_eq!(h.url, "https://evil.example.com/v1/x");
    assert!(!err.is_offline());
    assert_eq!(
        *seen.lock().unwrap(),
        vec!["POST https://evil.example.com/v1/x".to_string()]
    );

    let mut server = mockito::Server::new_async().await;
    let mock = server
        .mock("GET", "/ok")
        .with_status(204)
        .create_async()
        .await;
    let resp = client
        .get(format!("{}/ok", server.url()))
        .send()
        .await
        .expect("loopback is exempt from the allowlist");
    assert_eq!(resp.status().as_u16(), 204);
    mock.assert_async().await;
}

// reqwest follows redirects by default; a policy that judged only the
// first URL would let an allowed (here: loopback) host bounce the
// request to a disallowed one. Every hop is judged, the refusal is the
// typed variant, and it is audited.
#[tokio::test]
async fn redirect_to_a_refused_host_is_refused_and_audited() {
    use std::sync::{Arc, Mutex};

    for offline in [false, true] {
        let mut server = mockito::Server::new_async().await;
        let _m = server
            .mock("GET", "/hop")
            .with_status(302)
            .with_header("location", "https://evil.example.invalid/steal?k=q-secret")
            .create_async()
            .await;

        let seen: Arc<Mutex<Vec<String>>> = Arc::default();
        let sink = seen.clone();
        let base = if offline {
            NetPolicy::offline()
        } else {
            NetPolicy::online().with_allow_hosts(["graph.microsoft.com"])
        };
        let client = GuardedClient::new(base.with_audit(move |r| {
            sink.lock().unwrap().push(r.dest.clone());
        }))
        .expect("build client");

        let err = client
            .get(format!("{}/hop", server.url()))
            .send()
            .await
            .expect_err("redirect to a refused host was followed");
        if offline {
            assert!(err.is_offline(), "offline={offline}: {err}");
        } else {
            assert!(err.is_host_not_allowed(), "offline={offline}: {err}");
        }
        assert_eq!(
            *seen.lock().unwrap(),
            vec!["https://evil.example.invalid/steal".to_string()],
            "offline={offline}"
        );
    }
}

// A redirect between permitted hosts still works.
#[tokio::test]
async fn redirect_between_permitted_hosts_is_followed() {
    let mut server = mockito::Server::new_async().await;
    let target = format!("{}/final", server.url());
    let _a = server
        .mock("GET", "/hop")
        .with_status(302)
        .with_header("location", &target)
        .create_async()
        .await;
    let _b = server
        .mock("GET", "/final")
        .with_status(204)
        .create_async()
        .await;
    let client = GuardedClient::new(NetPolicy::offline()).expect("build client");
    let resp = client
        .get(format!("{}/hop", server.url()))
        .send()
        .await
        .expect("loopback redirect refused");
    assert_eq!(resp.status().as_u16(), 204);
}

// A caller that opts out of redirects (the llm adapter does, so a
// credential never moves to a host the server names) gets the 3xx back,
// not a followed hop and not a refusal.
#[tokio::test]
async fn none_mode_returns_the_redirect_unfollowed() {
    let mut server = mockito::Server::new_async().await;
    let _m = server
        .mock("GET", "/hop")
        .with_status(302)
        .with_header("location", "https://evil.example.invalid/x")
        .create_async()
        .await;
    let client = GuardedClient::build(
        NetPolicy::online().with_allow_hosts(["graph.microsoft.com"]),
        reqwest::Client::builder(),
        Redirects::None,
        reqwest::header::HeaderMap::new(),
    )
    .expect("build client");
    let resp = client
        .get(format!("{}/hop", server.url()))
        .send()
        .await
        .expect("a 3xx is a response, not an error");
    assert_eq!(resp.status().as_u16(), 302);
}

// Follow mode: a hop to another origin is refused when the request
// carries a credential, and nothing is sent to that origin. Without a
// credential the hop is followed.
#[tokio::test]
async fn cross_origin_hop_with_credentials_is_refused() {
    for (name, value) in [
        ("authorization", "Bearer t-secret"),
        ("x-api-key", "k-secret"),
        ("cookie", "s=1-secret"),
    ] {
        let mut a = mockito::Server::new_async().await;
        let mut b = mockito::Server::new_async().await;
        let _m = a
            .mock("GET", "/hop")
            .with_status(302)
            .with_header("location", &format!("{}/final", b.url()))
            .expect_at_least(1)
            .create_async()
            .await;
        let hit = b
            .mock("GET", "/final")
            .with_status(204)
            .expect(0)
            .create_async()
            .await;

        let client = GuardedClient::new(NetPolicy::online()).expect("build client");
        let err = client
            .get(format!("{}/hop", a.url()))
            .header(name, value)
            .send()
            .await
            .expect_err(name);
        assert!(
            matches!(err, NetError::Redirect(_)),
            "{name}: not the redirect refusal: {err}"
        );
        assert!(!err.to_string().contains("secret"), "{name}: leaked: {err}");
        hit.assert_async().await;
        hit.remove_async().await;

        // No credential: followed.
        let ok = b
            .mock("GET", "/final")
            .with_status(204)
            .expect(1)
            .create_async()
            .await;
        let resp = client
            .get(format!("{}/hop", a.url()))
            .send()
            .await
            .expect("credential-free hop followed");
        assert_eq!(resp.status().as_u16(), 204);
        ok.assert_async().await;
    }
}

// A same-origin hop keeps the credential and is followed; a loop stops.
#[tokio::test]
async fn same_origin_hop_is_followed_and_loops_are_bounded() {
    let mut s = mockito::Server::new_async().await;
    let _a = s
        .mock("GET", "/a")
        .with_status(302)
        .with_header("location", "/b")
        .create_async()
        .await;
    let _b = s
        .mock("GET", "/b")
        .match_header("authorization", "Bearer t")
        .with_status(204)
        .create_async()
        .await;
    let _l = s
        .mock("GET", "/loop")
        .with_status(302)
        .with_header("location", "/loop")
        .expect_at_least(1)
        .create_async()
        .await;
    let client = GuardedClient::new(NetPolicy::online()).expect("build client");
    let resp = client
        .get(format!("{}/a", s.url()))
        .bearer_auth("t")
        .send()
        .await
        .unwrap();
    assert_eq!(resp.status().as_u16(), 204);
    let err = client
        .get(format!("{}/loop", s.url()))
        .send()
        .await
        .unwrap_err();
    assert!(err.to_string().contains("too many redirects"), "{err}");
}

// A cross-origin hop carries the safe allowlist and nothing else: the
// second origin's server sees Accept (per-request) and User-Agent
// (default), and none of the sensitive or custom headers. A request
// that does carry one is refused before anything is sent.
#[tokio::test]
async fn cross_origin_hop_forwards_only_safe_headers() {
    use mockito::Matcher;
    let mut a = mockito::Server::new_async().await;
    let mut b = mockito::Server::new_async().await;
    let _m = a
        .mock("GET", "/hop")
        .with_status(302)
        .with_header("location", &format!("{}/final", b.url()))
        .expect_at_least(1)
        .create_async()
        .await;
    let hit = b
        .mock("GET", "/final")
        .match_header("accept", "application/json")
        .match_header("user-agent", "kit-test")
        .match_header("authorization", Matcher::Missing)
        .match_header("x-api-key", Matcher::Missing)
        .match_header("cookie", Matcher::Missing)
        .match_header("x-internal-trace", Matcher::Missing)
        .with_status(204)
        .expect(1)
        .create_async()
        .await;
    let mut defaults = reqwest::header::HeaderMap::new();
    defaults.insert("user-agent", "kit-test".parse().unwrap());
    let client = GuardedClient::build(
        NetPolicy::online(),
        reqwest::Client::builder(),
        Redirects::default(),
        defaults,
    )
    .expect("build client");
    let resp = client
        .get(format!("{}/hop", a.url()))
        .header("accept", "application/json")
        .send()
        .await
        .expect("followed");
    assert_eq!(resp.status().as_u16(), 204);
    hit.assert_async().await;
}

// Per-request and default headers alike: anything outside the allowlist,
// credential or custom, refuses the hop, and the second origin hears
// nothing.
#[tokio::test]
async fn unsafe_headers_refuse_the_hop_whether_default_or_per_request() {
    for (name, default) in [
        ("authorization", true),
        ("authorization", false),
        ("x-api-key", true),
        ("cookie", false),
        ("x-internal-trace", true),
        ("x-internal-trace", false),
    ] {
        let mut a = mockito::Server::new_async().await;
        let mut b = mockito::Server::new_async().await;
        let _m = a
            .mock("GET", "/hop")
            .with_status(302)
            .with_header("location", &format!("{}/final", b.url()))
            .expect_at_least(1)
            .create_async()
            .await;
        let hit = b
            .mock("GET", "/final")
            .with_status(204)
            .expect(0)
            .create_async()
            .await;

        let mut defaults = reqwest::header::HeaderMap::new();
        if default {
            defaults.insert(name, "d-secret".parse().unwrap());
        }
        let client = GuardedClient::build(
            NetPolicy::online(),
            reqwest::Client::builder(),
            Redirects::default(),
            defaults,
        )
        .expect("build client");
        let mut rb = client.get(format!("{}/hop", a.url()));
        if !default {
            rb = rb.header(name, "r-secret");
        }
        let err = rb.send().await.expect_err(name);
        assert!(
            matches!(err, NetError::Redirect(_)),
            "{name}/{default}: {err}"
        );
        assert!(!err.to_string().contains("secret"), "{err}");
        hit.assert_async().await;
    }
}
