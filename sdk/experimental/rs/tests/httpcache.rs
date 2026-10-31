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

/// Fetch closure that echoes the named request header as the body and
/// stamps the given response headers; counts calls in `hits`.
fn echo<'a>(
    hits: &'a Cell<u32>,
    hdr: &'a str,
    resp_headers: &'a [(&'a str, &'a str)],
) -> impl FnMut(&Request) -> Result<Response, Infallible> + 'a {
    move |req: &Request| {
        hits.set(hits.get() + 1);
        let mut resp = Response::new(
            200,
            format!("{hdr}={}", req.headers.get(hdr).unwrap_or_default()),
        );
        for (k, v) in resp_headers {
            resp.headers.append(k, *v);
        }
        Ok(resp)
    }
}

fn get_with(
    cache: &Cache<'_, SqliteStore>,
    hdrs: &[(&str, &str)],
    f: impl FnMut(&Request) -> Result<Response, Infallible>,
) -> String {
    let mut req = Request::get("https://example.com/a").expect("request");
    for (k, v) in hdrs {
        req.headers.set(k, *v);
    }
    cache.fetch(&req, f).expect("fetch").body_string()
}

#[test]
fn vary_separates_variants() {
    let (_dir, store) = new_store();
    let cache = Cache::new(&store);
    let hits = Cell::new(0);
    let vary = [("Vary", "Accept-Language")];
    let fr = [("Accept-Language", "fr")];
    let en = [("Accept-Language", "en")];

    assert_eq!(
        get_with(&cache, &fr, echo(&hits, "Accept-Language", &vary)),
        "Accept-Language=fr"
    );
    assert_eq!(
        get_with(&cache, &en, echo(&hits, "Accept-Language", &vary)),
        "Accept-Language=en"
    );
    assert_eq!(
        hits.get(),
        2,
        "different Vary value must not get the fr response"
    );
    assert_eq!(
        get_with(&cache, &fr, echo(&hits, "Accept-Language", &vary)),
        "Accept-Language=fr"
    );
    assert_eq!(
        get_with(&cache, &en, echo(&hits, "Accept-Language", &vary)),
        "Accept-Language=en"
    );
    assert_eq!(hits.get(), 2, "repeats served from their own variant");
}

#[test]
fn vary_missing_header_is_its_own_variant() {
    let (_dir, store) = new_store();
    let cache = Cache::new(&store);
    let hits = Cell::new(0);
    let vary = [("Vary", "accept-language")];
    let fr = [("Accept-Language", "fr")];

    assert_eq!(
        get_with(&cache, &[], echo(&hits, "Accept-Language", &vary)),
        "Accept-Language="
    );
    assert_eq!(
        get_with(&cache, &fr, echo(&hits, "Accept-Language", &vary)),
        "Accept-Language=fr"
    );
    assert_eq!(
        get_with(&cache, &[], echo(&hits, "Accept-Language", &vary)),
        "Accept-Language="
    );
    assert_eq!(hits.get(), 2);
}

#[test]
fn vary_star_never_stored() {
    let (_dir, store) = new_store();
    let cache = Cache::new(&store);
    let hits = Cell::new(0);
    let vary = [("Vary", "*")];
    get_with(&cache, &[], echo(&hits, "X", &vary));
    get_with(&cache, &[], echo(&hits, "X", &vary));
    assert_eq!(hits.get(), 2, "Vary: * must not be cached");
}

#[test]
fn vary_multiple_header_lines() {
    let (_dir, store) = new_store();
    let cache = Cache::new(&store);
    let hits = Cell::new(0);
    let vary = [("Vary", "Accept"), ("Vary", "Accept-Language")];
    let a = [("Accept", "text/html"), ("Accept-Language", "en")];
    let b = [("Accept", "text/html"), ("Accept-Language", "fr")];
    get_with(&cache, &a, echo(&hits, "Accept", &vary));
    get_with(&cache, &b, echo(&hits, "Accept", &vary));
    get_with(&cache, &a, echo(&hits, "Accept", &vary));
    assert_eq!(hits.get(), 2);
}

