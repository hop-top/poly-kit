//! Ports of the pure half of `go/runtime/sideeffect`'s tests: the dry-run
//! flag on a context, the dry-run publisher, and the dryrun and testfake
//! implementations. The real implementations are in
//! `sideeffect_real.rs`.

#![cfg(feature = "sideeffect")]

use std::panic::{catch_unwind, AssertUnwindSafe};
use std::sync::{Arc, Mutex};

use hop_top_kit::bus::Context;
use hop_top_kit::sideeffect::dryrun::{self, SharedBuf};
use hop_top_kit::sideeffect::testfake::{self, assert_called, assert_not_called, Call};
use hop_top_kit::sideeffect::{
    is_dry_run, with_dry_run, Bus, Command, DryRunPublisher, Exec, Fs, Http, PublishError, Request,
    Response, DRY_RUN_MECHANISM,
};
use serde_json::{json, Value};

// --- context ---

#[test]
fn is_dry_run_unset_context() {
    assert!(!is_dry_run(&Context::background()));
}

#[test]
fn is_dry_run_true_round_trip() {
    assert!(is_dry_run(&with_dry_run(&Context::background(), true)));
}

#[test]
fn is_dry_run_explicit_false_clears() {
    let ctx = with_dry_run(&Context::background(), true);
    assert!(!is_dry_run(&with_dry_run(&ctx, false)));
}

// --- DryRunPublisher ---

#[derive(Default)]
struct Recording(Mutex<Vec<Value>>);

impl Recording {
    fn last(&self) -> Option<Value> {
        self.0.lock().unwrap().last().cloned()
    }
}

impl Bus for Recording {
    fn publish(&self, _: &Context, _: &str, _: &str, payload: Value) -> Result<(), PublishError> {
        self.0.lock().unwrap().push(payload);
        Ok(())
    }
}

fn dry() -> Context {
    with_dry_run(&Context::background(), true)
}

#[test]
fn publisher_passthrough_when_not_dry_run() {
    let rec = Arc::new(Recording::default());
    let p = DryRunPublisher::new(Some(rec.clone()));
    let payload = json!({"qualifiers": {}, "id": "x"});
    p.publish(
        &Context::background(),
        "k.r.e.created",
        "src",
        payload.clone(),
    )
    .unwrap();
    assert_eq!(rec.last().unwrap(), payload);
}

#[test]
fn publisher_tags_when_dry_run() {
    let rec = Arc::new(Recording::default());
    let p = DryRunPublisher::new(Some(rec.clone()));
    p.publish(
        &dry(),
        "k.r.e.created",
        "src",
        json!({"qualifiers": {}, "id": "x"}),
    )
    .unwrap();
    assert_eq!(
        rec.last().unwrap(),
        json!({"qualifiers": {"mechanism": DRY_RUN_MECHANISM}, "id": "x"})
    );
}

#[test]
fn publisher_tags_flattened_qualifiers() {
    let rec = Arc::new(Recording::default());
    let p = DryRunPublisher::new(Some(rec.clone()));
    p.publish(
        &dry(),
        "k.r.e.created",
        "src",
        json!({"reason": "r", "id": "x"}),
    )
    .unwrap();
    assert_eq!(
        rec.last().unwrap(),
        json!({"reason": "r", "mechanism": "dry_run", "id": "x"})
    );
}

#[test]
fn publisher_preserves_existing_mechanism() {
    let rec = Arc::new(Recording::default());
    let p = DryRunPublisher::new(Some(rec.clone()));
    let payload = json!({"mechanism": "signal", "id": "x"});
    p.publish(&dry(), "k.r.e.created", "src", payload.clone())
        .unwrap();
    assert_eq!(rec.last().unwrap(), payload);
}

#[test]
fn publisher_payload_without_qualifiers() {
    let rec = Arc::new(Recording::default());
    let p = DryRunPublisher::new(Some(rec.clone()));
    let payload = json!({"id": "x"});
    p.publish(&dry(), "k.r.e.created", "src", payload.clone())
        .unwrap();
    assert_eq!(rec.last().unwrap(), payload);
    // Not an object at all: also unchanged.
    p.publish(&dry(), "k.r.e.created", "src", json!(7)).unwrap();
    assert_eq!(rec.last().unwrap(), json!(7));
}

#[test]
fn publisher_nil_inner_errors() {
    let p = DryRunPublisher::new(None);
    let err = p
        .publish(&Context::background(), "k.r.e.created", "src", Value::Null)
        .unwrap_err();
    assert!(matches!(err, PublishError::NilInner));
    assert_eq!(
        err.to_string(),
        "sideeffect: DryRunPublisher with nil Inner"
    );
}

// --- dryrun ---

