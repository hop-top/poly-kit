//! Behavioural tests for the httpcache port.
//!
//! Ports `go/storage/httpcache/httpcache_test.go`. Where the Go suite
//! stands up an `httptest` server and counts origin hits, this port
//! counts calls to the fetch closure — the same assertion without an HTTP
//! stack. The wire-format tests live in `httpcache_contract.rs`.

#![cfg(feature = "httpcache")]

use std::cell::Cell;
use std::convert::Infallible;
use std::time::Duration;

use hop_top_kit::httpcache::{Cache, Config, Request, Response};
use hop_top_kit::kv::{Config as KvConfig, SqliteStore};

/// Opens a SQLite-backed store in a temp dir.
///
/// The tempdir is returned alongside the store: dropping it removes the
/// database, so it must outlive the store. A file path is used rather
/// than `:memory:` — a shared-cache memory DSN silently creates a real
/// file unless opened with URI flags.
fn new_store() -> (tempfile::TempDir, SqliteStore) {
    let dir = tempfile::tempdir().expect("tempdir");
    let store = KvConfig::sqlite(dir.path().join("cache.db").to_str().expect("utf-8 path"))
        .open()
        .expect("open store");
    (dir, store)
}

/// A fetch closure that counts invocations and returns a fixed response.
struct Origin {
    calls: Cell<u32>,
    response: Response,
}

impl Origin {
    fn new(response: Response) -> Self {
        Self {
            calls: Cell::new(0),
            response,
        }
    }

    fn fetch(&self, _: &Request) -> Result<Response, Infallible> {
        self.calls.set(self.calls.get() + 1);
        Ok(self.response.clone())
    }

    fn calls(&self) -> u32 {
        self.calls.get()
    }
}

#[test]
fn caches_get() {
    let (_dir, store) = new_store();
    let cache = Cache::new(&store);
    let req = Request::get("https://example.com/a").expect("request");
    let origin = Origin::new(Response::new(200, "hello"));

    let first = cache.fetch(&req, |r| origin.fetch(r)).expect("first");
    assert_eq!(first.body_string(), "hello");
    assert_eq!(origin.calls(), 1);

    let second = cache.fetch(&req, |r| origin.fetch(r)).expect("second");
    assert_eq!(second.body_string(), "hello");
    assert_eq!(origin.calls(), 1, "cache hit must not reach the origin");
}

#[test]
fn does_not_cache_post() {
    let (_dir, store) = new_store();
    let cache = Cache::new(&store);
    let req = Request::new("POST", "https://example.com/a").expect("request");
    let origin = Origin::new(Response::new(200, "ok"));

    cache.fetch(&req, |r| origin.fetch(r)).expect("first");
    cache.fetch(&req, |r| origin.fetch(r)).expect("second");
    assert_eq!(origin.calls(), 2, "POST must never be served from cache");
}

#[test]
fn does_not_cache_non_2xx() {
    let (_dir, store) = new_store();
    let cache = Cache::new(&store);
    let req = Request::get("https://example.com/a").expect("request");
    let origin = Origin::new(Response::new(404, "nope"));

    cache.fetch(&req, |r| origin.fetch(r)).expect("first");
    cache.fetch(&req, |r| origin.fetch(r)).expect("second");
    assert_eq!(origin.calls(), 2, "404 must not be cached");
}

#[test]
fn respects_response_no_store() {
    let (_dir, store) = new_store();
    let cache = Cache::new(&store);
    let req = Request::get("https://example.com/a").expect("request");
    let origin = Origin::new(Response::new(200, "secret").with_header("Cache-Control", "no-store"));

    cache.fetch(&req, |r| origin.fetch(r)).expect("first");
    cache.fetch(&req, |r| origin.fetch(r)).expect("second");
    assert_eq!(origin.calls(), 2, "no-store response must not be cached");
}

#[test]
fn respects_request_no_store() {
    let (_dir, store) = new_store();
    let cache = Cache::new(&store);
    let req = Request::get("https://example.com/a")
        .expect("request")
        .with_header("Cache-Control", "no-store");
    let origin = Origin::new(Response::new(200, "v"));

    cache.fetch(&req, |r| origin.fetch(r)).expect("first");
    cache.fetch(&req, |r| origin.fetch(r)).expect("second");
    assert_eq!(origin.calls(), 2, "no-store request must not be cached");
}

