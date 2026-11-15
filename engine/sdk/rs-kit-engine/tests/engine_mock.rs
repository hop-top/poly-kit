#![cfg(feature = "http")]

use std::os::unix::fs::PermissionsExt;
use std::path::PathBuf;
use std::time::Duration;

use hop_top_kit_engine::{EngineError, KitEngine, KitEngineOptions};

/// Writes an executable stand-in for `kit` into `dir`.
// Tests that write a fake `kit` script and spawn it run one at a time. On
// Linux a child forked by a parallel test briefly holds the script's write
// descriptor (close-on-exec only fires at its exec), and exec of the script
// then fails with ETXTBSY. Hold the guard for the whole test.
static SPAWN: tokio::sync::Mutex<()> = tokio::sync::Mutex::const_new(());

async fn serial() -> tokio::sync::MutexGuard<'static, ()> {
    SPAWN.lock().await
}

fn fake_kit(dir: &std::path::Path, body: &str) -> PathBuf {
    let p = dir.join("kit");
    std::fs::write(&p, format!("#!/bin/sh\n{body}\n")).unwrap();
    std::fs::set_permissions(&p, std::fs::Permissions::from_mode(0o755)).unwrap();
    p
}

fn opts(bin: PathBuf) -> KitEngineOptions {
    KitEngineOptions {
        bin_path: Some(bin),
        startup_timeout: Duration::from_secs(5),
        stop_timeout: Duration::from_millis(300),
        ..Default::default()
    }
}

fn host_port(server: &mockito::ServerGuard) -> u16 {
    server
        .host_with_port()
        .rsplit(':')
        .next()
        .unwrap()
        .parse()
        .unwrap()
}

