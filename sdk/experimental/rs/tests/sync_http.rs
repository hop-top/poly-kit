//! Ports the HTTPTransport half of go/runtime/sync transport_test.go,
//! against a local mock server, plus the guard: every request goes
//! through the netpolicy client, so `--offline` refuses a remote host.
#![cfg(feature = "sync-http")]

use hop_top_kit::bus::Context;
use hop_top_kit::netpolicy::NetPolicy;
use hop_top_kit::sync::{Diff, HttpTransport, Operation, Timestamp, Transport};
use mockito::{Matcher, Server};

fn ctx() -> Context {
    Context::background()
}

fn diff(id: &str, physical: i64) -> Diff {
    Diff {
        entity_id: id.into(),
        operation: Operation::Create,
        timestamp: Timestamp {
            physical,
            logical: 0,
            node_id: "n1".into(),
        },
        node_id: "n1".into(),
        ..Diff::default()
    }
}

#[test]
fn push_posts_the_diffs_as_json_with_the_bearer_token() {
    let mut srv = Server::new();
    let m = srv
        .mock("POST", "/sync/push")
        .match_header("authorization", "Bearer secret")
        .match_header("content-type", "application/json")
        .match_body(Matcher::PartialJson(serde_json::json!([
            {"entity_id": "e1", "operation": 0, "node_id": "n1"}
        ])))
        .with_status(200)
        .create();

    let tr = HttpTransport::new(&srv.url()).with_auth_token("secret");
    tr.push(&ctx(), &[diff("e1", 1)]).unwrap();
    m.assert();
}

#[test]
fn pull_sends_the_watermark_and_decodes_the_diffs() {
    let mut srv = Server::new();
    let body = serde_json::to_string(&[diff("e1", 100)]).unwrap();
    let m = srv
        .mock("GET", "/sync/pull")
        .match_query(Matcher::AllOf(vec![
            Matcher::UrlEncoded("since_physical".into(), "7".into()),
            Matcher::UrlEncoded("since_logical".into(), "3".into()),
            Matcher::UrlEncoded("since_node".into(), "peer a".into()),
        ]))
        .with_status(200)
        .with_header("content-type", "application/json")
        .with_body(body)
        .create();

    let tr = HttpTransport::new(&srv.url());
    let since = Timestamp {
        physical: 7,
        logical: 3,
        node_id: "peer a".into(),
    };
    let got = tr.pull(&ctx(), &since).unwrap();
    m.assert();
    assert_eq!(got.len(), 1);
    assert_eq!(got[0].entity_id, "e1");
}

#[test]
fn pull_accepts_null_as_no_diffs() {
    let mut srv = Server::new();
    srv.mock("GET", "/sync/pull")
        .match_query(Matcher::Any)
        .with_status(200)
        .with_body("null")
        .create();
    let tr = HttpTransport::new(&srv.url());
    assert!(tr.pull(&ctx(), &Timestamp::default()).unwrap().is_empty());
}

#[test]
fn ping_hits_sync_ping() {
    let mut srv = Server::new();
    let m = srv.mock("GET", "/sync/ping").with_status(200).create();
    HttpTransport::new(&srv.url()).ping(&ctx()).unwrap();
    m.assert();
}

#[test]
fn a_trailing_slash_on_the_base_url_is_harmless() {
    let mut srv = Server::new();
    let m = srv.mock("GET", "/sync/ping").with_status(200).create();
    HttpTransport::new(&format!("{}/", srv.url()))
        .ping(&ctx())
        .unwrap();
    m.assert();
}

#[test]
fn non_200_statuses_are_errors_naming_the_status() {
    let mut srv = Server::new();
    srv.mock("POST", "/sync/push").with_status(500).create();
    srv.mock("GET", "/sync/pull")
        .match_query(Matcher::Any)
        .with_status(404)
        .create();
    srv.mock("GET", "/sync/ping").with_status(503).create();

    let tr = HttpTransport::new(&srv.url());
    assert_eq!(
        tr.push(&ctx(), &[diff("e1", 1)]).unwrap_err().to_string(),
        "push: status 500"
    );
    assert_eq!(
        tr.pull(&ctx(), &Timestamp::default())
            .unwrap_err()
            .to_string(),
        "pull: status 404"
    );
    assert_eq!(tr.ping(&ctx()).unwrap_err().to_string(), "ping: status 503");
}

