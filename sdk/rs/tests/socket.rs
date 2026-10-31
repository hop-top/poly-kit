//! Port of go/transport/socket's suite over a real socket, hosted by the
//! real serve supervisor.
#![cfg(all(feature = "socket", unix))]

use std::os::unix::fs::PermissionsExt;
use std::path::{Path, PathBuf};
use std::sync::atomic::{AtomicUsize, Ordering};
use std::sync::{Arc, Mutex};
use std::time::Duration;

use hop_top_kit::serve::{
    resolve, CancelToken, ResolveRequest, Service, ServiceConfig, ServiceConfigs, ServiceRegistry,
    Supervisor, SupervisorOptions,
};
use hop_top_kit::socket::*;
use serde_json::{json, Value};
use tokio::io::{AsyncBufReadExt, AsyncWriteExt, BufReader};
use tokio::net::UnixStream;

struct Running {
    svc: Arc<SocketService>,
    shutdown: CancelToken,
    done: tokio::task::JoinHandle<i32>,
    path: PathBuf,
    _dir: tempfile::TempDir,
}

impl Running {
    async fn stop(self) -> i32 {
        self.shutdown.cancel();
        self.done.await.unwrap()
    }
}

fn dir() -> tempfile::TempDir {
    // /tmp: macOS's $TMPDIR is long enough to blow the 103-byte limit.
    tempfile::Builder::new()
        .prefix("ks")
        .tempdir_in("/tmp")
        .unwrap()
}

async fn start(build: impl FnOnce(PathBuf) -> SocketService) -> Running {
    let d = dir();
    let path = d.path().join("t.sock");
    start_at(build(path.clone()), path, d).await
}

async fn start_at(svc: SocketService, path: PathBuf, d: tempfile::TempDir) -> Running {
    let svc = Arc::new(svc);
    let mut reg = ServiceRegistry::new();
    reg.register(svc.clone()).unwrap();
    let mut cfgs = ServiceConfigs::new();
    cfgs.insert("socket".into(), ServiceConfig::enabled());
    let out = resolve(
        &reg,
        &ResolveRequest {
            args: vec![],
            configs: Some(&cfgs),
            policy: None,
        },
    );
    let shutdown = CancelToken::new();
    let sup = Supervisor::new(reg, SupervisorOptions::default());
    let sd = shutdown.clone();
    let done = tokio::spawn(async move { sup.run(sd, &out.selected, &cfgs).await.exit_code });
    for _ in 0..200 {
        if svc.ready() {
            break;
        }
        tokio::time::sleep(Duration::from_millis(10)).await;
    }
    assert!(svc.ready(), "socket never became ready");
    Running {
        svc,
        shutdown,
        done,
        path,
        _dir: d,
    }
}

async fn call_on(path: &Path, req: Value) -> Value {
    let mut c = Client::dial(path).await;
    c.send(&req).await;
    c.recv().await
}

struct Client {
    rd: BufReader<tokio::net::unix::OwnedReadHalf>,
    wr: tokio::net::unix::OwnedWriteHalf,
}

impl Client {
    async fn dial(path: &Path) -> Self {
        let (rd, wr) = UnixStream::connect(path).await.unwrap().into_split();
        Self {
            rd: BufReader::new(rd),
            wr,
        }
    }
    async fn send(&mut self, v: &Value) {
        self.send_raw(&format!("{v}\n")).await;
    }
    async fn send_raw(&mut self, s: &str) {
        self.wr.write_all(s.as_bytes()).await.unwrap();
    }
    async fn recv(&mut self) -> Value {
        let mut line = String::new();
        tokio::time::timeout(Duration::from_secs(5), self.rd.read_line(&mut line))
            .await
            .expect("no response")
            .unwrap();
        serde_json::from_str(&line).unwrap_or_else(|e| panic!("bad line {line:?}: {e}"))
    }
}

fn echo() -> Handlers {
    let mut h = Handlers::new();
    h.register(&["ping"], |inv: Invocation, _c: CancelToken| async move {
        Ok(CommandResult::data(json!({
            "args": inv.args,
            "caller": inv.meta.caller,
            "tenant": inv.meta.tenant,
            "request_id": inv.meta.request_id,
            "trace_id": inv.meta.trace_id,
            "established": format!("{:?}", inv.meta.established),
            "extra": inv.meta.extra,
            "scopes": inv.meta.scopes,
        })))
    })
    .unwrap();
    h
}

