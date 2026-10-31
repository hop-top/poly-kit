//! The RPC server over a real listener, hosted by the real supervisor:
//! all three protocols on one port, interceptors, recovery, limits.
//! Mirrors go/transport/rpc's e2e and interceptor suites.
#![cfg(feature = "rpc-server")]

use std::collections::HashMap;
use std::sync::atomic::{AtomicUsize, Ordering};
use std::sync::{Arc, Mutex};
use std::time::Duration;

use connectrpc::client::{ClientConfig, Http2Connection, HttpClient};
use connectrpc::{ErrorCode, Protocol};
use hop_top_kit::authn::{AuthFunc, AuthnError, Claims};
use hop_top_kit::rpc::proto::crud::v1::{
    CreateRequest, CreateResponse, DeleteRequest, DeleteResponse, GetRequest, GetResponse,
    ListRequest, ListResponse, UpdateRequest, UpdateResponse,
};
use hop_top_kit::rpc::{
    authenticate, log, request_id, EntityRpc, EntityService, RequestContext, RpcError, RpcLog,
    RpcServer, RpcServerBuilder,
};
use hop_top_kit::serve::{
    resolve, CancelToken, ResolveRequest, Service, ServiceConfig, ServiceConfigs, ServiceRegistry,
    Supervisor, SupervisorOptions,
};
use serde_json::{json, Value};
use tokio::io::{AsyncReadExt, AsyncWriteExt};

type Struct = buffa_types::google::protobuf::Struct;

fn strukt(v: Value) -> Struct {
    serde_json::from_value(v).expect("struct")
}

fn json_of(s: &Struct) -> Value {
    serde_json::to_value(s).expect("json")
}

#[derive(Default)]
struct Store {
    rows: Mutex<HashMap<String, Value>>,
    seq: AtomicUsize,
    calls: AtomicUsize,
    last_rid: Mutex<Option<String>>,
    last_claims: Mutex<Option<(bool, String)>>,
}

impl Store {
    fn seen(&self, ctx: &RequestContext) {
        self.calls.fetch_add(1, Ordering::SeqCst);
        *self.last_rid.lock().unwrap() = ctx.request_id().map(str::to_string);
        *self.last_claims.lock().unwrap() = Some((
            ctx.authenticated(),
            ctx.claims().map(|c| c.subject.clone()).unwrap_or_default(),
        ));
    }
}

struct Svc(Arc<Store>);

impl std::ops::Deref for Svc {
    type Target = Store;
    fn deref(&self) -> &Store {
        &self.0
    }
}

impl EntityService for Svc {
    async fn create(
        &self,
        ctx: &RequestContext,
        req: CreateRequest,
    ) -> Result<CreateResponse, RpcError> {
        self.seen(ctx);
        let mut v = json_of(&req.entity);
        if v["name"] == "panic" {
            panic!("kaboom");
        }
        let id = format!("w-{}", self.seq.fetch_add(1, Ordering::SeqCst) + 1);
        v["id"] = json!(id);
        self.rows.lock().unwrap().insert(id, v.clone());
        Ok(CreateResponse {
            entity: strukt(v).into(),
            ..Default::default()
        })
    }
    async fn get(&self, ctx: &RequestContext, req: GetRequest) -> Result<GetResponse, RpcError> {
        self.seen(ctx);
        match self.rows.lock().unwrap().get(&req.id) {
            Some(v) => Ok(GetResponse {
                entity: strukt(v.clone()).into(),
                ..Default::default()
            }),
            None => Err(RpcError::not_found(format!("entity {} not found", req.id))),
        }
    }
    async fn list(
        &self,
        ctx: &RequestContext,
        _req: ListRequest,
    ) -> Result<ListResponse, RpcError> {
        self.seen(ctx);
        let rows = self.rows.lock().unwrap();
        let mut ids: Vec<_> = rows.keys().cloned().collect();
        ids.sort();
        Ok(ListResponse {
            entities: ids.iter().map(|i| strukt(rows[i].clone())).collect(),
            ..Default::default()
        })
    }
    async fn update(
        &self,
        ctx: &RequestContext,
        req: UpdateRequest,
    ) -> Result<UpdateResponse, RpcError> {
        self.seen(ctx);
        let v = json_of(&req.entity);
        let id = v["id"].as_str().unwrap_or_default().to_string();
        let mut rows = self.rows.lock().unwrap();
        if !rows.contains_key(&id) {
            return Err(RpcError::not_found(format!("entity {id} not found")));
        }
        rows.insert(id, v.clone());
        Ok(UpdateResponse {
            entity: strukt(v).into(),
            ..Default::default()
        })
    }
    async fn delete(
        &self,
        ctx: &RequestContext,
        req: DeleteRequest,
    ) -> Result<DeleteResponse, RpcError> {
        self.seen(ctx);
        self.rows.lock().unwrap().remove(&req.id);
        Ok(DeleteResponse::default())
    }
}

