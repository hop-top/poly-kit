//! Component tags and prereleases: `tag_prefix` and `include_prereleases`
//! list releases instead of reading `releases/latest` (sdk/ts `upgrade.ts`).

#![cfg(all(feature = "upgrade", not(target_arch = "wasm32")))]

use std::cell::RefCell;

use hop_top_kit::upgrade::check::{CheckConfig, CheckResult, Checker};
use serde_json::{json, Value};

const LIST: &str = "https://api.github.com/repos/acme/mono/releases";

fn rel(tag: &str) -> Value {
    json!({ "tag_name": tag })
}

fn pre(tag: &str) -> Value {
    json!({ "tag_name": tag, "prerelease": true })
}

fn filler(n: usize, count: usize) -> Vec<Value> {
    (0..count)
        .map(|i| rel(&format!("kit/v1.{n}.{i}")))
        .collect()
}

/// Serves `pages[n-1]` for `?page=n` on the list URL (404 elsewhere) and
/// records every requested URL.
struct Server {
    pages: Vec<Value>,
    urls: RefCell<Vec<String>>,
}

impl Server {
    fn new(pages: Vec<Vec<Value>>) -> Server {
        Server {
            pages: pages.into_iter().map(Value::Array).collect(),
            urls: RefCell::new(vec![]),
        }
    }

    fn serve(&self, url: &str) -> Result<String, hop_top_kit::upgrade::BoxError> {
        self.urls.borrow_mut().push(url.to_string());
        let (base, query) = url.split_once('?').unwrap_or((url, ""));
        if base != LIST {
            return Err("404".into());
        }
        let page: usize = query
            .split('&')
            .find_map(|kv| kv.strip_prefix("page="))
            .and_then(|p| p.parse().ok())
            .unwrap_or(1);
        Ok(self
            .pages
            .get(page - 1)
            .cloned()
            .unwrap_or(json!([]))
            .to_string())
    }

    fn calls(&self) -> usize {
        self.urls.borrow().len()
    }
}

fn run(
    dir: &std::path::Path,
    current: &str,
    srv: &Server,
    f: impl Fn(CheckConfig) -> CheckConfig,
) -> CheckResult {
    let cfg = f(CheckConfig::new("xray", current)
        .github("acme/mono")
        .state_dir(dir));
    Checker::new(cfg).check(|u| srv.serve(u))
}

fn notice(
    dir: &std::path::Path,
    current: &str,
    srv: &Server,
    f: impl Fn(CheckConfig) -> CheckConfig,
) -> Option<String> {
    let cfg = f(CheckConfig::new("xray", current)
        .github("acme/mono")
        .state_dir(dir));
    let c = Checker::new(cfg);
    let r = c.check(|u| srv.serve(u));
    c.notice(&r)
}

fn xray(c: CheckConfig) -> CheckConfig {
    c.tag_prefix("xray/v")
}

fn xray_pre(c: CheckConfig) -> CheckConfig {
    c.tag_prefix("xray/v").include_prereleases(true)
}

#[test]
fn prefixed_tag_notifies_and_links_the_release() {
    let t = tempfile::tempdir().unwrap();
    let srv = Server::new(vec![vec![rel("xray/v0.2.0")]]);
    let n = notice(t.path(), "0.1.0", &srv, xray).unwrap();
    assert_eq!(
        n,
        "\nA new release of xray is available: v0.1.0 → v0.2.0\n\
         https://github.com/acme/mono/releases/tag/xray/v0.2.0\n"
    );
}

#[test]
fn other_components_never_notify() {
    let t = tempfile::tempdir().unwrap();
    let srv = Server::new(vec![vec![rel("kit/v9.0.0"), rel("xray/v0.2.0")]]);
    assert_eq!(run(t.path(), "0.1.0", &srv, xray).latest, "0.2.0");
    let t = tempfile::tempdir().unwrap();
    let srv = Server::new(vec![vec![rel("kit/v9.0.0"), rel("xray-cli/v9.0.0")]]);
    let r = run(t.path(), "0.1.0", &srv, xray);
    assert!(!r.update_avail && r.latest.is_empty(), "{r:?}");
}