#[tokio::test]
async fn serves_a_command_and_is_owner_only() {
    let r = start(|p| SocketService::new(p, echo())).await;
    let resp = call_on(&r.path, json!({"path":["ping"],"args":["7"]})).await;
    assert_eq!(resp["ok"], true, "{resp}");
    assert_eq!(resp["result"]["exit_code"], 0);
    assert_eq!(resp["result"]["data"]["args"], json!(["7"]));
    let mode = std::fs::metadata(&r.path).unwrap().permissions().mode() & 0o777;
    assert_eq!(mode, 0o600);
    assert_eq!(r.svc.addr().as_deref(), r.path.to_str());
    assert_eq!(r.stop().await, 0);
}

#[tokio::test]
async fn bind_never_exposes_a_wider_socket() {
    // The socket is narrowed inside a private directory before it gets
    // its name: no observer sees the name with a wider mode. Poll the
    // moment the name appears.
    let d = dir();
    let path = d.path().join("t.sock");
    let watch = {
        let path = path.clone();
        tokio::task::spawn_blocking(move || {
            let end = std::time::Instant::now() + Duration::from_secs(5);
            while std::time::Instant::now() < end {
                if let Ok(m) = std::fs::symlink_metadata(&path) {
                    return m.permissions().mode() & 0o777;
                }
            }
            0
        })
    };
    let r = start_at(SocketService::new(path.clone(), echo()), path, d).await;
    assert_eq!(watch.await.unwrap(), 0o600);
    r.stop().await;
}

#[tokio::test]
async fn sequential_and_pipelined_requests_answer_in_order() {
    let mut h = Handlers::new();
    h.register(&["slow"], |_i: Invocation, _c: CancelToken| async {
        tokio::time::sleep(Duration::from_millis(150)).await;
        Ok(CommandResult::stdout("slow"))
    })
    .unwrap();
    h.register(&["fast"], |_i: Invocation, _c: CancelToken| async {
        Ok(CommandResult::stdout("fast"))
    })
    .unwrap();
    let r = start(|p| SocketService::new(p, h)).await;
    let mut c = Client::dial(&r.path).await;
    c.send_raw("{\"path\":[\"slow\"]}\n{\"path\":[\"fast\"]}\n{\"path\":[\"slow\"]}\n")
        .await;
    let got: Vec<String> = vec![
        c.recv().await["result"]["stdout"].as_str().unwrap().into(),
        c.recv().await["result"]["stdout"].as_str().unwrap().into(),
        c.recv().await["result"]["stdout"].as_str().unwrap().into(),
    ];
    assert_eq!(got, ["slow", "fast", "slow"]);
    r.stop().await;
}

#[tokio::test]
async fn refusals_on_the_wire() {
    let r = start(|p| SocketService::new(p, echo())).await;
    let mut c = Client::dial(&r.path).await;

    c.send(&json!({"path":["nosuch","cmd"]})).await;
    let e = c.recv().await;
    assert_eq!(e["ok"], false);
    assert_eq!(e["error"]["code"], "NOT_FOUND");
    assert!(e["error"]["message"]
        .as_str()
        .unwrap()
        .contains("nosuch cmd"));

    // A malformed line does not close the connection.
    c.send_raw("{not json\n").await;
    assert_eq!(c.recv().await["error"]["code"], "INVALID");
    c.send(&json!({"path":[]})).await;
    let e = c.recv().await;
    assert_eq!(e["error"]["code"], "INVALID");
    assert_eq!(e["error"]["message"], "path is required");
    c.send_raw("{\"args\":[\"x\"]}\n").await;
    assert_eq!(c.recv().await["error"]["code"], "INVALID");

    c.send(&json!({"path":["ping"]})).await;
    assert_eq!(c.recv().await["ok"], true);
    r.stop().await;
}