#[test]
fn ttl_expiry_refetches() {
    let (_dir, store) = new_store();
    let cache = Cache::with_config(
        &store,
        Config::default().with_ttl(Duration::from_millis(50)),
    );
    let req = Request::get("https://example.com/a").expect("request");
    let origin = Origin::new(Response::new(200, "v"));

    cache.fetch(&req, |r| origin.fetch(r)).expect("first");
    assert_eq!(origin.calls(), 1);

    std::thread::sleep(Duration::from_millis(80));
    cache.fetch(&req, |r| origin.fetch(r)).expect("second");
    assert_eq!(origin.calls(), 2, "expired entry must refetch");
}

#[test]
fn zero_ttl_caches_without_expiry() {
    let (_dir, store) = new_store();
    // A zero TTL must store with NO expiry, not stamp an already-past
    // one — which would make every entry a perpetual miss.
    let cache = Cache::with_config(&store, Config::default().with_ttl(Duration::ZERO));
    let req = Request::get("https://example.com/a").expect("request");
    let origin = Origin::new(Response::new(200, "v"));

    cache.fetch(&req, |r| origin.fetch(r)).expect("first");
    cache.fetch(&req, |r| origin.fetch(r)).expect("second");
    assert_eq!(
        origin.calls(),
        1,
        "zero TTL must cache, not expire instantly"
    );
}

#[test]
fn preserves_headers_and_status() {
    let (_dir, store) = new_store();
    let cache = Cache::new(&store);
    let req = Request::get("https://example.com/a").expect("request");
    let origin = Origin::new(Response::new(200, "body").with_header("X-Custom", "abc"));

    cache.fetch(&req, |r| origin.fetch(r)).expect("prime");

    let cached = cache.fetch(&req, |r| origin.fetch(r)).expect("from cache");
    assert_eq!(origin.calls(), 1, "served from cache");
    assert_eq!(cached.status, 200);
    assert_eq!(cached.headers.get("X-Custom"), Some("abc"));
    assert_eq!(cached.body_string(), "body");
}

#[test]
fn strips_framing_headers() {
    let (_dir, store) = new_store();
    let cache = Cache::new(&store);
    let req = Request::get("https://example.com/a").expect("request");
    let origin = Origin::new(
        Response::new(200, "chunked-body")
            .with_header("X-Keep", "yes")
            .with_header("Transfer-Encoding", "chunked")
            .with_header("Content-Length", "999")
            .with_header("Connection", "keep-alive"),
    );

    cache.fetch(&req, |r| origin.fetch(r)).expect("prime");
    let cached = cache.fetch(&req, |r| origin.fetch(r)).expect("from cache");

    assert_eq!(cached.body_string(), "chunked-body");
    assert_eq!(
        cached.headers.get("X-Keep"),
        Some("yes"),
        "non-framing headers preserved"
    );
    assert_eq!(cached.headers.get("Transfer-Encoding"), None);
    assert_eq!(cached.headers.get("Connection"), None);
    assert_eq!(
        cached.headers.get("Content-Length"),
        None,
        "the recomputed body length is authoritative"
    );
    assert_eq!(cached.content_length, "chunked-body".len() as u64);
}

#[test]
fn distinct_urls_do_not_collide() {
    let (_dir, store) = new_store();
    let cache = Cache::new(&store);
    let a = Request::get("https://example.com/a").expect("request");
    let b = Request::get("https://example.com/b").expect("request");

    cache
        .fetch(&a, |_| Ok::<_, Infallible>(Response::new(200, "A")))
        .expect("a");
    cache
        .fetch(&b, |_| Ok::<_, Infallible>(Response::new(200, "B")))
        .expect("b");

    let hit_a = cache
        .fetch(&a, |_| Ok::<_, Infallible>(Response::new(200, "wrong")))
        .expect("a again");
    let hit_b = cache
        .fetch(&b, |_| Ok::<_, Infallible>(Response::new(200, "wrong")))
        .expect("b again");
    assert_eq!(hit_a.body_string(), "A");
    assert_eq!(hit_b.body_string(), "B");
}

