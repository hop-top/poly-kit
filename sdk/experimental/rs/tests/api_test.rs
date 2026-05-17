// This integration test exercises the optional `api` module. Cargo
// compiles every file under tests/ unconditionally, so the test must
// gate its entire body on the `api` feature; otherwise the unresolved
// imports break `cargo test` under default features.
#![cfg(feature = "api")]

use hop_top_kit::api::{ApiClient, Query};
use mockito::{Matcher, Server};
use serde::{Deserialize, Serialize};

#[derive(Debug, Clone, Serialize, Deserialize, PartialEq)]
struct Item {
    id: String,
    name: String,
}

#[tokio::test]
async fn create_sends_post_and_returns_entity() {
    let mut server = Server::new_async().await;
    let mock = server
        .mock("POST", "/")
        .match_header("content-type", "application/json")
        .match_body(Matcher::Json(serde_json::json!({"id": "1", "name": "foo"})))
        .with_status(201)
        .with_header("content-type", "application/json")
        .with_body(r#"{"id":"1","name":"foo"}"#)
        .create_async()
        .await;

    let client = ApiClient::new(&server.url());
    let item = Item {
        id: "1".into(),
        name: "foo".into(),
    };
    let result: Item = client.create(&item).await.unwrap();

    assert_eq!(result, item);
    mock.assert_async().await;
}

#[tokio::test]
async fn get_sends_get_with_id() {
    let mut server = Server::new_async().await;
    let mock = server
        .mock("GET", "/42")
        .with_status(200)
        .with_header("content-type", "application/json")
        .with_body(r#"{"id":"42","name":"bar"}"#)
        .create_async()
        .await;

    let client = ApiClient::new(&server.url());
    let result: Item = client.get("42").await.unwrap();

    assert_eq!(
        result,
        Item {
            id: "42".into(),
            name: "bar".into()
        }
    );
    mock.assert_async().await;
}

#[tokio::test]
async fn list_sends_query_params() {
    let mut server = Server::new_async().await;
    let mock = server
        .mock("GET", "/")
        .match_query(Matcher::AllOf(vec![
            Matcher::UrlEncoded("limit".into(), "10".into()),
            Matcher::UrlEncoded("offset".into(), "5".into()),
            Matcher::UrlEncoded("sort".into(), "name".into()),
        ]))
        .with_status(200)
        .with_header("content-type", "application/json")
        .with_body(r#"[{"id":"1","name":"a"},{"id":"2","name":"b"}]"#)
        .create_async()
        .await;

    let client = ApiClient::new(&server.url());
    let q = Query {
        limit: Some(10),
        offset: Some(5),
        sort: Some("name".into()),
        search: None,
    };
    let result: Vec<Item> = client.list(&q).await.unwrap();

    assert_eq!(result.len(), 2);
    mock.assert_async().await;
}

#[tokio::test]
async fn delete_returns_ok_on_204() {
    let mut server = Server::new_async().await;
    let mock = server
        .mock("DELETE", "/99")
        .with_status(204)
        .create_async()
        .await;

    let client = ApiClient::new(&server.url());
    client.delete("99").await.unwrap();

    mock.assert_async().await;
}

#[tokio::test]
async fn non_2xx_returns_api_error() {
    let mut server = Server::new_async().await;
    let mock = server
        .mock("GET", "/missing")
        .with_status(404)
        .with_header("content-type", "application/json")
        .with_body(r#"{"status":404,"code":"not_found","message":"not found"}"#)
        .create_async()
        .await;

    let client = ApiClient::new(&server.url());
    let err = client.get::<Item>("missing").await.unwrap_err();

    assert_eq!(err.status, 404);
    assert_eq!(err.code, "not_found");
    assert_eq!(err.message, "not found");
    mock.assert_async().await;
}

#[tokio::test]
async fn non_2xx_without_json_body_constructs_fallback_error() {
    let mut server = Server::new_async().await;
    let mock = server
        .mock("GET", "/bad")
        .with_status(500)
        .with_body("internal server error")
        .create_async()
        .await;

    let client = ApiClient::new(&server.url());
    let err = client.get::<Item>("bad").await.unwrap_err();

    assert_eq!(err.status, 500);
    assert_eq!(err.code, "unknown");
    mock.assert_async().await;
}

#[tokio::test]
async fn auth_header_is_set() {
    let mut server = Server::new_async().await;
    let mock = server
        .mock("GET", "/secure")
        .match_header("authorization", "Bearer my-token")
        .with_status(200)
        .with_header("content-type", "application/json")
        .with_body(r#"{"id":"s","name":"secure"}"#)
        .create_async()
        .await;

    let client = ApiClient::new(&server.url()).with_auth("my-token");
    let _: Item = client.get("secure").await.unwrap();

    mock.assert_async().await;
}