#[tokio::test]
async fn provenance_reaches_meta_and_ids_are_issued() {
    let r = start(|p| SocketService::new(p, echo())).await;
    let resp = call_on(
        &r.path,
        json!({"path":["ping"],"caller":"daemon","tenant":"acme","request_id":"r-1","trace_id":"t-1"}),
    )
    .await;
    let d = &resp["result"]["data"];
    assert_eq!(d["caller"], "daemon");
    assert_eq!(d["tenant"], "acme");
    assert_eq!(d["request_id"], "r-1");
    assert_eq!(d["trace_id"], "t-1");
    // Owner-only socket: transport-established, the claim stays a claim.
    assert_eq!(d["established"], "Transport");

    let resp = call_on(&r.path, json!({"path":["ping"]})).await;
    let id = resp["result"]["data"]["request_id"]
        .as_str()
        .unwrap()
        .to_string();
    assert_eq!(id.len(), 32);
    r.stop().await;
}

#[tokio::test]
async fn line_over_the_bound_is_answered_after_earlier_lines_then_closes() {
    let r = start(|p| SocketService::new(p, echo())).await;
    let mut c = Client::dial(&r.path).await;
    c.send(&json!({"path":["ping"]})).await;
    c.send_raw(&"x".repeat(MAX_LINE_BYTES + 10)).await;
    assert_eq!(c.recv().await["ok"], true);
    let e = c.recv().await;
    assert_eq!(e["error"]["code"], "INVALID");
    assert!(e["error"]["message"].as_str().unwrap().contains("exceeds"));
    let mut rest = String::new();
    assert_eq!(
        c.rd.read_line(&mut rest).await.unwrap(),
        0,
        "connection closes"
    );
    r.stop().await;
}

#[tokio::test]
async fn stop_unlinks_and_refuses_new_connections() {
    let r = start(|p| SocketService::new(p, echo())).await;
    let path = r.path.clone();
    let idle = Client::dial(&path).await;
    assert_eq!(r.stop().await, 0);
    assert!(!path.exists(), "socket file left behind");
    assert!(UnixStream::connect(&path).await.is_err());
    drop(idle);
}

#[tokio::test]
async fn bind_reclaims_stale_refuses_live_and_non_socket() {
    // Stale: a socket file nobody listens on.
    let d = dir();
    let path = d.path().join("t.sock");
    drop(std::os::unix::net::UnixListener::bind(&path).unwrap());
    assert!(path.exists());
    let r = start_at(SocketService::new(path.clone(), echo()), path.clone(), d).await;
    assert_eq!(call_on(&r.path, json!({"path":["ping"]})).await["ok"], true);

    // Live: a second service on the same path is refused, the first keeps serving.
    let svc = Arc::new(SocketService::new(path.clone(), echo()));
    let second = tokio::spawn({
        let svc = svc.clone();
        let mut reg = ServiceRegistry::new();
        reg.register(svc).unwrap();
        let mut cfgs = ServiceConfigs::new();
        cfgs.insert("socket".into(), ServiceConfig::enabled());
        async move {
            let out = resolve(
                &reg,
                &ResolveRequest {
                    args: vec![],
                    configs: Some(&cfgs),
                    policy: None,
                },
            );
            Supervisor::new(reg, SupervisorOptions::default())
                .run(CancelToken::new(), &out.selected, &cfgs)
                .await
                .exit_code
        }
    });
    assert_eq!(second.await.unwrap(), 1, "live path was taken");
    assert_eq!(call_on(&r.path, json!({"path":["ping"]})).await["ok"], true);
    r.stop().await;

    // Non-socket: refused and left untouched.
    let d = dir();
    let plain = d.path().join("plain");
    std::fs::write(&plain, "keep").unwrap();
    let svc = SocketService::new(plain.clone(), echo());
    let cancel = CancelToken::new();
    let err = bind_error(svc, cancel).await;
    assert!(err.contains("not a socket"), "{err}");
    assert_eq!(std::fs::read_to_string(&plain).unwrap(), "keep");
}