#[tokio::test]
async fn connect_gates_on_health_and_reads_pid() {
    let mut s = mockito::Server::new_async().await;
    let m = s
        .mock("GET", "/health")
        .with_body(r#"{"status":"ok","pid":4242}"#)
        .create_async()
        .await;
    let e = KitEngine::connect(host_port(&s), Some("t".into()), Some("st".into()))
        .await
        .unwrap();
    m.assert_async().await;
    assert_eq!(e.pid, 4242);
    assert_eq!(e.token.as_deref(), Some("t"));
    assert_eq!(e.shutdown_token.as_deref(), Some("st"));
    assert_eq!(e.base_url, format!("http://127.0.0.1:{}", host_port(&s)));
}

#[tokio::test]
async fn connect_fails_on_unhealthy() {
    let mut s = mockito::Server::new_async().await;
    s.mock("GET", "/health")
        .with_status(503)
        .create_async()
        .await;
    let err = KitEngine::connect(host_port(&s), None, None)
        .await
        .err()
        .unwrap();
    assert!(
        matches!(err, EngineError::Status { status: 503, .. }),
        "{err:?}"
    );
}

#[tokio::test]
async fn start_spawns_parses_startup_line_health_checks_and_stops() {
    let _serial = serial().await;
    let mut s = mockito::Server::new_async().await;
    let port = host_port(&s);
    let health = s
        .mock("GET", "/health")
        .with_body(r#"{"status":"ok"}"#)
        .create_async()
        .await;
    let shutdown = s
        .mock("POST", "/shutdown")
        .match_header("authorization", "Bearer sdt")
        .with_status(200)
        .create_async()
        .await;
    let dir = tempfile_dir("start");
    // Echoes argv to a file so flag mapping is checked; exits on its own
    // shortly after printing, like an engine that honours /shutdown.
    let argv = dir.join("argv");
    let bin = fake_kit(
        &dir,
        &format!(
            "echo \"$@\" > {}\necho '{{\"port\":{port},\"pid\":77,\"token\":\"tok\",\"shutdown_token\":\"sdt\"}}'\nsleep 0.2",
            argv.display()
        ),
    );
    let mut o = opts(bin);
    o.app = Some("myapp".into());
    o.data = Some("/d".into());
    o.encrypt = true;
    o.no_peer = true;
    o.no_sync = true;
    o.daemon = true;
    let e = KitEngine::start(o).await.unwrap();
    health.assert_async().await;
    assert_eq!((e.port, e.pid), (port, 77));
    assert_eq!(e.token.as_deref(), Some("tok"));
    assert_eq!(e.shutdown_token.as_deref(), Some("sdt"));
    let args = std::fs::read_to_string(&argv).unwrap();
    assert_eq!(
        args.trim(),
        "serve --port 0 --app myapp --data /d --daemon --encrypt --no-peer --no-sync"
    );
    e.stop().await.unwrap();
    shutdown.assert_async().await;
}

#[tokio::test]
async fn stop_kills_child_that_ignores_shutdown() {
    let _serial = serial().await;
    let mut s = mockito::Server::new_async().await;
    let port = host_port(&s);
    s.mock("GET", "/health").create_async().await;
    s.mock("POST", "/shutdown").create_async().await;
    let dir = tempfile_dir("kill");
    let pidfile = dir.join("pid");
    let bin = fake_kit(
        &dir,
        &format!(
            "echo $$ > {}\necho '{{\"port\":{port},\"pid\":1,\"token\":\"\",\"shutdown_token\":\"\"}}'\nexec sleep 60",
            pidfile.display()
        ),
    );
    let e = KitEngine::start(opts(bin)).await.unwrap();
    e.stop().await.unwrap();
    let pid: i32 = std::fs::read_to_string(&pidfile)
        .unwrap()
        .trim()
        .parse()
        .unwrap();
    // kill(pid, 0): ESRCH once the child is reaped.
    let alive = std::process::Command::new("kill")
        .args(["-0", &pid.to_string()])
        .status()
        .unwrap()
        .success();
    assert!(!alive, "child {pid} leaked");
}

#[tokio::test]
async fn start_rejects_bad_startup_json() {
    let _serial = serial().await;
    let dir = tempfile_dir("badjson");
    let bin = fake_kit(&dir, "echo 'not json'\nsleep 5");
    let err = KitEngine::start(opts(bin)).await.err().unwrap();
    assert!(matches!(err, EngineError::Startup(_)), "{err:?}");
}

#[tokio::test]
async fn start_reports_early_exit() {
    let _serial = serial().await;
    let dir = tempfile_dir("exit");
    let bin = fake_kit(&dir, "exit 3");
    let err = KitEngine::start(opts(bin)).await.err().unwrap();
    assert!(matches!(err, EngineError::Startup(_)), "{err:?}");
}

#[tokio::test]
async fn start_fails_and_reaps_child_when_health_fails() {
    let _serial = serial().await;
    let mut s = mockito::Server::new_async().await;
    let port = host_port(&s);
    s.mock("GET", "/health")
        .with_status(500)
        .create_async()
        .await;
    let dir = tempfile_dir("unhealthy");
    let pidfile = dir.join("pid");
    let bin = fake_kit(
        &dir,
        &format!(
            "echo $$ > {}\necho '{{\"port\":{port},\"pid\":1,\"token\":\"\",\"shutdown_token\":\"\"}}'\nexec sleep 60",
            pidfile.display()
        ),
    );
    let err = KitEngine::start(opts(bin)).await.err().unwrap();
    assert!(
        matches!(err, EngineError::Status { status: 500, .. }),
        "{err:?}"
    );
    tokio::time::sleep(Duration::from_millis(200)).await;
    let pid = std::fs::read_to_string(&pidfile)
        .unwrap()
        .trim()
        .to_string();
    let alive = std::process::Command::new("kill")
        .args(["-0", &pid])
        .status()
        .unwrap()
        .success();
    assert!(!alive, "child {pid} leaked after failed health check");
}

#[tokio::test]
async fn sync_peers_identity_clients() {
    let mut s = mockito::Server::new_async().await;
    s.mock("GET", "/health")
        .with_body(r#"{"pid":1}"#)
        .create_async()
        .await;
    let add = s
        .mock("POST", "/sync/remotes")
        .match_header("authorization", "Bearer t")
        .match_body(mockito::Matcher::JsonString(
            r#"{"name":"o","url":"http://x","mode":"both","filter":""}"#.into(),
        ))
        .with_status(201)
        .with_body(r#"{"name":"o","url":"http://x","mode":"both","filter":""}"#)
        .create_async()
        .await;
    let rm = s
        .mock("DELETE", "/sync/remotes/o%20x")
        .match_header("authorization", "Bearer t")
        .with_status(204)
        .create_async()
        .await;
    s.mock("GET", "/sync/status")
        .with_body(r#"{"remotes":[{"name":"o","url":"http://x","mode":"both","connected":false}]}"#)
        .create_async()
        .await;
    s.mock("GET", "/peers")
        .with_body(r#"[{"id":"p1","address":"1.2.3.4:5","trusted":false}]"#)
        .create_async()
        .await;
    let trust = s
        .mock("POST", "/peers/p1/trust")
        .match_header("authorization", "Bearer t")
        .with_status(204)
        .create_async()
        .await;
    let block = s
        .mock("POST", "/peers/p1/block")
        .match_header("authorization", "Bearer t")
        .with_status(204)
        .create_async()
        .await;
    s.mock("GET", "/identity")
        .with_body(r#"{"id":"me","public_key":"abc","fingerprint":"ff"}"#)
        .create_async()
        .await;
    let verify = s
        .mock("POST", "/identity/verify")
        .match_header("authorization", "Bearer t")
        .match_body(mockito::Matcher::JsonString(
            r#"{"data":"d","signature":"sig"}"#.into(),
        ))
        .with_body(r#"{"valid":true}"#)
        .create_async()
        .await;

    let e = KitEngine::connect(host_port(&s), Some("t".into()), None)
        .await
        .unwrap();
    let r = e
        .sync()
        .add_remote("o", "http://x", "both", "")
        .await
        .unwrap();
    assert_eq!(r.name, "o");
    add.assert_async().await;
    e.sync().remove_remote("o x").await.unwrap();
    rm.assert_async().await;
    let st = e.sync().status().await.unwrap();
    assert_eq!(st.len(), 1);
    assert_eq!(st[0].connected, Some(false));

    let peers = e.peers().list().await.unwrap();
    assert_eq!(peers[0].id, "p1");
    e.peers().trust("p1").await.unwrap();
    e.peers().block("p1").await.unwrap();
    trust.assert_async().await;
    block.assert_async().await;

    let k = e.identity().public_key().await.unwrap();
    assert_eq!(
        (k.public_key.as_str(), k.fingerprint.as_str()),
        ("abc", "ff")
    );
    assert!(e.identity().verify("d", "sig").await.unwrap().valid);
    verify.assert_async().await;
}

fn tempfile_dir(tag: &str) -> PathBuf {
    let d = std::env::temp_dir().join(format!("kit-engine-rs-{tag}-{}", std::process::id()));
    let _ = std::fs::remove_dir_all(&d);
    std::fs::create_dir_all(&d).unwrap();
    d
}