struct Running {
    addr: String,
    shutdown: CancelToken,
    done: tokio::task::JoinHandle<i32>,
}

impl Running {
    async fn stop(self) -> i32 {
        self.shutdown.cancel();
        self.done.await.unwrap()
    }
}

async fn start(b: RpcServerBuilder) -> Running {
    let svc = Arc::new(b.build());
    let mut reg = ServiceRegistry::new();
    reg.register(svc.clone()).unwrap();
    let mut cfgs = ServiceConfigs::new();
    cfgs.insert("rpc".into(), ServiceConfig::enabled());
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
    for _ in 0..300 {
        if svc.ready() {
            break;
        }
        tokio::time::sleep(Duration::from_millis(10)).await;
    }
    assert!(svc.ready(), "rpc never became ready");
    Running {
        addr: svc.addr().expect("bound address"),
        shutdown,
        done,
    }
}

fn server(store: &Arc<Store>) -> RpcServerBuilder {
    RpcServer::builder("127.0.0.1:0").service(EntityRpc::new(Svc(store.clone())))
}

/// One HTTP/1.1 exchange, hand-rolled so the wire is what is asserted.
async fn http1(
    addr: &str,
    path: &str,
    headers: &[(&str, &str)],
    body: &str,
) -> (u16, String, String) {
    let mut s = tokio::net::TcpStream::connect(addr).await.unwrap();
    let mut req = format!(
        "POST {path} HTTP/1.1\r\nHost: {addr}\r\nConnection: close\r\nContent-Type: application/json\r\nContent-Length: {}\r\n",
        body.len()
    );
    for (k, v) in headers {
        req.push_str(&format!("{k}: {v}\r\n"));
    }
    req.push_str("\r\n");
    req.push_str(body);
    s.write_all(req.as_bytes()).await.unwrap();
    let mut raw = Vec::new();
    tokio::time::timeout(Duration::from_secs(5), s.read_to_end(&mut raw))
        .await
        .expect("response")
        .unwrap();
    let text = String::from_utf8_lossy(&raw).to_string();
    let (head, body) = text.split_once("\r\n\r\n").unwrap_or((&text, ""));
    let status = head.split_whitespace().nth(1).unwrap().parse().unwrap();
    let head = head.to_lowercase();
    let body = if head.contains("transfer-encoding: chunked") {
        dechunk(body)
    } else {
        body.to_string()
    };
    (status, head, body)
}

fn dechunk(mut rest: &str) -> String {
    let mut out = String::new();
    while let Some((size, tail)) = rest.split_once("\r\n") {
        let n = usize::from_str_radix(size.trim(), 16).unwrap_or(0);
        if n == 0 {
            break;
        }
        out.push_str(&tail[..n]);
        rest = &tail[n + 2..];
    }
    out
}

use connectrpc::client::ClientTransport;
use hop_top_kit::rpc::connect::crud::v1::EntityServiceClient;

type BodyErr<T> = <<T as ClientTransport>::ResponseBody as connectrpc::http_body::Body>::Error;

/// Create, Get, a missing Get and Delete: the same exchange in whatever
/// protocol the client was built for.
async fn roundtrip<T>(c: EntityServiceClient<T>, label: &str)
where
    T: ClientTransport,
    BodyErr<T>: std::fmt::Display,
{
    let created = c
        .create(CreateRequest {
            entity: strukt(json!({"name": label})).into(),
            ..Default::default()
        })
        .await
        .unwrap_or_else(|e| panic!("{label}: create: {e}"));
    let v = json_of(&created.into_owned().entity);
    let id = v["id"]
        .as_str()
        .unwrap_or_else(|| panic!("{label}: no id in {v}"))
        .to_string();
    assert_eq!(v["name"], label);

    let got = c
        .get(GetRequest {
            id: id.clone(),
            ..Default::default()
        })
        .await
        .unwrap_or_else(|e| panic!("{label}: get: {e}"));
    assert_eq!(json_of(&got.into_owned().entity)["name"], label);

    let err = match c
        .get(GetRequest {
            id: "nope".into(),
            ..Default::default()
        })
        .await
    {
        Ok(_) => panic!("{label}: missing entity found"),
        Err(e) => e,
    };
    assert_eq!(err.code, ErrorCode::NotFound, "{label}: {err}");

    c.delete(DeleteRequest {
        id,
        ..Default::default()
    })
    .await
    .unwrap_or_else(|e| panic!("{label}: delete: {e}"));
}

