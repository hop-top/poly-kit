//! The release check: a best-effort "is a newer build out?" with a
//! cache, and `--offline` switching it off entirely (Go
//! `core/upgrade/check.go`, `state.go`; sdk/ts `upgrade.ts`).

#![cfg(all(feature = "upgrade", not(target_arch = "wasm32")))]

use std::cell::Cell;
use std::time::Duration;

use hop_top_kit::upgrade::check::{CheckConfig, Checker, Skipped};

fn github(tag: &str) -> String {
    format!(
        r#"{{"tag_name":"{tag}","body":"notes","assets":[
            {{"name":"tool_{os}_{arch}.tar.gz","browser_download_url":"https://dl/asset"}},
            {{"name":"tool_other_other.tar.gz","browser_download_url":"https://dl/other"}},
            {{"name":"checksums.txt","browser_download_url":"https://dl/sums"}}]}}"#,
        os = std::env::consts::OS,
        arch = std::env::consts::ARCH,
    )
}

fn cfg(dir: &std::path::Path) -> CheckConfig {
    CheckConfig::new("tool", "1.0.0")
        .github("org/tool")
        .state_dir(dir)
}

#[test]
fn newer_release_is_available() {
    let tmp = tempfile::tempdir().unwrap();
    let c = Checker::new(cfg(tmp.path()));
    let urls = Cell::new(0);
    let r = c.check(|url| {
        urls.set(urls.get() + 1);
        assert_eq!(url, "https://api.github.com/repos/org/tool/releases/latest");
        Ok(github("v1.2.0"))
    });
    assert!(r.update_avail, "{r:?}");
    assert_eq!((r.current.as_str(), r.latest.as_str()), ("1.0.0", "1.2.0"));
    assert_eq!(r.url, "https://dl/asset");
    assert_eq!(r.checksum_url, "https://dl/sums");
    assert_eq!(r.notes, "notes");
    assert!(r.error.is_none() && r.skipped.is_none() && !r.from_cache);
}

#[test]
fn equal_older_and_prerelease_are_not_updates() {
    let tmp = tempfile::tempdir().unwrap();
    for (cur, tag, want) in [
        ("1.0.0", "v1.0.0", false),
        ("1.2.0", "v1.0.0", false),
        ("1.0.0-rc.1", "v1.0.0", true),
        ("1.0.0", "v1.0.0-rc.1", false),
        ("1.9.0", "v1.10.0", true),
    ] {
        let dir = tmp.path().join(format!("{cur}-{tag}"));
        let c = Checker::new(CheckConfig::new("tool", cur).github("o/t").state_dir(&dir));
        let r = c.check(|_| Ok(github(tag)));
        assert_eq!(r.update_avail, want, "{cur} vs {tag}");
    }
}

#[test]
fn offline_short_circuits_before_fetch_and_cache() {
    let tmp = tempfile::tempdir().unwrap();
    // A warm cache must not be served either: offline disables the check.
    let warm = Checker::new(cfg(tmp.path()));
    assert!(warm.check(|_| Ok(github("v2.0.0"))).update_avail);

    let c = Checker::new(cfg(tmp.path()).offline(true));
    let r = c.check(|_| panic!("offline must not fetch"));
    assert_eq!(r.skipped, Some(Skipped::Offline));
    assert!(!r.update_avail && r.error.is_none() && !r.from_cache);
    assert_eq!(c.notice(&r), None);
    let mut out = Vec::new();
    c.notify(&r, &mut out).unwrap();
    assert!(out.is_empty());
}

#[test]
fn offline_leaves_no_cache_file() {
    let tmp = tempfile::tempdir().unwrap();
    let dir = tmp.path().join("state");
    let c = Checker::new(cfg(&dir).offline(true));
    let _ = c.check(|_| panic!("no fetch"));
    assert!(!dir.exists());
}

#[test]
fn a_fresh_cache_answers_without_fetching() {
    let tmp = tempfile::tempdir().unwrap();
    let c = Checker::new(cfg(tmp.path()));
    assert!(c.check(|_| Ok(github("v1.5.0"))).update_avail);
    let r = c.check(|_| panic!("served from cache"));
    assert!(r.from_cache && r.update_avail, "{r:?}");
    assert_eq!(r.latest, "1.5.0");
}

#[test]
fn an_expired_cache_is_refetched() {
    let tmp = tempfile::tempdir().unwrap();
    let c = Checker::new(cfg(tmp.path()).cache_ttl(Duration::ZERO));
    c.check(|_| Ok(github("v1.5.0")));
    let r = c.check(|_| Ok(github("v1.6.0")));
    assert!(!r.from_cache);
    assert_eq!(r.latest, "1.6.0");
}