#[test]
fn a_malformed_pull_body_is_a_decode_error() {
    let mut srv = Server::new();
    srv.mock("GET", "/sync/pull")
        .match_query(Matcher::Any)
        .with_status(200)
        .with_body("not json")
        .create();
    let err = HttpTransport::new(&srv.url())
        .pull(&ctx(), &Timestamp::default())
        .unwrap_err();
    assert!(err.to_string().starts_with("decode diffs: "), "{err}");
}

#[test]
fn an_unreachable_server_is_a_transport_error() {
    // Port 1 (tcpmux): nothing listens, the connect is refused.
    assert!(HttpTransport::new("http://127.0.0.1:1")
        .ping(&ctx())
        .is_err());
}

#[test]
fn offline_policy_refuses_a_remote_host_but_not_loopback() {
    let tr =
        HttpTransport::with_policy("http://sync.example.invalid", NetPolicy::offline()).unwrap();
    let err = tr.ping(&ctx()).unwrap_err();
    assert!(err.to_string().contains("--offline"), "{err}");

    let mut srv = Server::new();
    srv.mock("GET", "/sync/ping").with_status(200).create();
    HttpTransport::with_policy(&srv.url(), NetPolicy::offline())
        .unwrap()
        .ping(&ctx())
        .unwrap();
}

#[tokio::test]
async fn async_methods_work_inside_a_runtime() {
    let mut srv = Server::new_async().await;
    srv.mock("GET", "/sync/ping")
        .with_status(200)
        .create_async()
        .await;
    HttpTransport::new(&srv.url()).ping_async().await.unwrap();
}

#[test]
fn a_replicator_round_runs_over_http() {
    use hop_top_kit::domain::{Entity, Query, RepoError, Repository};
    use hop_top_kit::sync::{Remote, Replicator, SyncMode};
    use serde::{Deserialize, Serialize};
    use std::sync::Arc;

    #[derive(Clone, Serialize, Deserialize)]
    struct Item {
        id: String,
    }
    impl Entity for Item {
        fn id(&self) -> &str {
            &self.id
        }
    }
    struct Nop;
    impl Repository<Item> for Nop {
        fn create(&self, _: &Context, _: &Item) -> Result<(), RepoError> {
            Ok(())
        }
        fn get(&self, _: &Context, _: &str) -> Result<Item, RepoError> {
            Err(RepoError::NotFound)
        }
        fn list(&self, _: &Context, _: &Query) -> Result<Vec<Item>, RepoError> {
            Ok(vec![])
        }
        fn update(&self, _: &Context, _: &Item) -> Result<(), RepoError> {
            Ok(())
        }
        fn delete(&self, _: &Context, _: &str) -> Result<(), RepoError> {
            Ok(())
        }
    }

    let mut srv = Server::new();
    let ping = srv.mock("GET", "/sync/ping").with_status(200).create();
    let push = srv.mock("POST", "/sync/push").with_status(200).create();
    let pull = srv
        .mock("GET", "/sync/pull")
        .match_query(Matcher::Any)
        .with_status(200)
        .with_body("[]")
        .create();

    let rep = Replicator::builder(Arc::new(Nop))
        .remote(
            Remote::new("peer", Arc::new(HttpTransport::new(&srv.url())))
                .mode(SyncMode::Bidirectional),
        )
        .build();
    rep.enqueue(diff("e1", 1));
    rep.sync_once(&ctx(), "peer").unwrap();

    ping.assert();
    push.assert();
    pull.assert();
    let st = rep.status();
    assert!(st[0].connected && st[0].last_error.is_none());
    assert_eq!(st[0].pending_diffs, 0);
}

#[test]
fn host_allowlist_refuses_an_unlisted_host_but_not_loopback() {
    let policy = NetPolicy::online().with_allow_hosts(["sync.example"]);
    let tr = HttpTransport::with_policy("http://other.example.invalid", policy.clone()).unwrap();
    let err = tr.ping(&ctx()).unwrap_err();
    assert!(
        err.to_string()
            .contains("host not allowed by netpolicy.allow_hosts"),
        "{err}"
    );

    let mut srv = Server::new();
    srv.mock("GET", "/sync/ping").with_status(200).create();
    HttpTransport::with_policy(&srv.url(), policy)
        .unwrap()
        .ping(&ctx())
        .unwrap();
}