fn cfg(addr: &str, p: Protocol) -> (axum::http::Uri, ClientConfig) {
    let uri: axum::http::Uri = format!("http://{addr}").parse().unwrap();
    (uri.clone(), ClientConfig::new(uri).with_protocol(p))
}

#[tokio::test]
async fn one_port_answers_connect_grpc_and_grpc_web() {
    let store = Arc::new(Store::default());
    let r = start(server(&store)).await;

    let (_, c) = cfg(&r.addr, Protocol::Connect);
    roundtrip(
        EntityServiceClient::new(HttpClient::plaintext(), c),
        "connect",
    )
    .await;

    let (_, c) = cfg(&r.addr, Protocol::GrpcWeb);
    roundtrip(
        EntityServiceClient::new(HttpClient::plaintext(), c),
        "grpc-web",
    )
    .await;

    // Native gRPC: HTTP/2 with prior knowledge, no TLS (h2c).
    let (uri, c) = cfg(&r.addr, Protocol::Grpc);
    let conn = Http2Connection::connect_plaintext(uri)
        .await
        .unwrap()
        .shared(64);
    roundtrip(EntityServiceClient::new(conn, c), "grpc").await;

    assert_eq!(r.stop().await, 0);
}

#[tokio::test]
async fn connect_json_over_http1_has_the_documented_wire_shape() {
    let store = Arc::new(Store::default());
    let r = start(server(&store)).await;

    let (status, _, body) = http1(
        &r.addr,
        "/crud.v1.EntityService/Create",
        &[],
        r#"{"entity":{"name":"bolt"}}"#,
    )
    .await;
    assert_eq!(status, 200, "{body}");
    let v: Value = serde_json::from_str(&body).unwrap();
    assert_eq!(v["entity"]["name"], "bolt");
    assert_eq!(v["entity"]["id"], "w-1");

    let (status, _, body) = http1(
        &r.addr,
        "/crud.v1.EntityService/Get",
        &[],
        r#"{"id":"w-1"}"#,
    )
    .await;
    assert_eq!(status, 200);
    assert_eq!(
        serde_json::from_str::<Value>(&body).unwrap()["entity"]["name"],
        "bolt"
    );

    // A refusal is the Connect error: HTTP status from the code, JSON body.
    let (status, head, body) =
        http1(&r.addr, "/crud.v1.EntityService/Get", &[], r#"{"id":"zz"}"#).await;
    assert_eq!(status, 404);
    assert!(head.contains("application/json"), "{head}");
    let e: Value = serde_json::from_str(&body).unwrap();
    assert_eq!(e["code"], "not_found");
    assert_eq!(e["message"], "entity zz not found");

    let (status, _, body) = http1(&r.addr, "/crud.v1.EntityService/Nope", &[], "{}").await;
    assert_eq!(status, 404, "{body}");
    assert_eq!(
        serde_json::from_str::<Value>(&body).unwrap()["code"],
        "unimplemented"
    );
    r.stop().await;
}

#[tokio::test]
async fn request_id_reaches_the_handler_and_log_records_every_call() {
    let store = Arc::new(Store::default());
    let lines: Arc<Mutex<Vec<RpcLog>>> = Arc::default();
    let sink = lines.clone();
    let r = start(
        server(&store)
            .interceptor(request_id())
            .interceptor(log(move |l| sink.lock().unwrap().push(l.clone()))),
    )
    .await;

    // The caller's plain-token id is kept.
    http1(
        &r.addr,
        "/crud.v1.EntityService/List",
        &[("X-Request-ID", "req-42")],
        "{}",
    )
    .await;
    assert_eq!(store.last_rid.lock().unwrap().as_deref(), Some("req-42"));

    // One that is not a plain token is replaced, never echoed.
    http1(
        &r.addr,
        "/crud.v1.EntityService/List",
        &[("X-Request-ID", "bad id")],
        "{}",
    )
    .await;
    let rid = store.last_rid.lock().unwrap().clone().unwrap();
    assert_eq!(rid.len(), 32, "{rid}");

    // None given: minted, and different each call.
    http1(&r.addr, "/crud.v1.EntityService/List", &[], "{}").await;
    let rid2 = store.last_rid.lock().unwrap().clone().unwrap();
    assert_ne!(rid, rid2);

    http1(&r.addr, "/crud.v1.EntityService/Get", &[], r#"{"id":"zz"}"#).await;
    {
        let l = lines.lock().unwrap();
        assert_eq!(l.len(), 4);
        assert_eq!(l[0].procedure, "/crud.v1.EntityService/List");
        assert!(l[0].error.is_none());
        assert_eq!(l[3].procedure, "/crud.v1.EntityService/Get");
        assert!(
            l[3].error.as_deref().unwrap().contains("not_found"),
            "{:?}",
            l[3]
        );
        assert!(l[3]
            .line()
            .starts_with("rpc request procedure=/crud.v1.EntityService/Get duration="));
    }
    r.stop().await;
}

fn auth_func() -> AuthFunc {
    Arc::new(|c| match c.authorization {
        Some("Bearer good") => Ok(Claims {
            subject: "alice".into(),
            ..Default::default()
        }),
        Some(_) => Err(AuthnError::InvalidToken("bad token".into())),
        None => Err(AuthnError::NoToken(None)),
    })
}

#[tokio::test]
async fn authenticate_refuses_before_the_handler_in_every_protocol() {
    let store = Arc::new(Store::default());
    let refused: Arc<Mutex<Vec<String>>> = Arc::default();
    let seen = refused.clone();
    let r = start(server(&store).interceptor(
        authenticate(auth_func())
            .challenge(r#"Bearer resource_metadata="https://x.example/.well-known/oauth-protected-resource""#)
            .on_refused(move |call, e| seen.lock().unwrap().push(format!("{} {e}", call.procedure))),
    ))
    .await;

    // Connect/JSON: 401, the code in the body, the challenge in a header.
    let (status, head, body) = http1(&r.addr, "/crud.v1.EntityService/List", &[], "{}").await;
    assert_eq!(status, 401, "{body}");
    assert_eq!(
        serde_json::from_str::<Value>(&body).unwrap()["code"],
        "unauthenticated"
    );
    assert!(
        head.contains("www-authenticate: bearer resource_metadata="),
        "{head}"
    );

    // Every protocol: unauthenticated, and the handler never ran.
    for (p, label) in [
        (Protocol::Connect, "connect"),
        (Protocol::GrpcWeb, "grpc-web"),
    ] {
        let (_, c) = cfg(&r.addr, p);
        let cl = EntityServiceClient::new(HttpClient::plaintext(), c);
        let e = cl.list(ListRequest::default()).await.expect_err(label);
        assert_eq!(e.code, ErrorCode::Unauthenticated, "{label}: {e}");
    }
    let (uri, c) = cfg(&r.addr, Protocol::Grpc);
    let conn = Http2Connection::connect_plaintext(uri)
        .await
        .unwrap()
        .shared(8);
    let cl = EntityServiceClient::new(conn, c);
    let e = cl.list(ListRequest::default()).await.expect_err("grpc");
    assert_eq!(e.code, ErrorCode::Unauthenticated, "{e}");
    assert_eq!(
        store.calls.load(Ordering::SeqCst),
        0,
        "a refused call reached the handler"
    );
    assert_eq!(
        refused.lock().unwrap().len(),
        4,
        "{:?}",
        refused.lock().unwrap()
    );
    assert!(refused.lock().unwrap()[0].contains("/crud.v1.EntityService/List"));

    // A bad token is refused too; a good one carries its claims.
    let (status, _, _) = http1(
        &r.addr,
        "/crud.v1.EntityService/List",
        &[("Authorization", "Bearer nope")],
        "{}",
    )
    .await;
    assert_eq!(status, 401);
    let (status, _, body) = http1(
        &r.addr,
        "/crud.v1.EntityService/List",
        &[("Authorization", "Bearer good")],
        "{}",
    )
    .await;
    assert_eq!(status, 200, "{body}");
    assert_eq!(
        *store.last_claims.lock().unwrap(),
        Some((true, "alice".to_string()))
    );
    r.stop().await;
}

#[tokio::test]
async fn a_panicking_handler_is_internal_and_the_server_survives() {
    let store = Arc::new(Store::default());
    let panics: Arc<Mutex<Vec<String>>> = Arc::default();
    let sink = panics.clone();
    let r = start(server(&store).on_panic(move |m| sink.lock().unwrap().push(m.to_string()))).await;

    let (status, _, body) = http1(
        &r.addr,
        "/crud.v1.EntityService/Create",
        &[],
        r#"{"entity":{"name":"panic"}}"#,
    )
    .await;
    assert_eq!(status, 500, "{body}");
    assert_eq!(
        serde_json::from_str::<Value>(&body).unwrap()["code"],
        "internal"
    );
    assert!(!body.contains("kaboom"), "panic detail leaked: {body}");

    let (_, c) = cfg(&r.addr, Protocol::GrpcWeb);
    let cl = EntityServiceClient::new(HttpClient::plaintext(), c);
    let e = cl
        .create(CreateRequest {
            entity: strukt(json!({"name":"panic"})).into(),
            ..Default::default()
        })
        .await
        .expect_err("panic");
    assert_eq!(e.code, ErrorCode::Internal, "{e}");

    let (status, _, _) = http1(&r.addr, "/crud.v1.EntityService/List", &[], "{}").await;
    assert_eq!(status, 200, "the server died with the handler");
    assert!(panics.lock().unwrap().iter().any(|m| m.contains("kaboom")));
    r.stop().await;
}

#[tokio::test]
async fn request_body_is_bounded() {
    let store = Arc::new(Store::default());
    let r = start(server(&store).max_body_bytes(1024)).await;
    let big = format!(r#"{{"entity":{{"name":"{}"}}}}"#, "x".repeat(4096));
    let (status, _, body) = http1(&r.addr, "/crud.v1.EntityService/Create", &[], &big).await;
    assert!(
        status == 413 || status == 429 || status == 400,
        "{status} {body}"
    );
    assert_eq!(
        store.calls.load(Ordering::SeqCst),
        0,
        "an oversized body reached the handler"
    );
    let (status, _, _) = http1(&r.addr, "/crud.v1.EntityService/List", &[], "{}").await;
    assert_eq!(status, 200);
    r.stop().await;
}

#[tokio::test]
async fn stop_drains_and_the_port_closes() {
    let store = Arc::new(Store::default());
    let r = start(server(&store)).await;
    let addr = r.addr.clone();
    assert!(tokio::net::TcpStream::connect(&addr).await.is_ok());
    assert_eq!(r.stop().await, 0);
    assert!(
        tokio::net::TcpStream::connect(&addr).await.is_err(),
        "still listening after stop"
    );
}

// ---- streaming ----
//
// crud.v1 has no streaming procedure, so a server-streaming one is
// registered through the generated-stub seam, by hand, and spoken to in
// raw gRPC-Web frames: the wire is what is asserted.

struct Counter(AtomicUsize);
struct CounterMarker;

impl connectrpc::ServiceRegister<CounterMarker> for Arc<Counter> {
    fn register_service(self, router: connectrpc::Router) -> connectrpc::Router {
        router.route_server_stream(
            "test.v1.Counter",
            "Count",
            connectrpc::streaming_handler_fn(move |_ctx, _req: ListRequest| {
                let me = self.clone();
                async move {
                    me.0.fetch_add(1, Ordering::SeqCst);
                    connectrpc::Response::stream_ok(futures_util::stream::iter(vec![
                        Ok::<_, connectrpc::ConnectError>(ListResponse::default()),
                        Ok(ListResponse::default()),
                    ]))
                }
            }),
        )
    }
}

struct Stream(Arc<Counter>);

impl hop_top_kit::rpc::RpcService for Stream {
    fn mount(self: Arc<Self>, mount: &mut hop_top_kit::rpc::Mount) {
        mount.generated(self.0.clone());
    }
}

/// POST one empty gRPC-Web request and return the status, the response
/// headers and the grpc-status found in the headers or the trailer frame.
async fn grpc_web_stream(
    addr: &str,
    headers: &[(&str, &str)],
) -> (u16, String, Option<String>, usize) {
    let mut s = tokio::net::TcpStream::connect(addr).await.unwrap();
    let mut req = format!(
        "POST /test.v1.Counter/Count HTTP/1.1\r\nHost: {addr}\r\nConnection: close\r\nContent-Type: application/grpc-web+proto\r\nContent-Length: 5\r\n"
    );
    for (k, v) in headers {
        req.push_str(&format!("{k}: {v}\r\n"));
    }
    req.push_str("\r\n");
    let mut bytes = req.into_bytes();
    bytes.extend_from_slice(&[0, 0, 0, 0, 0]);
    s.write_all(&bytes).await.unwrap();
    let mut raw = Vec::new();
    tokio::time::timeout(Duration::from_secs(5), s.read_to_end(&mut raw))
        .await
        .expect("response")
        .unwrap();
    let split = raw.windows(4).position(|w| w == b"\r\n\r\n").unwrap();
    let head = String::from_utf8_lossy(&raw[..split]).to_lowercase();
    let status = head.split_whitespace().nth(1).unwrap().parse().unwrap();
    let mut body = raw[split + 4..].to_vec();
    if head.contains("transfer-encoding: chunked") {
        body = dechunk_bytes(&body);
    }
    // Frames: flag, u32 length, payload. A flag with the high bit is the
    // trailers frame.
    let (mut data_frames, mut trailer_status) = (0, None);
    let mut i = 0;
    while i + 5 <= body.len() {
        let n = u32::from_be_bytes(body[i + 1..i + 5].try_into().unwrap()) as usize;
        let payload = &body[i + 5..i + 5 + n];
        if body[i] & 0x80 != 0 {
            let t = String::from_utf8_lossy(payload).to_lowercase();
            trailer_status = t
                .lines()
                .find_map(|l| l.strip_prefix("grpc-status:").map(|v| v.trim().to_string()));
        } else {
            data_frames += 1;
        }
        i += 5 + n;
    }
    let header_status = head
        .lines()
        .find_map(|l| l.strip_prefix("grpc-status:").map(|v| v.trim().to_string()));
    (status, head, trailer_status.or(header_status), data_frames)
}

fn dechunk_bytes(mut rest: &[u8]) -> Vec<u8> {
    let mut out = Vec::new();
    while let Some(p) = rest.windows(2).position(|w| w == b"\r\n") {
        let n = usize::from_str_radix(String::from_utf8_lossy(&rest[..p]).trim(), 16).unwrap_or(0);
        if n == 0 {
            break;
        }
        out.extend_from_slice(&rest[p + 2..p + 2 + n]);
        rest = &rest[p + 2 + n + 2..];
    }
    out
}

#[tokio::test]
async fn authenticate_covers_streaming_calls_too() {
    let counter = Arc::new(Counter(AtomicUsize::new(0)));
    let r = start(
        RpcServer::builder("127.0.0.1:0")
            .service(Stream(counter.clone()))
            .interceptor(authenticate(auth_func())),
    )
    .await;

    // Refused: unauthenticated (16), no data, and the handler never ran.
    let (status, head, grpc, frames) = grpc_web_stream(&r.addr, &[]).await;
    assert_eq!(status, 200, "{head}");
    assert_eq!(grpc.as_deref(), Some("16"), "{head}");
    assert_eq!(frames, 0);
    assert_eq!(
        counter.0.load(Ordering::SeqCst),
        0,
        "a refused stream reached the handler"
    );

    // Admitted: both messages arrive, then status 0.
    let (status, head, grpc, frames) =
        grpc_web_stream(&r.addr, &[("Authorization", "Bearer good")]).await;
    assert_eq!(status, 200, "{head}");
    assert_eq!(grpc.as_deref(), Some("0"), "{head}");
    assert_eq!(frames, 2);
    assert_eq!(counter.0.load(Ordering::SeqCst), 1);
    r.stop().await;
}

#[tokio::test]
async fn native_grpc_panic_is_internal() {
    let store = Arc::new(Store::default());
    let r = start(server(&store)).await;
    let (uri, c) = cfg(&r.addr, Protocol::Grpc);
    let conn = Http2Connection::connect_plaintext(uri)
        .await
        .unwrap()
        .shared(8);
    let cl = EntityServiceClient::new(conn, c);
    let e = cl
        .create(CreateRequest {
            entity: strukt(json!({"name":"panic"})).into(),
            ..Default::default()
        })
        .await
        .expect_err("panic");
    assert_eq!(e.code, ErrorCode::Internal, "{e}");
    r.stop().await;
}