/// Run `start` directly for its error; the supervisor hides the text.
async fn bind_error(svc: SocketService, cancel: CancelToken) -> String {
    let mut reg = ServiceRegistry::new();
    let svc = Arc::new(svc);
    reg.register(svc.clone()).unwrap();
    let sink = Arc::new(Mutex::new(String::new()));
    struct Cap(Arc<Mutex<String>>);
    impl hop_top_kit::serve::ServeLogger for Cap {
        fn info(&self, _msg: &str, _kv: &[(&str, String)]) {}
        fn error(&self, msg: &str, kv: &[(&str, String)]) {
            let mut s = self.0.lock().unwrap();
            s.push_str(msg);
            for (k, v) in kv {
                s.push_str(&format!(" {k}={v}"));
            }
        }
    }
    let mut cfgs = ServiceConfigs::new();
    cfgs.insert("socket".into(), ServiceConfig::enabled());
    let out = resolve(
        &reg,
        &ResolveRequest {
            args: vec![],
            configs: Some(&cfgs),
            policy: None,
        },
    );
    let opts = SupervisorOptions {
        logger: Some(Arc::new(Cap(sink.clone()))),
        ..SupervisorOptions::default()
    };
    Supervisor::new(reg, opts)
        .run(cancel, &out.selected, &cfgs)
        .await;
    let s = sink.lock().unwrap().clone();
    s
}

#[tokio::test]
async fn disconnect_cancels_the_invocation() {
    let seen = Arc::new(AtomicUsize::new(0));
    let s2 = seen.clone();
    let mut h = Handlers::new();
    h.register(&["hang"], move |_i: Invocation, c: CancelToken| {
        let s2 = s2.clone();
        async move {
            s2.store(1, Ordering::SeqCst);
            c.cancelled().await;
            s2.store(2, Ordering::SeqCst);
            Ok(CommandResult::default())
        }
    })
    .unwrap();
    let r = start(|p| SocketService::new(p, h)).await;
    let mut c = Client::dial(&r.path).await;
    c.send(&json!({"path":["hang"]})).await;
    for _ in 0..100 {
        if seen.load(Ordering::SeqCst) == 1 {
            break;
        }
        tokio::time::sleep(Duration::from_millis(10)).await;
    }
    assert_eq!(seen.load(Ordering::SeqCst), 1);
    drop(c);
    // The handler's token fires and it returns.
    for _ in 0..100 {
        if seen.load(Ordering::SeqCst) == 2 {
            break;
        }
        tokio::time::sleep(Duration::from_millis(10)).await;
    }
    assert_eq!(
        seen.load(Ordering::SeqCst),
        2,
        "hang-up did not cancel the handler"
    );
    r.stop().await;
}

#[tokio::test]
async fn handler_errors_map_to_wire_codes_and_panics_are_internal() {
    let mut h = Handlers::new();
    h.register(&["limited"], |_i: Invocation, _c: CancelToken| async {
        Err(HandlerError::new(CODE_RATE_LIMITED, "slow down")
            .retry_after(Duration::from_micros(1500)))
    })
    .unwrap();
    h.register(&["plain"], |_i: Invocation, _c: CancelToken| async {
        Err(HandlerError::internal("boom"))
    })
    .unwrap();
    h.register(&["panic"], |_i: Invocation, _c: CancelToken| async move {
        if true {
            panic!("kaboom");
        }
        Ok(CommandResult::default())
    })
    .unwrap();
    let r = start(|p| SocketService::new(p, h)).await;
    let e = call_on(&r.path, json!({"path":["limited"]})).await;
    assert_eq!(e["error"]["code"], "RATE_LIMITED");
    assert_eq!(e["error"]["retry_after_ms"], 2, "rounded up to whole ms");
    let e = call_on(&r.path, json!({"path":["plain"]})).await;
    assert_eq!(e["error"]["code"], "INTERNAL");
    assert!(e["error"].get("retry_after_ms").is_none());
    let e = call_on(&r.path, json!({"path":["panic"]})).await;
    assert_eq!(e["error"]["code"], "INTERNAL");
    // The service survived the panic.
    assert_eq!(
        call_on(&r.path, json!({"path":["limited"]})).await["ok"],
        false
    );
    r.stop().await;
}

#[tokio::test]
async fn nonzero_exit_is_ok_true() {
    let mut h = Handlers::new();
    h.register(&["fail"], |_i: Invocation, _c: CancelToken| async {
        Ok(CommandResult {
            exit_code: 3,
            stderr: "bad".into(),
            ..Default::default()
        })
    })
    .unwrap();
    let r = start(|p| SocketService::new(p, h)).await;
    let resp = call_on(&r.path, json!({"path":["fail"]})).await;
    assert_eq!(resp["ok"], true);
    assert_eq!(resp["result"]["exit_code"], 3);
    r.stop().await;
}