#[test]
fn dryrun_fs_describes_and_returns_ok() {
    let buf = SharedBuf::default();
    let fs = dryrun::Fs::new().with_writer(buf.clone());
    fs.write_file("/etc/foo".as_ref(), b"data", 0o644).unwrap();
    fs.mkdir_all("/etc/foo.d".as_ref(), 0o755).unwrap();
    fs.rename("/a".as_ref(), "/b".as_ref()).unwrap();
    fs.remove("/c".as_ref()).unwrap();
    let out = buf.contents();
    for want in [
        "[dry-run] would write /etc/foo (4 bytes, mode 0644)",
        "[dry-run] would mkdir -p /etc/foo.d (mode 0755)",
        "[dry-run] would rename /a -> /b",
        "[dry-run] would remove /c",
    ] {
        assert!(out.contains(want), "missing {want:?} in:\n{out}");
    }
}

#[tokio::test]
async fn dryrun_http_get_passes_through() {
    let inner = Arc::new(testfake::Http::new());
    let buf = SharedBuf::default();
    let h = dryrun::Http::new(inner.clone()).with_writer(buf.clone());
    let resp = h.send(Request::get("http://example.test/x")).await.unwrap();
    assert_eq!(resp.status, 200);
    assert!(buf.contents().is_empty(), "safe verbs must not log");
    assert_eq!(inner.calls().len(), 1);
    // HEAD and the empty method are safe too.
    h.send(Request::new("HEAD", "http://example.test/x"))
        .await
        .unwrap();
    h.send(Request::new("", "http://example.test/x"))
        .await
        .unwrap();
    assert_eq!(inner.calls().len(), 3);
}

#[tokio::test]
async fn dryrun_http_post_intercepted() {
    let inner = Arc::new(testfake::Http::new());
    let buf = SharedBuf::default();
    let h = dryrun::Http::new(inner.clone()).with_writer(buf.clone());
    let resp = h
        .send(Request::new("POST", "http://example.test/x").with_body(b"payload".to_vec()))
        .await
        .unwrap();
    assert!(inner.calls().is_empty(), "inner must not be reached");
    assert_eq!(resp.status, 201);
    assert_eq!(resp.body, b"{}");
    let out = buf.contents();
    assert!(
        out.contains("[dry-run] would POST http://example.test/x"),
        "{out}"
    );
    assert!(out.contains("(7-byte body)"), "{out}");
    for verb in ["PUT", "PATCH", "DELETE"] {
        let r = h
            .send(Request::new(verb, "http://example.test/x"))
            .await
            .unwrap();
        assert_eq!(r.status, 201);
    }
    assert!(inner.calls().is_empty());
}

#[test]
fn dryrun_bus_augments_qualifiers_when_present() {
    let buf = SharedBuf::default();
    let b = dryrun::Bus::new().with_writer(buf.clone());
    b.publish(
        &Context::background(),
        "kit.runtime.entity.created",
        "test",
        json!({"qualifiers": {}, "id": "x"}),
    )
    .unwrap();
    assert!(
        buf.contents().contains("mechanism=dry_run"),
        "{}",
        buf.contents()
    );
}

#[test]
fn dryrun_bus_no_qualifiers_logs_gap_once() {
    let buf = SharedBuf::default();
    let b = dryrun::Bus::new().with_writer(buf.clone());
    for _ in 0..2 {
        b.publish(
            &Context::background(),
            "kit.runtime.entity.created",
            "test",
            json!({"id": "x"}),
        )
        .unwrap();
    }
    let out = buf.contents();
    assert!(
        out.contains("would publish kit.runtime.entity.created from test"),
        "{out}"
    );
    assert!(out.contains("does not embed bus.Qualifiers"), "{out}");
    assert_eq!(out.matches("does not embed").count(), 1, "{out}");
    assert_eq!(out.matches("would publish").count(), 2, "{out}");
}