#[test]
fn highest_semver_wins_and_drafts_are_skipped() {
    let t = tempfile::tempdir().unwrap();
    let srv = Server::new(vec![vec![
        rel("xray/v0.2.1"),
        rel("xray/v0.10.0"),
        rel("xray/v0.9.0"),
        json!({"tag_name": "xray/v0.99.0", "draft": true}),
    ]]);
    assert_eq!(run(t.path(), "0.1.0", &srv, xray).latest, "0.10.0");
}

#[test]
fn a_stable_user_is_not_nudged_onto_a_prerelease() {
    let t = tempfile::tempdir().unwrap();
    let srv = Server::new(vec![vec![
        pre("xray/v0.3.0-rc.1"),
        rel("xray/v0.3.0-rc.0"), // not flagged by GitHub; semver says prerelease
    ]]);
    let r = run(t.path(), "0.2.0", &srv, xray);
    assert!(!r.update_avail, "{r:?}");
}

#[test]
fn prerelease_users_and_the_option() {
    let t = tempfile::tempdir().unwrap();
    let srv = Server::new(vec![vec![pre("xray/v0.3.0-rc.1")]]);
    let r = run(t.path(), "0.2.0", &srv, xray_pre);
    assert!(r.update_avail && r.latest == "0.3.0-rc.1", "{r:?}");

    let srv = Server::new(vec![vec![pre("xray/v0.1.0-rc.1"), pre("xray/v0.1.0-rc.0")]]);
    let t = tempfile::tempdir().unwrap();
    let r = run(t.path(), "0.1.0-rc.0", &srv, xray_pre);
    assert!(r.update_avail && r.latest == "0.1.0-rc.1", "{r:?}");
    let t = tempfile::tempdir().unwrap();
    assert!(!run(t.path(), "0.1.0-rc.1", &srv, xray_pre).update_avail);

    let srv = Server::new(vec![vec![pre("xray/v0.1.0-rc.1"), rel("xray/v0.1.0")]]);
    for f in [xray_pre as fn(CheckConfig) -> CheckConfig, xray] {
        let t = tempfile::tempdir().unwrap();
        let r = run(t.path(), "0.1.0-rc.0", &srv, f);
        assert!(r.update_avail && r.latest == "0.1.0", "{r:?}");
    }
}

#[test]
fn prerelease_ordering_matches_semver() {
    let t = tempfile::tempdir().unwrap();
    let srv = Server::new(vec![vec![
        pre("xray/v1.0.0-alpha.2"),
        pre("xray/v1.0.0-alpha.10"),
        pre("xray/v1.0.0-alpha.beta"),
        pre("xray/v1.0.0-alpha"),
        pre("xray/v1.0.0-beta"),
        pre("xray/v1.0.0-rc.1"),
    ]]);
    assert_eq!(run(t.path(), "0.9.0", &srv, xray_pre).latest, "1.0.0-rc.1");
    let srv = Server::new(vec![vec![
        pre("xray/v1.0.0-alpha.2"),
        pre("xray/v1.0.0-alpha.10"),
    ]]);
    let t = tempfile::tempdir().unwrap();
    assert_eq!(
        run(t.path(), "0.9.0", &srv, xray_pre).latest,
        "1.0.0-alpha.10"
    );
}

#[test]
fn include_prereleases_alone_links_the_tag_as_published() {
    let t = tempfile::tempdir().unwrap();
    let srv = Server::new(vec![vec![pre("v1.0.0-rc.1"), rel("v0.9.0")]]);
    let n = notice(t.path(), "0.9.0", &srv, |c| c.include_prereleases(true)).unwrap();
    assert_eq!(
        n,
        "\nA new release of xray is available: v0.9.0 → v1.0.0-rc.1\n\
         https://github.com/acme/mono/releases/tag/v1.0.0-rc.1\n"
    );
}