#[test]
fn a_cache_for_an_older_build_is_recomputed() {
    // The tool upgraded since the cached answer: "update available"
    // must be judged against the running version, not the stored flag.
    let tmp = tempfile::tempdir().unwrap();
    Checker::new(cfg(tmp.path())).check(|_| Ok(github("v1.5.0")));
    let newer = CheckConfig::new("tool", "1.5.0")
        .github("org/tool")
        .state_dir(tmp.path());
    let r = Checker::new(newer).check(|_| panic!("cached"));
    assert!(r.from_cache && !r.update_avail, "{r:?}");
}

#[test]
fn failures_are_embedded_and_never_cached() {
    let tmp = tempfile::tempdir().unwrap();
    let c = Checker::new(cfg(tmp.path()));
    let r = c.check(|_| Err("boom".into()));
    assert_eq!(r.error.as_deref(), Some("boom"));
    assert!(!r.update_avail);
    // Not cached: the next call fetches again.
    let r = c.check(|_| Ok(github("v1.1.0")));
    assert!(r.update_avail && !r.from_cache);
    // Garbage bodies are failures too.
    let tmp2 = tempfile::tempdir().unwrap();
    let r = Checker::new(cfg(tmp2.path())).check(|_| Ok("not json".into()));
    assert!(r.error.is_some() && !r.update_avail);
}

#[test]
fn no_source_is_an_error() {
    let tmp = tempfile::tempdir().unwrap();
    let c = Checker::new(CheckConfig::new("tool", "1.0.0").state_dir(tmp.path()));
    let r = c.check(|_| panic!("nothing to fetch"));
    assert_eq!(
        r.error.as_deref(),
        Some("upgrade: no release source configured")
    );
}

#[test]
fn a_custom_release_url_wins_and_has_its_own_shape() {
    let tmp = tempfile::tempdir().unwrap();
    let c = Checker::new(cfg(tmp.path()).release_url("https://example.test/latest.json"));
    let r = c.check(|url| {
        assert_eq!(url, "https://example.test/latest.json");
        Ok(r#"{"version":"v3.0.0","url":"https://dl/x","notes":"n"}"#.into())
    });
    assert!(r.update_avail);
    assert_eq!(
        (r.latest.as_str(), r.url.as_str()),
        ("3.0.0", "https://dl/x")
    );
    assert_eq!(
        c.notice(&r).unwrap(),
        "\nA new release of tool is available: v1.0.0 → v3.0.0\nhttps://dl/x\n"
    );
}

#[test]
fn notice_names_the_releases_page_for_github() {
    let tmp = tempfile::tempdir().unwrap();
    let c = Checker::new(cfg(tmp.path()));
    let r = c.check(|_| Ok(github("v1.2.0")));
    let mut out = Vec::new();
    c.notify(&r, &mut out).unwrap();
    assert_eq!(
        String::from_utf8(out).unwrap(),
        "\nA new release of tool is available: v1.0.0 → v1.2.0\n\
         https://github.com/org/tool/releases/latest\n"
    );
    let tmp2 = tempfile::tempdir().unwrap();
    let c2 = Checker::new(cfg(tmp2.path()));
    let none = c2.check(|_| Ok(github("v1.0.0")));
    assert_eq!(c2.notice(&none), None);
}

#[test]
fn long_notes_are_truncated() {
    let tmp = tempfile::tempdir().unwrap();
    let body = format!(
        r#"{{"tag_name":"v9.0.0","body":"{}","assets":[]}}"#,
        "x".repeat(1500)
    );
    let r = Checker::new(cfg(tmp.path())).check(|_| Ok(body));
    assert_eq!(r.notes.chars().count(), 1001);
    assert!(r.notes.ends_with('…'));
}

#[test]
fn the_async_form_matches_the_sync_one() {
    let tmp = tempfile::tempdir().unwrap();
    let c = Checker::new(cfg(tmp.path()));
    let r = block_on(c.check_async(|_| async { Ok(github("v1.2.0")) }));
    assert!(r.update_avail);
    let off = Checker::new(cfg(tmp.path()).offline(true));
    let r = block_on(off.check_async(|_| async { panic!("no fetch") }));
    assert_eq!(r.skipped, Some(Skipped::Offline));
}

/// Polls a future that never pends (no runtime in this feature set).
fn block_on<F: std::future::Future>(f: F) -> F::Output {
    use std::task::{Context, Poll, RawWaker, RawWakerVTable, Waker};
    fn noop(_: *const ()) {}
    fn clone(_: *const ()) -> RawWaker {
        RawWaker::new(std::ptr::null(), &VTABLE)
    }
    static VTABLE: RawWakerVTable = RawWakerVTable::new(clone, noop, noop, noop);
    let waker = unsafe { Waker::from_raw(RawWaker::new(std::ptr::null(), &VTABLE)) };
    let mut cx = Context::from_waker(&waker);
    let mut f = std::pin::pin!(f);
    match f.as_mut().poll(&mut cx) {
        Poll::Ready(v) => v,
        Poll::Pending => panic!("future pended"),
    }
}
