//! Shared by the api-server integration tests: a real service on a
//! kernel-picked loopback port, run under the supervisor.
#![allow(dead_code)]

pub mod pki;

use std::sync::Arc;
use std::time::Duration;

use hop_top_kit::api_server::{ApiServerConfig, ApiService};
use hop_top_kit::serve::{
    CancelToken, RunResult, Service, ServiceConfig, ServiceConfigs, ServiceRegistry, Supervisor,
    SupervisorOptions,
};

pub struct Running {
    pub svc: Arc<ApiService>,
    pub addr: String,
    cancel: CancelToken,
    run: tokio::task::JoinHandle<RunResult>,
}

impl Running {
    pub async fn stop(self) -> RunResult {
        self.cancel.cancel();
        self.run.await.expect("supervisor task")
    }
}

/// A config for tests: loopback, quiet log.
pub fn config(routes: axum::Router) -> ApiServerConfig {
    let mut c = ApiServerConfig::new("127.0.0.1:0", routes);
    c.log = Some(Arc::new(|_| {}));
    c
}

/// One request to `/healthz`, so lazy first-use work (the access log's
/// redaction rules compile on first use) is behind the timing tests.
pub async fn warm(addr: &str) {
    use tokio::io::{AsyncReadExt, AsyncWriteExt};
    let mut s = tokio::net::TcpStream::connect(addr).await.expect("connect");
    s.write_all(b"GET /healthz HTTP/1.1\r\nhost: 127.0.0.1\r\nconnection: close\r\n\r\n")
        .await
        .expect("write");
    let mut b = Vec::new();
    let _ = s.read_to_end(&mut b).await;
}

pub async fn start(cfg: ApiServerConfig) -> Running {
    let svc = Arc::new(ApiService::build(cfg).expect("build"));
    let mut reg = ServiceRegistry::new();
    reg.register(svc.clone()).expect("register");
    let mut configs = ServiceConfigs::new();
    configs.insert("api".to_string(), ServiceConfig::enabled());
    let sup = Supervisor::new(reg, SupervisorOptions::default());
    let cancel = CancelToken::new();
    let c2 = cancel.clone();
    let run = tokio::spawn(async move { sup.run(c2, &["api".to_string()], &configs).await });
    for _ in 0..500 {
        if svc.ready() {
            let addr = svc.addr().expect("bound");
            warm(&addr).await;
            return Running {
                svc,
                addr,
                cancel,
                run,
            };
        }
        tokio::time::sleep(Duration::from_millis(10)).await;
    }
    panic!("the service never became ready");
}

/// A plain-HTTP GET, the raw response.
pub async fn plain_get(addr: &str, path: &str) -> String {
    use tokio::io::{AsyncReadExt, AsyncWriteExt};
    let mut s = tokio::net::TcpStream::connect(addr).await.expect("connect");
    s.write_all(
        format!("GET {path} HTTP/1.1\r\nhost: {addr}\r\nconnection: close\r\n\r\n").as_bytes(),
    )
    .await
    .expect("write");
    let mut b = Vec::new();
    s.read_to_end(&mut b).await.expect("read");
    String::from_utf8_lossy(&b).into_owned()
}