#[test]
fn dryrun_exec_describes_and_returns_zero() {
    let buf = SharedBuf::default();
    let e = dryrun::Exec::new().with_writer(buf.clone());
    e.run(&Command::new("/bin/this-does-not-exist").args(["--flag", "with space"]))
        .unwrap();
    let out = e.output(&Command::new("rm").args(["-rf", "/"])).unwrap();
    assert!(out.is_empty());
    let s = buf.contents();
    assert!(
        s.contains(r#"would exec: /bin/this-does-not-exist --flag "with space""#),
        "{s}"
    );
    assert!(s.contains("would exec (capture): rm -rf /"), "{s}");
}

// --- testfake ---

#[test]
fn fs_records_all_calls() {
    let f = testfake::Fs::new();
    f.write_file("/a".as_ref(), b"hi", 0o600).unwrap();
    f.mkdir_all("/b".as_ref(), 0o755).unwrap();
    f.rename("/c".as_ref(), "/d".as_ref()).unwrap();
    f.remove("/e".as_ref()).unwrap();
    let methods: Vec<_> = f.calls().into_iter().map(|c| c.method).collect();
    assert_eq!(
        methods,
        ["FS.WriteFile", "FS.MkdirAll", "FS.Rename", "FS.Remove"]
    );
}

#[test]
fn fs_allow_list_rejects() {
    let f = testfake::Fs::new().allow(|c: &Call| c.method == "FS.WriteFile");
    f.write_file("/ok".as_ref(), b"data", 0o600).unwrap();
    let r = catch_unwind(AssertUnwindSafe(|| {
        let _ = f.mkdir_all("/nope".as_ref(), 0o755);
    }));
    let msg = *r
        .expect_err("MkdirAll outside the allowlist must panic")
        .downcast::<String>()
        .unwrap();
    assert!(
        msg.contains("testfake/FS: unexpected call FS.MkdirAll(/nope, 0755)"),
        "{msg}"
    );
    assert_eq!(f.calls().len(), 1, "rejected call is not recorded");
}

#[tokio::test]
async fn http_default_and_expect() {
    let h = testfake::Http::new();
    h.expect(Ok(Response::new(418)));
    let req = Request::new("POST", "https://example.test");
    assert_eq!(h.send(req.clone()).await.unwrap().status, 418);
    // After the queue empties: 200 OK with an empty JSON body.
    let r = h.send(req).await.unwrap();
    assert_eq!(r.status, 200);
    assert_eq!(r.body, b"{}");
    assert_eq!(
        h.calls()[0].to_string(),
        "HTTP.Do(POST, https://example.test)"
    );
}

#[test]
fn bus_publish_records_and_returns_expect() {
    let b = testfake::Bus::new();
    b.expect(Err(PublishError::NilInner));
    let got = b.publish(&Context::background(), "k.r.e.created", "src", json!(1));
    assert!(matches!(got, Err(PublishError::NilInner)));
    assert!(b
        .publish(&Context::background(), "k.r.e.created", "src", json!(1))
        .is_ok());
    let calls = b.calls();
    assert_eq!(calls.len(), 2);
    assert_eq!(calls[0].method, "Bus.Publish");
}

#[test]
fn exec_run_output_defaults() {
    let e = testfake::Exec::new();
    e.run(&Command::new("ls")).unwrap();
    assert!(e.output(&Command::new("ls")).unwrap().is_empty());
    e.expect_output(Ok(b"hello".to_vec()));
    assert_eq!(e.output(&Command::new("ls")).unwrap(), b"hello");
    assert_eq!(e.calls()[0].to_string(), "Exec.Run([ls])");
}

#[test]
fn assert_called_not_called() {
    let f = testfake::Fs::new();
    f.write_file("/hello".as_ref(), b"hi", 0o600).unwrap();
    let calls = f.calls();
    assert_called(&calls, |c| c.method == "FS.WriteFile");
    assert_not_called(&calls, |c| c.method == "FS.Remove");
    assert!(catch_unwind(|| assert_called(&calls, |c| c.method == "FS.Remove")).is_err());
    assert!(catch_unwind(|| assert_not_called(&calls, |c| c.method == "FS.WriteFile")).is_err());
}

// --- contract: pure impls ---

#[test]
fn contract_fs_nil_data_and_nested_mkdir() {
    let fss: Vec<Box<dyn Fs>> = vec![
        Box::new(dryrun::Fs::new().with_writer(SharedBuf::default())),
        Box::new(testfake::Fs::new()),
    ];
    for fs in fss {
        fs.write_file("/x/nil-data".as_ref(), &[], 0o644).unwrap();
        fs.mkdir_all("/x/a/b/c/d".as_ref(), 0o755).unwrap();
    }
}

#[test]
fn contract_bus_accepts_empty_topic() {
    let buses: Vec<Box<dyn Bus>> = vec![
        Box::new(dryrun::Bus::new().with_writer(SharedBuf::default())),
        Box::new(testfake::Bus::new()),
    ];
    for b in buses {
        b.publish(&Context::background(), "", "src", Value::Null)
            .unwrap();
    }
}

#[tokio::test]
async fn dryrun_http_preview_redacts_credentials() {
    let buf = SharedBuf::default();
    let h = dryrun::Http::new(Arc::new(testfake::Http::new())).with_writer(buf.clone());
    h.send(Request::new(
        "POST",
        "https://user:pw@example.test:8443/a/b?api=abc&flag&x=1#frag",
    ))
    .await
    .unwrap();
    let out = buf.contents();
    assert!(
        out.contains(
            "would POST https://example.test:8443/a/b?api=REDACTED&flag&x=REDACTED (0-byte body)"
        ),
        "{out}"
    );
    for leak in ["user", "pw", "abc", "frag"] {
        assert!(!out.contains(leak), "{leak} leaked: {out}");
    }
    // Unparseable input still never echoes a query value.
    h.send(Request::new("PUT", "no-scheme/p?token=zzz"))
        .await
        .unwrap();
    assert!(!buf.contents().contains("zzz"));
}
