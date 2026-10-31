//! Ports of `go/runtime/sideeffect/real`'s tests and the cross-impl
//! contract for the real implementations. The HTTP half needs
//! `sideeffect-http`.

#![cfg(all(feature = "sideeffect-real", not(target_arch = "wasm32")))]

use std::sync::{Arc, Mutex};

use hop_top_kit::bus::{Context, MemoryAdapter};
use hop_top_kit::sideeffect::real;
use hop_top_kit::sideeffect::{Bus, Command, Exec, ExecError, Fs, PublishError};
use serde_json::json;

#[test]
fn real_fs_write_mkdir_rename_remove() {
    let dir = tempfile::tempdir().unwrap();
    let fs = real::Fs;
    let sub = dir.path().join("a").join("b");
    fs.mkdir_all(&sub, 0o755).unwrap();
    let src = sub.join("src.txt");
    fs.write_file(&src, b"hi", 0o600).unwrap();
    let dst = sub.join("dst.txt");
    fs.rename(&src, &dst).unwrap();
    assert_eq!(std::fs::read(&dst).unwrap(), b"hi");
    fs.remove(&dst).unwrap();
    assert!(!dst.exists());
}

#[test]
fn real_fs_write_truncates_and_accepts_nil_data() {
    let dir = tempfile::tempdir().unwrap();
    let p = dir.path().join("f");
    let fs = real::Fs;
    fs.write_file(&p, b"long content", 0o644).unwrap();
    fs.write_file(&p, &[], 0o644).unwrap();
    assert_eq!(std::fs::read(&p).unwrap(), b"");
    fs.mkdir_all(&dir.path().join("a/b/c/d"), 0o755).unwrap();
}

#[cfg(unix)]
#[test]
fn real_fs_applies_mode() {
    use std::os::unix::fs::PermissionsExt;
    let dir = tempfile::tempdir().unwrap();
    let p = dir.path().join("secret");
    real::Fs.write_file(&p, b"x", 0o600).unwrap();
    let mode = std::fs::metadata(&p).unwrap().permissions().mode();
    assert_eq!(mode & 0o777, 0o600);
}

#[test]
fn real_fs_remove_missing_is_not_found() {
    let dir = tempfile::tempdir().unwrap();
    let err = real::Fs.remove(&dir.path().join("nope")).unwrap_err();
    assert_eq!(err.kind(), std::io::ErrorKind::NotFound);
}

#[test]
fn real_bus_publish_delegates() {
    let adapter = Arc::new(MemoryAdapter::new());
    let seen = Arc::new(Mutex::new(Vec::new()));
    let s = seen.clone();
    adapter.subscribe("#", move |e| {
        s.lock()
            .unwrap()
            .push((e.topic.0.clone(), e.source.clone(), e.payload.clone()));
        Ok(())
    });
    let b = real::Bus::new(adapter);
    b.publish(
        &Context::background(),
        "kit.runtime.entity.created",
        "test",
        json!(42),
    )
    .unwrap();
    assert_eq!(
        *seen.lock().unwrap(),
        [(
            "kit.runtime.entity.created".to_string(),
            "test".to_string(),
            json!(42)
        )]
    );
}

#[test]
fn real_bus_nil_publisher() {
    let err = real::Bus::default()
        .publish(&Context::background(), "k.r.e.created", "test", json!(null))
        .unwrap_err();
    assert!(matches!(err, PublishError::NilPublisher));
    assert_eq!(err.to_string(), "sideeffect/real: nil publisher");
}

#[cfg(unix)]
#[test]
fn real_exec_run_output() {
    let e = real::Exec;
    let out = e.output(&Command::new("echo").args(["hello"])).unwrap();
    assert_eq!(out, b"hello\n");
    e.run(&Command::new("true")).unwrap();
    match e.run(&Command::new("false")) {
        Err(ExecError::Exit { code, .. }) => assert_eq!(code, Some(1)),
        other => panic!("want exit error, got {other:?}"),
    }
    assert!(matches!(
        e.run(&Command::new("/definitely/not/here")),
        Err(ExecError::Spawn { .. })
    ));
}

#[cfg(unix)]
#[test]
fn real_exec_honours_dir_and_env() {
    let dir = tempfile::tempdir().unwrap();
    let cmd = Command::new("sh")
        .args([
            "-c",
            "printf '%s:%s' \"$SIDEEFFECT_X\" \"$(basename \"$PWD\")\"",
        ])
        .env("SIDEEFFECT_X", "v")
        .current_dir(dir.path());
    let out = real::Exec.output(&cmd).unwrap();
    let want = format!("v:{}", dir.path().file_name().unwrap().to_string_lossy());
    assert_eq!(String::from_utf8(out).unwrap(), want);
}

#[cfg(feature = "sideeffect-http")]
mod http {
    use hop_top_kit::netpolicy::NetPolicy;
    use hop_top_kit::sideeffect::real;
    use hop_top_kit::sideeffect::{Http, Request};
    use mockito::Server;

    #[tokio::test]
    async fn real_http_sends_request_and_returns_response() {
        let mut server = Server::new_async().await;
        let mock = server
            .mock("POST", "/x")
            .match_header("x-test", "1")
            .match_body("payload")
            .with_status(418)
            .with_header("x-reply", "yes")
            .with_body("short and stout")
            .create_async()
            .await;
        let h = real::Http::new().unwrap();
        let req = Request::new("POST", format!("{}/x", server.url()))
            .with_header("x-test", "1")
            .with_body(b"payload".to_vec());
        let resp = h.send(req).await.unwrap();
        mock.assert_async().await;
        assert_eq!(resp.status, 418);
        assert_eq!(resp.body, b"short and stout");
        assert_eq!(resp.header("X-Reply"), Some("yes"));
    }

    #[tokio::test]
    async fn real_http_get_ok() {
        let mut server = Server::new_async().await;
        server
            .mock("GET", "/")
            .with_status(200)
            .create_async()
            .await;
        let resp = real::Http::new()
            .unwrap()
            .send(Request::get(server.url()))
            .await
            .unwrap();
        assert_eq!(resp.status, 200);
    }

    #[tokio::test]
    async fn real_http_refuses_off_loopback_when_offline() {
        let h = real::Http::with_policy(NetPolicy::offline()).unwrap();
        let err = h
            .send(Request::get("https://example.com/x?token=secret"))
            .await
            .unwrap_err();
        assert!(err.is_offline(), "{err}");
        assert!(!err.to_string().contains("secret"), "{err}");
    }

    #[tokio::test]
    async fn real_http_allows_loopback_when_offline() {
        let mut server = Server::new_async().await;
        server
            .mock("GET", "/")
            .with_status(204)
            .create_async()
            .await;
        let h = real::Http::with_policy(NetPolicy::offline()).unwrap();
        assert_eq!(
            h.send(Request::get(server.url())).await.unwrap().status,
            204
        );
    }

    #[tokio::test]
    async fn real_http_rejects_bad_method() {
        let err = real::Http::new()
            .unwrap()
            .send(Request::new("BAD METHOD", "http://127.0.0.1/"))
            .await
            .unwrap_err();
        assert!(!err.is_offline());
    }
}