#[test]
fn routes_refuse_empty_and_duplicate() {
    let mut h = Handlers::new();
    let noop = |_i: Invocation, _c: CancelToken| async { Ok(CommandResult::default()) };
    assert_eq!(h.register(&[], noop).err(), Some(RouteError::EmptyPath));
    h.register(&["a", "b"], noop).unwrap();
    assert_eq!(
        h.register(&["a", "b"], noop).err(),
        Some(RouteError::Duplicate("a b".into()))
    );
}

// ---- authentication ----

fn peer_auth() -> PeerAuthenticator {
    PeerAuthenticator::new(PeerAuthConfig {
        require_same_uid: true,
        scopes: None,
    })
}

#[tokio::test]
async fn peer_authenticator_names_the_kernel_uid_and_replaces_the_claim() {
    let r = start(|p| SocketService::new(p, echo()).authenticator(peer_auth())).await;
    let resp = call_on(
        &r.path,
        json!({"path":["ping"],"caller":"mallory","tenant":"evil"}),
    )
    .await;
    assert_eq!(resp["ok"], true, "{resp}");
    let d = &resp["result"]["data"];
    let uid = std::fs::metadata(&r.path)
        .map(|m| std::os::unix::fs::MetadataExt::uid(&m))
        .unwrap();
    assert_eq!(
        d["caller"],
        format!("uid:{uid}"),
        "the kernel's answer replaces the claim"
    );
    assert_eq!(d["tenant"], "");
    assert_eq!(d["established"], "Verified");
    assert_eq!(d["extra"]["peer_uid"], uid.to_string());
    assert_eq!(d["extra"]["peer_pid"], std::process::id().to_string());
    r.stop().await;
}

#[test]
fn peer_authenticator_refuses_another_uid_with_credentials_recorded() {
    let conn = Connection::new(
        Ok(PeerCred {
            uid: 5001,
            gid: 20,
            pid: Some(4242),
        }),
        501,
    );
    let err = peer_auth()
        .authenticate(&conn, &Request::default())
        .unwrap_err();
    assert_eq!(err.message, "peer uid 5001 is not the server's uid 501");
    assert_eq!(err.extra[EXTRA_PEER_UID], "5001");
    assert_eq!(err.extra[EXTRA_PEER_PID], "4242");

    // Without the restriction another uid is named, not refused.
    let id = PeerAuthenticator::default()
        .authenticate(&conn, &Request::default())
        .unwrap();
    assert_eq!(id.principal, "uid:5001");
    assert_eq!(id.extra[EXTRA_PEER_GID], "20");
}

#[test]
fn peer_authenticator_refuses_unreadable_credentials_and_scopes_only_admitted() {
    let bad = Connection::new(Err("no descriptor".into()), 501);
    let err = peer_auth()
        .authenticate(&bad, &Request::default())
        .unwrap_err();
    assert!(err.message.contains("no descriptor"));

    let asked = Arc::new(AtomicUsize::new(0));
    let a2 = asked.clone();
    let auth = PeerAuthenticator::new(
        PeerAuthConfig {
            require_same_uid: true,
            scopes: None,
        }
        .with_scopes(move |_| {
            a2.fetch_add(1, Ordering::SeqCst);
            vec![" read ".into(), "".into(), "read".into(), "write".into()]
        }),
    );
    let other = Connection::new(
        Ok(PeerCred {
            uid: 9,
            gid: 1,
            pid: None,
        }),
        501,
    );
    assert!(auth.authenticate(&other, &Request::default()).is_err());
    assert_eq!(
        asked.load(Ordering::SeqCst),
        0,
        "a refused peer is not asked for scopes"
    );
    let same = Connection::new(
        Ok(PeerCred {
            uid: 501,
            gid: 1,
            pid: None,
        }),
        501,
    );
    let id = auth.authenticate(&same, &Request::default()).unwrap();
    assert_eq!(id.scopes, ["read", "write"]);
    assert!(!id.extra.contains_key(EXTRA_PEER_PID), "no pid, no entry");
}

struct Deny;
impl Authenticator for Deny {
    fn authenticate(&self, _c: &Connection, req: &Request) -> Result<Identity, Refusal> {
        let mut r = Refusal::new(format!("no token for {}", req.path.join(" ")));
        r.extra.insert("seen".into(), "yes".into());
        r.extra.insert("scopes".into(), "smuggled".into());
        Err(r)
    }
}