#[test]
fn no_vary_hits_across_headers() {
    let (_dir, store) = new_store();
    let cache = Cache::new(&store);
    let hits = Cell::new(0);
    get_with(
        &cache,
        &[("Accept-Language", "fr")],
        echo(&hits, "Accept-Language", &[]),
    );
    get_with(
        &cache,
        &[("Accept-Language", "en")],
        echo(&hits, "Accept-Language", &[]),
    );
    assert_eq!(hits.get(), 1, "no Vary: key stays method+URL");
}

#[test]
fn vary_less_save_clears_stale_index() {
    let (_dir, store) = new_store();
    let cache = Cache::new(&store);
    let hits = Cell::new(0);
    let x = |v: &'static str| [("X", v)];
    // Vary response records an index for the URL.
    get_with(&cache, &x("a"), echo(&hits, "X", &[("Vary", "X")]));
    // A Vary-less response for another variant lands on the primary key
    // and must drop the index.
    get_with(&cache, &x("b"), echo(&hits, "X", &[]));
    // With the index gone, any request is served from the primary entry.
    assert_eq!(get_with(&cache, &x("c"), echo(&hits, "X", &[])), "X=b");
    assert_eq!(hits.get(), 2);
}

#[test]
fn authorization_not_stored_by_default() {
    let (_dir, store) = new_store();
    let cache = Cache::new(&store);
    let hits = Cell::new(0);
    let alice = [("Authorization", "Bearer alice")];
    let bob = [("Authorization", "Bearer bob")];
    assert_eq!(
        get_with(&cache, &alice, echo(&hits, "Authorization", &[])),
        "Authorization=Bearer alice"
    );
    assert_eq!(
        get_with(&cache, &bob, echo(&hits, "Authorization", &[])),
        "Authorization=Bearer bob"
    );
    assert_eq!(
        get_with(&cache, &alice, echo(&hits, "Authorization", &[])),
        "Authorization=Bearer alice"
    );
    assert_eq!(hits.get(), 3);
}

#[test]
fn unauthenticated_never_sees_authenticated_response() {
    let (_dir, store) = new_store();
    let cache = Cache::new(&store);
    let hits = Cell::new(0);
    let cc = [("Cache-Control", "public")];
    get_with(
        &cache,
        &[("Authorization", "Bearer alice")],
        echo(&hits, "Authorization", &[]),
    );
    assert_eq!(
        get_with(&cache, &[], echo(&hits, "Authorization", &cc)),
        "Authorization="
    );
}

#[test]
fn authorization_with_public_is_stored() {
    let (_dir, store) = new_store();
    let cache = Cache::new(&store);
    let hits = Cell::new(0);
    let cc = [("Cache-Control", "public, max-age=60")];
    let alice = [("Authorization", "Bearer alice")];
    get_with(&cache, &alice, echo(&hits, "Authorization", &cc));
    get_with(&cache, &alice, echo(&hits, "Authorization", &cc));
    assert_eq!(hits.get(), 1);
}

#[test]
fn private_never_stored_even_with_exemption() {
    let (_dir, store) = new_store();
    let cache = Cache::new(&store);
    let hits = Cell::new(0);
    let cc = [("Cache-Control", "private, must-revalidate")];
    let alice = [("Authorization", "Bearer alice")];
    get_with(&cache, &alice, echo(&hits, "Authorization", &cc));
    get_with(&cache, &alice, echo(&hits, "Authorization", &cc));
    get_with(
        &cache,
        &[],
        echo(&hits, "Authorization", &[("Cache-Control", "private")]),
    );
    get_with(
        &cache,
        &[],
        echo(&hits, "Authorization", &[("Cache-Control", "private")]),
    );
    assert_eq!(hits.get(), 4);
}

#[test]
fn primary_entry_naming_vary_is_a_miss() {
    use hop_top_kit::kv::Store as _;
    let (_dir, store) = new_store();
    let cache = Cache::new(&store);
    let hits = Cell::new(0);
    let req = Request::get("https://example.com/a").expect("request");
    // A Vary-unaware writer sharing the store left a Vary response at the
    // primary key; serving it would ignore the request headers.
    store
        .put(
            cache.key(&req).as_bytes(),
            br#"{"status":200,"headers":{"Vary":["X"]},"body":"aGk="}"#,
        )
        .expect("seed");
    cache.fetch(&req, echo(&hits, "X", &[])).expect("fetch");
    assert_eq!(hits.get(), 1);
}