#[test]
fn prefix_namespaces_entries() {
    let (_dir, store) = new_store();
    let req = Request::get("https://example.com/a").expect("request");

    let one = Cache::with_config(&store, Config::default().with_prefix("one:"));
    let two = Cache::with_config(&store, Config::default().with_prefix("two:"));

    one.fetch(&req, |_| Ok::<_, Infallible>(Response::new(200, "one")))
        .expect("one");

    // A different prefix is a different key, so this must miss.
    let origin = Origin::new(Response::new(200, "two"));
    let got = two.fetch(&req, |r| origin.fetch(r)).expect("two");
    assert_eq!(origin.calls(), 1, "a distinct prefix must not hit");
    assert_eq!(got.body_string(), "two");
}

#[test]
fn binary_body_round_trips() {
    let (_dir, store) = new_store();
    let cache = Cache::new(&store);
    let req = Request::get("https://example.com/bin").expect("request");
    let bytes: Vec<u8> = vec![0x00, 0xff, 0xfe, 0x01, 0x02, 0x80];
    let origin = Origin::new(Response::new(200, bytes.clone()));

    cache.fetch(&req, |r| origin.fetch(r)).expect("prime");
    let cached = cache.fetch(&req, |r| origin.fetch(r)).expect("from cache");

    assert_eq!(origin.calls(), 1);
    assert_eq!(cached.body, bytes, "arbitrary bytes survive the envelope");
    assert_eq!(cached.content_length, bytes.len() as u64);
}

#[test]
fn multi_value_headers_round_trip() {
    let (_dir, store) = new_store();
    let cache = Cache::new(&store);
    let req = Request::get("https://example.com/a").expect("request");
    let origin = Origin::new(
        Response::new(200, "x")
            .with_header("Set-Cookie", "a=1")
            .with_header("Set-Cookie", "b=2"),
    );

    cache.fetch(&req, |r| origin.fetch(r)).expect("prime");
    let cached = cache.fetch(&req, |r| origin.fetch(r)).expect("from cache");

    assert_eq!(
        cached.headers.values("Set-Cookie"),
        ["a=1", "b=2"],
        "duplicate header values and their order survive"
    );
}

#[test]
fn fetch_error_propagates_and_stores_nothing() {
    let (_dir, store) = new_store();
    let cache = Cache::new(&store);
    let req = Request::get("https://example.com/a").expect("request");

    let err = cache
        .fetch(&req, |_| Err::<Response, _>("boom"))
        .expect_err("fetch error must propagate");
    assert_eq!(err, "boom");

    // Nothing was cached, so a subsequent fetch still reaches the origin.
    let origin = Origin::new(Response::new(200, "v"));
    cache.fetch(&req, |r| origin.fetch(r)).expect("retry");
    assert_eq!(origin.calls(), 1);
}

#[test]
fn corrupt_entry_degrades_to_a_miss() {
    let (_dir, store) = new_store();
    let cache = Cache::new(&store);
    let req = Request::get("https://example.com/a").expect("request");

    // A malformed envelope must never surface as a caller-visible error.
    hop_top_kit::kv::Store::put(&store, cache.key(&req).as_bytes(), b"{not json")
        .expect("seed corrupt entry");

    let origin = Origin::new(Response::new(200, "fresh"));
    let got = cache
        .fetch(&req, |r| origin.fetch(r))
        .expect("must not error");
    assert_eq!(origin.calls(), 1, "corrupt entry refetches");
    assert_eq!(got.body_string(), "fresh");
}

#[test]
fn method_case_is_significant() {
    let (_dir, store) = new_store();
    let cache = Cache::new(&store);
    // "get" is not "GET": it is neither cacheable nor the same key.
    let upper = Request::new("GET", "https://example.com/a").expect("request");
    let lower = Request::new("get", "https://example.com/a").expect("request");

    assert_ne!(cache.key(&upper), cache.key(&lower));

    let origin = Origin::new(Response::new(200, "v"));
    cache.fetch(&lower, |r| origin.fetch(r)).expect("first");
    cache.fetch(&lower, |r| origin.fetch(r)).expect("second");
    assert_eq!(origin.calls(), 2, "only GET is cacheable");
}