#[tokio::test]
async fn refused_request_never_reaches_the_handler_and_is_reported() {
    let ran = Arc::new(AtomicUsize::new(0));
    let r2 = ran.clone();
    let mut h = Handlers::new();
    h.register(&["ping"], move |_i: Invocation, _c: CancelToken| {
        r2.fetch_add(1, Ordering::SeqCst);
        async { Ok(CommandResult::default()) }
    })
    .unwrap();
    let log: Arc<Mutex<Vec<(String, Meta)>>> = Arc::default();
    let l2 = log.clone();
    let r = start(move |p| {
        SocketService::new(p, h)
            .authenticator(Deny)
            .on_refused(move |inv, why| {
                l2.lock().unwrap().push((why.to_string(), inv.meta.clone()))
            })
    })
    .await;
    let resp = call_on(&r.path, json!({"path":["ping"],"request_id":"req-p"})).await;
    assert_eq!(resp["error"]["code"], "UNAUTHENTICATED");
    assert_eq!(resp["error"]["message"], "no token for ping");
    assert_eq!(ran.load(Ordering::SeqCst), 0);
    {
        let log = log.lock().unwrap();
        assert_eq!(log.len(), 1);
        assert_eq!(log[0].1.request_id, "req-p");
        assert_eq!(log[0].1.extra["seen"], "yes");
        assert!(!log[0].1.extra.contains_key("scopes"));
    }
    r.stop().await;
}

struct Scoped;
impl Authenticator for Scoped {
    fn authenticate(&self, _c: &Connection, _r: &Request) -> Result<Identity, Refusal> {
        let mut id = Identity {
            principal: "alice".into(),
            tenant: "acme".into(),
            scopes: vec!["a".into(), "b".into()],
            ..Default::default()
        };
        id.extra.insert("scopes".into(), "smuggled".into());
        id.extra.insert("k".into(), "v".into());
        Ok(id)
    }
}

#[tokio::test]
async fn authenticator_scopes_reach_the_handler_extra_cannot_grant_any() {
    let r = start(|p| SocketService::new(p, echo()).authenticator(Scoped)).await;
    let d =
        call_on(&r.path, json!({"path":["ping"],"caller":"bob"})).await["result"]["data"].clone();
    assert_eq!(d["caller"], "alice");
    assert_eq!(d["tenant"], "acme");
    assert_eq!(d["scopes"], json!(["a", "b"]));
    assert_eq!(d["extra"], json!({"k": "v"}));
    r.stop().await;
}

// ---- idempotency ----

fn counting() -> (Handlers, Arc<AtomicUsize>) {
    let n = Arc::new(AtomicUsize::new(0));
    let n2 = n.clone();
    let mut h = Handlers::new();
    h.register(&["ping"], move |inv: Invocation, _c: CancelToken| {
        let k = n2.fetch_add(1, Ordering::SeqCst) + 1;
        async move { Ok(CommandResult::stdout(format!("run {k} {:?}", inv.args))) }
    })
    .unwrap();
    h.register(&["flaky"], {
        let n = n.clone();
        move |_i: Invocation, _c: CancelToken| {
            let k = n.fetch_add(1, Ordering::SeqCst) + 1;
            async move {
                if k == 1 {
                    Err(HandlerError::internal("first fails"))
                } else {
                    Ok(CommandResult::stdout("ok"))
                }
            }
        }
    })
    .unwrap();
    (h, n)
}

