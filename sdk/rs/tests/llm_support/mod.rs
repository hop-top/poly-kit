//! A loopback HTTP/1.1 server answering canned responses, for the llm
//! tests. Nothing leaves the machine: every test points the adapter at
//! this server. Each connection serves one request and closes.

#![allow(dead_code)]

use std::collections::HashMap;
use std::sync::atomic::{AtomicBool, AtomicUsize, Ordering};
use std::sync::{Arc, Mutex};
use std::time::Duration;

use tokio::io::{AsyncReadExt, AsyncWriteExt};
use tokio::net::TcpListener;

/// One canned response. `hang` keeps the connection open after the
/// body until the client closes it, which `Server::closed` reports.
#[derive(Clone, Debug, Default)]
pub struct Canned {
    pub status: u16,
    pub headers: Vec<(String, String)>,
    pub body: String,
    pub hang: bool,
}

impl Canned {
    pub fn json(status: u16, body: &str) -> Self {
        Canned {
            status,
            headers: vec![("content-type".into(), "application/json".into())],
            body: body.into(),
            hang: false,
        }
    }

    pub fn sse(body: &str) -> Self {
        Canned {
            status: 200,
            headers: vec![("content-type".into(), "text/event-stream".into())],
            body: body.into(),
            hang: false,
        }
    }
}

/// A request as the server received it. Header names are lower case.
#[derive(Clone, Debug)]
pub struct Captured {
    pub path: String,
    pub headers: HashMap<String, Vec<String>>,
    pub body: Vec<u8>,
}

impl Captured {
    pub fn header(&self, name: &str) -> Option<&str> {
        self.headers
            .get(name)
            .and_then(|v| v.first())
            .map(String::as_str)
    }

    pub fn json(&self) -> serde_json::Value {
        serde_json::from_slice(&self.body).expect("request body is JSON")
    }
}

pub struct Server {
    pub url: String,
    attempts: Arc<AtomicUsize>,
    requests: Arc<Mutex<Vec<Captured>>>,
    closed: Arc<AtomicBool>,
}

impl Server {
    pub fn attempts(&self) -> usize {
        self.attempts.load(Ordering::SeqCst)
    }

    pub fn requests(&self) -> Vec<Captured> {
        self.requests.lock().unwrap().clone()
    }

    pub fn only(&self) -> Captured {
        let reqs = self.requests();
        assert_eq!(reqs.len(), 1, "want exactly one request");
        reqs.into_iter().next().unwrap()
    }

    /// Whether the client closed a hanging connection.
    pub fn closed(&self) -> bool {
        self.closed.load(Ordering::SeqCst)
    }
}

/// Serve `responses` in order; the last one repeats.
pub async fn serve(responses: Vec<Canned>) -> Server {
    assert!(!responses.is_empty());
    let listener = TcpListener::bind("127.0.0.1:0").await.unwrap();
    let url = format!("http://{}", listener.local_addr().unwrap());
    let attempts = Arc::new(AtomicUsize::new(0));
    let requests = Arc::new(Mutex::new(Vec::new()));
    let closed = Arc::new(AtomicBool::new(false));
    let (a, r, c) = (attempts.clone(), requests.clone(), closed.clone());
    let responses = Arc::new(responses);
    tokio::spawn(async move {
        loop {
            let Ok((mut sock, _)) = listener.accept().await else {
                return;
            };
            let (a, r, c, responses) = (a.clone(), r.clone(), c.clone(), responses.clone());
            tokio::spawn(async move {
                let Some(req) = read_request(&mut sock).await else {
                    return;
                };
                let n = a.fetch_add(1, Ordering::SeqCst);
                r.lock().unwrap().push(req);
                let canned = &responses[n.min(responses.len() - 1)];
                let mut head = format!("HTTP/1.1 {} X\r\n", canned.status);
                for (k, v) in &canned.headers {
                    head.push_str(&format!("{k}: {v}\r\n"));
                }
                if canned.hang {
                    head.push_str("transfer-encoding: chunked\r\n\r\n");
                    let chunk = format!("{:x}\r\n{}\r\n", canned.body.len(), canned.body);
                    let _ = sock.write_all(head.as_bytes()).await;
                    let _ = sock.write_all(chunk.as_bytes()).await;
                    let _ = sock.flush().await;
                    let mut buf = [0u8; 64];
                    loop {
                        match sock.read(&mut buf).await {
                            Ok(0) | Err(_) => {
                                c.store(true, Ordering::SeqCst);
                                return;
                            }
                            Ok(_) => {}
                        }
                    }
                }
                head.push_str(&format!(
                    "content-length: {}\r\nconnection: close\r\n\r\n",
                    canned.body.len()
                ));
                let _ = sock.write_all(head.as_bytes()).await;
                let _ = sock.write_all(canned.body.as_bytes()).await;
                let _ = sock.shutdown().await;
            });
        }
    });
    Server {
        url,
        attempts,
        requests,
        closed,
    }
}

async fn read_request(sock: &mut tokio::net::TcpStream) -> Option<Captured> {
    let mut buf = Vec::new();
    let mut tmp = [0u8; 4096];
    let head_end = loop {
        let n = sock.read(&mut tmp).await.ok()?;
        if n == 0 {
            return None;
        }
        buf.extend_from_slice(&tmp[..n]);
        if let Some(i) = buf.windows(4).position(|w| w == b"\r\n\r\n") {
            break i + 4;
        }
    };
    let head = String::from_utf8_lossy(&buf[..head_end]).to_string();
    let mut lines = head.split("\r\n");
    let path = lines
        .next()?
        .split(' ')
        .nth(1)
        .unwrap_or_default()
        .to_string();
    let mut headers: HashMap<String, Vec<String>> = HashMap::new();
    for line in lines.filter(|l| !l.is_empty()) {
        if let Some((k, v)) = line.split_once(':') {
            headers
                .entry(k.trim().to_ascii_lowercase())
                .or_default()
                .push(v.trim().to_string());
        }
    }
    let len: usize = headers
        .get("content-length")
        .and_then(|v| v.first())
        .and_then(|v| v.parse().ok())
        .unwrap_or(0);
    let mut body = buf[head_end..].to_vec();
    while body.len() < len {
        let n = sock.read(&mut tmp).await.ok()?;
        if n == 0 {
            break;
        }
        body.extend_from_slice(&tmp[..n]);
    }
    Some(Captured {
        path,
        headers,
        body,
    })
}

/// Poll `cond` for up to two seconds.
pub async fn eventually(cond: impl Fn() -> bool) -> bool {
    for _ in 0..200 {
        if cond() {
            return true;
        }
        tokio::time::sleep(Duration::from_millis(10)).await;
    }
    cond()
}