#[test]
fn list_mode_never_reads_releases_latest() {
    let t = tempfile::tempdir().unwrap();
    let srv = Server::new(vec![vec![rel("xray/v0.2.0")]]);
    run(t.path(), "0.1.0", &srv, xray);
    assert!(srv
        .urls
        .borrow()
        .iter()
        .all(|u| !u.contains("/releases/latest")));
}

#[test]
fn pages_until_a_short_page_and_at_most_three() {
    let t = tempfile::tempdir().unwrap();
    let srv = Server::new(vec![filler(0, 100), vec![rel("xray/v0.2.0")]]);
    assert_eq!(run(t.path(), "0.1.0", &srv, xray).latest, "0.2.0");
    assert_eq!(srv.calls(), 2);

    let t = tempfile::tempdir().unwrap();
    let srv = Server::new(vec![
        filler(0, 100),
        filler(1, 100),
        filler(2, 100),
        vec![rel("xray/v0.2.0")],
    ]);
    assert!(!run(t.path(), "0.1.0", &srv, xray).update_avail);
    assert_eq!(srv.calls(), 3);
}

#[test]
fn a_later_page_failing_keeps_earlier_pages() {
    let t = tempfile::tempdir().unwrap();
    let full: Vec<Value> = (0..100).map(|i| rel(&format!("xray/v0.2.{i}"))).collect();
    let calls = RefCell::new(0);
    let cfg = xray(
        CheckConfig::new("xray", "0.1.0")
            .github("acme/mono")
            .state_dir(t.path()),
    );
    let r = Checker::new(cfg).check(|_| {
        *calls.borrow_mut() += 1;
        if *calls.borrow() == 1 {
            Ok(Value::Array(full.clone()).to_string())
        } else {
            Err("500".into())
        }
    });
    assert_eq!(r.latest, "0.2.99", "{r:?}");
}

#[test]
fn failures_are_embedded_not_raised() {
    let t = tempfile::tempdir().unwrap();
    let cfg = || {
        xray(
            CheckConfig::new("xray", "0.1.0")
                .github("acme/mono")
                .state_dir(t.path()),
        )
    };
    for body in ["{\"message\":\"nope\"}", "not json"] {
        let r = Checker::new(cfg()).check(|_| Ok(body.to_string()));
        assert!(r.error.is_some() && !r.update_avail, "{r:?}");
    }
    let r = Checker::new(cfg()).check(|_| Err("offline".into()));
    assert!(r.error.is_some() && !r.update_avail);
    let junk = r#"[null, {"tag_name": 7}, {"tag_name": "xray/vbogus"}, "x"]"#;
    let r = Checker::new(cfg()).check(|_| Ok(junk.to_string()));
    assert!(!r.update_avail, "{r:?}");
}

#[test]
fn the_cache_keeps_the_tag_link() {
    let t = tempfile::tempdir().unwrap();
    let srv = Server::new(vec![vec![rel("xray/v0.2.0")]]);
    run(t.path(), "0.1.0", &srv, xray);
    let n = notice(t.path(), "0.1.0", &srv, xray).unwrap();
    assert_eq!(srv.calls(), 1);
    assert!(n.contains("releases/tag/xray/v0.2.0"), "{n}");
}

fn write_cache(dir: &std::path::Path, tag: Option<Value>) {
    let mut rec = json!({
        "current": "1.0.0", "latest": "2.0.0", "url": "", "checksum_url": "", "notes": "",
        "checked_at": std::time::SystemTime::now()
            .duration_since(std::time::UNIX_EPOCH).unwrap().as_secs(),
        "update_avail": true,
    });
    if let Some(t) = tag {
        rec["tag"] = t;
    }
    std::fs::write(dir.join("cache.json"), rec.to_string()).unwrap();
}

