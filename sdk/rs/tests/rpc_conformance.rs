//! Runs the shared corpus in `contracts/rpc-v1` against the Rust rpc
//! server. The vectors are the source of truth: a failure means this
//! port drifted from the contract (Go is canonical), not that a vector
//! should be edited to fit.
//!
//! Strict: unknown fields fail, `version` must be `v1`, and the corpus
//! directory must match [`FILES`] exactly.
#![cfg(feature = "rpc-server")]

use std::collections::HashMap;
use std::fs;
use std::path::PathBuf;
use std::sync::atomic::{AtomicUsize, Ordering};
use std::sync::{Arc, Mutex};
use std::time::Duration;

use hop_top_kit::authn::{AuthnError, Claims};
use hop_top_kit::rpc::proto::crud::v1::{
    CreateRequest, CreateResponse, DeleteRequest, DeleteResponse, GetRequest, GetResponse,
    ListRequest, ListResponse, UpdateRequest, UpdateResponse,
};
use hop_top_kit::rpc::{
    authenticate, request_id, EntityRpc, EntityService, RequestContext, RpcError, RpcServer,
};
use hop_top_kit::serve::{
    resolve, CancelToken, ResolveRequest, Service, ServiceConfig, ServiceConfigs, ServiceRegistry,
    Supervisor, SupervisorOptions,
};
use serde::Deserialize;
use serde_json::{json, Value};
use tokio::io::{AsyncReadExt, AsyncWriteExt};

const FILES: &[&str] = &["vectors.json"];

#[derive(Deserialize)]
#[serde(deny_unknown_fields)]
struct Step {
    name: String,
    procedure: String,
    #[serde(default)]
    request: Option<Value>,
    #[serde(default)]
    raw: Option<String>,
    #[serde(default)]
    headers: HashMap<String, String>,
    status: u16,
    #[serde(default)]
    response: Option<Value>,
    #[serde(default)]
    code_only: Option<String>,
}

#[derive(Deserialize)]
#[serde(deny_unknown_fields)]
struct Auth {
    token: String,
}

#[derive(Deserialize)]
#[serde(deny_unknown_fields)]
struct Group {
    name: String,
    #[serde(default)]
    auth: Option<Auth>,
    steps: Vec<Step>,
}

#[derive(Deserialize)]
#[serde(deny_unknown_fields)]
struct Vectors {
    version: String,
    #[allow(dead_code)]
    description: String,
    #[serde(rename = "$schema")]
    #[allow(dead_code)]
    schema: String,
    #[serde(rename = "$id")]
    #[allow(dead_code)]
    id: String,
    groups: Vec<Group>,
}

fn load() -> Vectors {
    let dir = PathBuf::from(env!("CARGO_MANIFEST_DIR")).join("../../contracts/rpc-v1");
    let mut found: Vec<String> = fs::read_dir(&dir)
        .expect("read corpus dir")
        .map(|e| e.expect("dir entry").file_name().into_string().unwrap())
        .filter(|n| n.ends_with(".json"))
        .collect();
    found.sort();
    assert_eq!(found, FILES, "corpus files without a runner");
    let raw = fs::read_to_string(dir.join("vectors.json")).expect("read vectors.json");
    let v: Vectors = serde_json::from_str(&raw).expect("parse vectors.json");
    assert_eq!(v.version, "v1");
    v
}

type Struct = buffa_types::google::protobuf::Struct;

fn strukt(v: Value) -> Struct {
    serde_json::from_value(v).expect("struct")
}

fn json_of(s: &Struct) -> Value {
    serde_json::to_value(s).expect("json")
}

/// The fixture handler the corpus describes.
#[derive(Default)]
struct Entities {
    rows: Mutex<HashMap<String, Value>>,
    seq: AtomicUsize,
}

impl EntityService for Entities {
    async fn create(
        &self,
        _: &RequestContext,
        req: CreateRequest,
    ) -> Result<CreateResponse, RpcError> {
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
    async fn get(&self, _: &RequestContext, req: GetRequest) -> Result<GetResponse, RpcError> {
        match self.rows.lock().unwrap().get(&req.id) {
            Some(v) => Ok(GetResponse {
                entity: strukt(v.clone()).into(),
                ..Default::default()
            }),
            None => Err(RpcError::not_found(format!("entity {} not found", req.id))),
        }
    }
    async fn list(&self, _: &RequestContext, _: ListRequest) -> Result<ListResponse, RpcError> {
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
        _: &RequestContext,
        req: UpdateRequest,
    ) -> Result<UpdateResponse, RpcError> {
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
        _: &RequestContext,
        req: DeleteRequest,
    ) -> Result<DeleteResponse, RpcError> {
        self.rows.lock().unwrap().remove(&req.id);
        Ok(DeleteResponse::default())
    }
}

async fn serve(g: &Group) -> (String, CancelToken, tokio::task::JoinHandle<i32>) {
    let mut b = RpcServer::builder("127.0.0.1:0")
        .service(EntityRpc::new(Entities::default()))
        .interceptor(request_id());
    if let Some(a) = &g.auth {
        let want = format!("Bearer {}", a.token);
        b = b.interceptor(authenticate(Arc::new(move |c| {
            if c.authorization == Some(want.as_str()) {
                Ok(Claims {
                    subject: "alice".into(),
                    ..Default::default()
                })
            } else {
                Err(AuthnError::InvalidToken("bad credentials".into()))
            }
        })));
    }
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
    let sd = shutdown.clone();
    let sup = Supervisor::new(reg, SupervisorOptions::default());
    let done = tokio::spawn(async move { sup.run(sd, &out.selected, &cfgs).await.exit_code });
    for _ in 0..300 {
        if svc.ready() {
            break;
        }
        tokio::time::sleep(Duration::from_millis(10)).await;
    }
    (svc.addr().expect("bound"), shutdown, done)
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

async fn post(
    addr: &str,
    path: &str,
    headers: &HashMap<String, String>,
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

#[tokio::test]
async fn connect_json_vectors() {
    let v = load();
    assert!(!v.groups.is_empty(), "groups is empty");
    for g in &v.groups {
        let (addr, shutdown, done) = serve(g).await;
        for s in &g.steps {
            let body = match (&s.raw, &s.request) {
                (Some(r), _) => r.clone(),
                (None, Some(r)) => r.to_string(),
                (None, None) => panic!("{}/{}: no request", g.name, s.name),
            };
            let (status, head, got) = post(
                &addr,
                &format!("/crud.v1.EntityService/{}", s.procedure),
                &s.headers,
                &body,
            )
            .await;
            let at = format!("{}/{}", g.name, s.name);
            assert_eq!(status, s.status, "{at}: {got}");
            assert!(
                head.contains("content-type: application/json"),
                "{at}: {head}"
            );
            let got: Value = serde_json::from_str(&got)
                .unwrap_or_else(|e| panic!("{at}: not JSON ({e}): {got}"));
            match (&s.code_only, &s.response) {
                (Some(code), _) => assert_eq!(got["code"], *code, "{at}: {got}"),
                (None, Some(want)) => assert_eq!(&got, want, "{at}"),
                (None, None) => panic!("{at}: vector has no expectation"),
            }
        }
        shutdown.cancel();
        assert_eq!(done.await.unwrap(), 0);
    }
}
