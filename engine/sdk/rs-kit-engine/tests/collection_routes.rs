#![cfg(feature = "http")]

use hop_top_kit_engine::{CollectionQuery, EngineError, KitEngine};
use mockito::Matcher;
use serde::{Deserialize, Serialize};
use serde_json::json;

#[derive(Debug, Serialize, Deserialize, PartialEq)]
struct Note {
    title: String,
}

const DOC: &str = r#"{"type":"notes","id":"a b","data":{"title":"Hi"},"created_at":"2026-01-01T00:00:00Z","updated_at":"2026-01-01T00:00:01Z"}"#;

async fn engine(s: &mut mockito::ServerGuard) -> KitEngine {
    s.mock("GET", "/health")
        .with_body(r#"{"pid":1}"#)
        .create_async()
        .await;
    let port: u16 = s
        .host_with_port()
        .rsplit(':')
        .next()
        .unwrap()
        .parse()
        .unwrap();
    KitEngine::connect(port, Some("tok".into()), None)
        .await
        .unwrap()
}

#[tokio::test]
async fn create_posts_body_to_trailing_slash_route_with_bearer() {
    let mut s = mockito::Server::new_async().await;
    let e = engine(&mut s).await;
    let m = s
        .mock("POST", "/notes/")
        .match_header("authorization", "Bearer tok")
        .match_header("content-type", "application/json")
        .match_body(Matcher::Json(json!({"title":"Hi"})))
        .with_status(201)
        .with_body(DOC)
        .create_async()
        .await;
    let r = e
        .collection::<Note>("notes")
        .create(Note { title: "Hi".into() })
        .await
        .unwrap();
    m.assert_async().await;
    assert_eq!(r.doc_type, "notes");
    assert_eq!(r.id, "a b");
    assert_eq!(r.data, Note { title: "Hi".into() });
}

#[tokio::test]
async fn get_encodes_id_and_sends_no_auth() {
    let mut s = mockito::Server::new_async().await;
    let e = engine(&mut s).await;
    let m = s
        .mock("GET", "/notes/a%20b")
        .match_header("authorization", Matcher::Missing)
        .with_body(DOC)
        .create_async()
        .await;
    let r = e.collection::<Note>("notes").get("a b").await.unwrap();
    m.assert_async().await;
    assert_eq!(r.updated_at, "2026-01-01T00:00:01Z");
}

#[tokio::test]
async fn get_404_is_status_error_with_envelope_message() {
    let mut s = mockito::Server::new_async().await;
    let e = engine(&mut s).await;
    s.mock("GET", "/notes/x")
        .with_status(404)
        .with_body(r#"{"status":404,"code":"not_found","message":"not found","error":"not found"}"#)
        .create_async()
        .await;
    let err = e.collection::<Note>("notes").get("x").await.err().unwrap();
    match err {
        EngineError::Status {
            status: 404,
            message,
        } => assert_eq!(message, "not found"),
        o => panic!("{o:?}"),
    }
}

#[tokio::test]
async fn list_sends_query_params_only_when_set() {
    let mut s = mockito::Server::new_async().await;
    let e = engine(&mut s).await;
    let all = s
        .mock("GET", "/notes/")
        .match_query(Matcher::Missing)
        .with_body(format!("[{DOC}]"))
        .create_async()
        .await;
    let q = s
        .mock("GET", "/notes/")
        .match_query(Matcher::AllOf(vec![
            Matcher::UrlEncoded("limit".into(), "10".into()),
            Matcher::UrlEncoded("offset".into(), "5".into()),
            Matcher::UrlEncoded("sort".into(), "updated_at".into()),
            Matcher::UrlEncoded("search".into(), "a&b".into()),
        ]))
        .with_body("[]")
        .create_async()
        .await;
    let c = e.collection::<Note>("notes");
    assert_eq!(c.list(None).await.unwrap().len(), 1);
    all.assert_async().await;
    let r = c
        .list(Some(CollectionQuery {
            limit: Some(10),
            offset: Some(5),
            sort: Some("updated_at".into()),
            search: Some("a&b".into()),
        }))
        .await
        .unwrap();
    assert!(r.is_empty());
    q.assert_async().await;
}

#[tokio::test]
async fn update_puts_patch_body() {
    let mut s = mockito::Server::new_async().await;
    let e = engine(&mut s).await;
    let m = s
        .mock("PUT", "/notes/a")
        .match_header("authorization", "Bearer tok")
        .match_body(Matcher::Json(json!({"title":"New"})))
        .with_body(DOC)
        .create_async()
        .await;
    e.collection::<Note>("notes")
        .update("a", json!({"title":"New"}))
        .await
        .unwrap();
    m.assert_async().await;
}

#[tokio::test]
async fn delete_expects_204() {
    let mut s = mockito::Server::new_async().await;
    let e = engine(&mut s).await;
    let m = s
        .mock("DELETE", "/notes/a")
        .match_header("authorization", "Bearer tok")
        .with_status(204)
        .create_async()
        .await;
    e.collection::<Note>("notes").delete("a").await.unwrap();
    m.assert_async().await;
}

#[tokio::test]
async fn history_unwraps_versions() {
    let mut s = mockito::Server::new_async().await;
    let e = engine(&mut s).await;
    let m = s
        .mock("GET", "/notes/a/history")
        .match_query(Matcher::Missing)
        .with_body(r#"{"versions":[{"version":2,"data":{"title":"B"},"timestamp":"t2","operation":"update"},{"version":1,"data":{"title":"A"},"timestamp":"t1","operation":"create"}]}"#)
        .create_async()
        .await;
    let v = e.collection::<Note>("notes").history("a").await.unwrap();
    m.assert_async().await;
    assert_eq!(v.len(), 2);
    assert_eq!((v[0].version, v[0].operation.as_str()), (2, "update"));
    assert_eq!(v[1].data, json!({"title":"A"}));
}

#[tokio::test]
async fn history_with_topology_sets_flag_and_returns_dag() {
    let mut s = mockito::Server::new_async().await;
    let e = engine(&mut s).await;
    let m = s
        .mock("GET", "/notes/a/history")
        .match_query(Matcher::UrlEncoded("topology".into(), "1".into()))
        .with_body(r#"{"heads":["v2"],"versions":[{"version_id":"v2","seq":2,"parent_ids":["v1"],"timestamp":"t"},{"version_id":"v1","seq":1,"parent_ids":[],"timestamp":"t"}]}"#)
        .create_async()
        .await;
    let t = e
        .collection::<Note>("notes")
        .history_with_topology("a")
        .await
        .unwrap();
    m.assert_async().await;
    assert_eq!(t.heads, vec!["v2"]);
    assert_eq!(t.versions[0].parent_ids, vec!["v1"]);
    assert_eq!(t.versions[1].seq, 1);
}

#[tokio::test]
async fn revert_posts_version_and_returns_document() {
    let mut s = mockito::Server::new_async().await;
    let e = engine(&mut s).await;
    let m = s
        .mock("POST", "/notes/a/revert")
        .match_header("authorization", "Bearer tok")
        .match_body(Matcher::Json(json!({"version":2})))
        .with_body(DOC)
        .create_async()
        .await;
    let r = e.collection::<Note>("notes").revert("a", 2).await.unwrap();
    m.assert_async().await;
    assert_eq!(r.data.title, "Hi");
}

#[tokio::test]
async fn revert_409_is_version_conflict() {
    let mut s = mockito::Server::new_async().await;
    let e = engine(&mut s).await;
    s.mock("POST", "/notes/a/revert")
        .with_status(409)
        .with_body(r#"{"status":409,"code":"conflict","message":"no such version","error":"no such version"}"#)
        .create_async()
        .await;
    let err = e
        .collection::<Note>("notes")
        .revert("a", 9)
        .await
        .err()
        .unwrap();
    match err {
        EngineError::VersionConflict(m) => assert_eq!(m, "no such version"),
        o => panic!("{o:?}"),
    }
}

#[tokio::test]
async fn branches_live_flag_and_heads_unwrap() {
    let mut s = mockito::Server::new_async().await;
    let e = engine(&mut s).await;
    let body = r#"{"heads":[{"version_id":"v2","seq":2,"parent_ids":["v1"],"timestamp":"t","live":true}]}"#;
    let all = s
        .mock("GET", "/notes/a/branches")
        .match_query(Matcher::Missing)
        .with_body(body)
        .create_async()
        .await;
    let live = s
        .mock("GET", "/notes/a/branches")
        .match_query(Matcher::UrlEncoded("live".into(), "1".into()))
        .with_body(body)
        .create_async()
        .await;
    let c = e.collection::<Note>("notes");
    let h = c.branches("a", false).await.unwrap();
    all.assert_async().await;
    assert_eq!(h[0].version_id, "v2");
    c.branches("a", true).await.unwrap();
    live.assert_async().await;
}