#[test]
fn a_cached_tag_must_name_the_version() {
    let cases = [
        ("path traversal", Some(json!("xray/v../../../evil"))),
        ("another version", Some(json!("xray/v1.0.0"))),
        ("another component", Some(json!("kit/v2.0.0"))),
        ("not a string", Some(json!(42))),
        ("empty", Some(json!(""))),
        ("missing", None),
    ];
    for (label, tag) in cases {
        let t = tempfile::tempdir().unwrap();
        write_cache(t.path(), tag);
        let srv = Server::new(vec![vec![rel("xray/v1.5.0")]]);
        let n = notice(t.path(), "1.0.0", &srv, xray).unwrap_or_default();
        assert_eq!(srv.calls(), 1, "{label}: poisoned cache was served");
        assert!(
            n.contains("v1.0.0 → v1.5.0") && n.contains("releases/tag/xray/v1.5.0"),
            "{label}: {n}"
        );
        assert!(!n.contains("evil"), "{label}");
    }
}

#[test]
fn a_matching_cached_tag_is_served() {
    let t = tempfile::tempdir().unwrap();
    write_cache(t.path(), Some(json!("xray/v2.0.0")));
    let srv = Server::new(vec![]);
    let n = notice(t.path(), "1.0.0", &srv, xray).unwrap();
    assert_eq!(srv.calls(), 0);
    assert!(n.contains("releases/tag/xray/v2.0.0"));
}

#[test]
fn tag_segments_are_encoded() {
    let t = tempfile::tempdir().unwrap();
    let srv = Server::new(vec![vec![rel("my tool/v0.2.0")]]);
    let n = notice(t.path(), "0.1.0", &srv, |c| c.tag_prefix("my tool/v")).unwrap();
    assert!(n.contains("releases/tag/my%20tool/v0.2.0"), "{n}");
}

#[test]
fn unset_options_keep_the_old_path_and_cache_keys() {
    let t = tempfile::tempdir().unwrap();
    let cfg = CheckConfig::new("xray", "1.0.0")
        .github("acme/mono")
        .state_dir(t.path());
    let c = Checker::new(cfg);
    let urls = RefCell::new(vec![]);
    let r = c.check(|u| {
        urls.borrow_mut().push(u.to_string());
        Ok(r#"{"tag_name":"v2.0.0","assets":[]}"#.to_string())
    });
    assert_eq!(*urls.borrow(), [format!("{LIST}/latest")]);
    assert!(r.update_avail && r.tag.is_empty());
    assert_eq!(
        c.notice(&r).unwrap(),
        "\nA new release of xray is available: v1.0.0 → v2.0.0\n\
         https://github.com/acme/mono/releases/latest\n"
    );
    let raw: Value =
        serde_json::from_str(&std::fs::read_to_string(t.path().join("cache.json")).unwrap())
            .unwrap();
    let mut keys: Vec<&String> = raw.as_object().unwrap().keys().collect();
    keys.sort();
    assert_eq!(
        keys,
        [
            "checked_at",
            "checksum_url",
            "current",
            "latest",
            "notes",
            "update_avail",
            "url"
        ]
    );
}

#[test]
fn the_async_form_lists_too() {
    let t = tempfile::tempdir().unwrap();
    let srv = Server::new(vec![filler(0, 100), vec![rel("xray/v0.2.0")]]);
    let cfg = xray(
        CheckConfig::new("xray", "0.1.0")
            .github("acme/mono")
            .state_dir(t.path()),
    );
    let r = block_on(Checker::new(cfg).check_async(|u| {
        let body = srv.serve(&u);
        async move { body }
    }));
    assert_eq!(r.latest, "0.2.0", "{r:?}");
    assert_eq!(srv.calls(), 2);
}

/// Polls a future that never pends (no runtime in this feature set).
fn block_on<F: std::future::Future>(f: F) -> F::Output {
    use std::task::{Context, Poll, RawWaker, RawWakerVTable, Waker};
    fn noop(_: *const ()) {}
    fn clone(_: *const ()) -> RawWaker {
        RawWaker::new(std::ptr::null(), &VTABLE)
    }
    static VTABLE: RawWakerVTable = RawWakerVTable::new(clone, noop, noop, noop);
    let mut f = Box::pin(f);
    let waker = unsafe { Waker::from_raw(RawWaker::new(std::ptr::null(), &VTABLE)) };
    match f.as_mut().poll(&mut Context::from_waker(&waker)) {
        Poll::Ready(v) => v,
        Poll::Pending => panic!("future pended"),
    }
}