#[tokio::test]
async fn idempotency_key_replays_and_rejects_reuse() {
    let (h, n) = counting();
    let r = start(|p| SocketService::new(p, h).idempotency(Duration::from_secs(3600))).await;
    let req = json!({"path":["ping"],"args":["a"],"idempotency_key":"k1"});

    let first = call_on(&r.path, req.clone()).await;
    assert_eq!(first["ok"], true);
    assert!(first.get("replayed").is_none());

    let second = call_on(&r.path, req.clone()).await;
    assert_eq!(second["replayed"], true);
    assert_eq!(second["result"], first["result"]);
    assert_eq!(n.load(Ordering::SeqCst), 1);

    // Owner-only socket vouches for the connection, not the claimed
    // name: bob is still the owner, and the owner's key replays.
    let mut bob = req.clone();
    bob["caller"] = json!("bob");
    assert_eq!(call_on(&r.path, bob).await["replayed"], true);

    let mut reused = req.clone();
    reused["args"] = json!(["b"]);
    let e = call_on(&r.path, reused).await;
    assert_eq!(e["error"]["code"], "CONFLICT");
    assert!(e["error"]["message"]
        .as_str()
        .unwrap()
        .contains("idempotency_key_reused"));
    assert_eq!(n.load(Ordering::SeqCst), 1);

    // Flag order does not change what the call is.
    let a = json!({"path":["ping"],"flags":{"x":1,"y":{"p":1,"q":2}},"idempotency_key":"k2"});
    let b = json!({"path":["ping"],"flags":{"y":{"q":2,"p":1},"x":1},"idempotency_key":"k2"});
    call_on(&r.path, a).await;
    assert_eq!(call_on(&r.path, b).await["replayed"], true);
    r.stop().await;
}

#[tokio::test]
async fn failed_calls_are_not_recorded() {
    let (h, n) = counting();
    let r = start(|p| SocketService::new(p, h).idempotency(Duration::ZERO)).await;
    let req = json!({"path":["flaky"],"idempotency_key":"k"});
    assert_eq!(call_on(&r.path, req.clone()).await["ok"], false);
    let again = call_on(&r.path, req.clone()).await;
    assert_eq!(again["ok"], true);
    assert!(again.get("replayed").is_none(), "a failure left a record");
    assert_eq!(call_on(&r.path, req).await["replayed"], true);
    assert_eq!(n.load(Ordering::SeqCst), 2);
    r.stop().await;
}

#[tokio::test]
async fn key_in_flight_is_a_conflict_and_without_the_option_keys_are_ignored() {
    let release = Arc::new(tokio::sync::Notify::new());
    let started = Arc::new(tokio::sync::Notify::new());
    let mut h = Handlers::new();
    let (rel, st) = (release.clone(), started.clone());
    h.register(&["ping"], move |_i: Invocation, _c: CancelToken| {
        let (rel, st) = (rel.clone(), st.clone());
        async move {
            st.notify_one();
            rel.notified().await;
            Ok(CommandResult::stdout("done"))
        }
    })
    .unwrap();
    let r = start(|p| SocketService::new(p, h).idempotency(Duration::from_secs(60))).await;
    let req = json!({"path":["ping"],"idempotency_key":"k1"});
    let first = tokio::spawn({
        let (p, req) = (r.path.clone(), req.clone());
        async move { call_on(&p, req).await }
    });
    started.notified().await;
    let e = call_on(&r.path, req).await;
    assert_eq!(e["error"]["code"], "CONFLICT");
    assert!(e["error"]["message"]
        .as_str()
        .unwrap()
        .contains("idempotency_conflict"));
    release.notify_one();
    assert_eq!(first.await.unwrap()["ok"], true);
    r.stop().await;

    // Off by default: the key is ignored and every call runs.
    let (h, n) = counting();
    let r = start(|p| SocketService::new(p, h)).await;
    let req = json!({"path":["ping"],"idempotency_key":"k"});
    call_on(&r.path, req.clone()).await;
    assert!(call_on(&r.path, req).await.get("replayed").is_none());
    assert_eq!(n.load(Ordering::SeqCst), 2);
    r.stop().await;
}

#[tokio::test]
async fn nonzero_exit_is_not_recorded() {
    let n = Arc::new(AtomicUsize::new(0));
    let n2 = n.clone();
    let mut h = Handlers::new();
    h.register(&["fail"], move |_i: Invocation, _c: CancelToken| {
        n2.fetch_add(1, Ordering::SeqCst);
        async {
            Ok(CommandResult {
                exit_code: 1,
                ..Default::default()
            })
        }
    })
    .unwrap();
    let r = start(|p| SocketService::new(p, h).idempotency(Duration::ZERO)).await;
    let req = json!({"path":["fail"],"idempotency_key":"k"});
    call_on(&r.path, req.clone()).await;
    assert!(call_on(&r.path, req).await.get("replayed").is_none());
    assert_eq!(n.load(Ordering::SeqCst), 2);
    r.stop().await;
}
