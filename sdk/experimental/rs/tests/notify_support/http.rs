//! A loopback HTTP server that records requests: enough HTTP/1.1 for a
//! sink's POST, and no more.

#![allow(dead_code)]

use std::io::{Read, Write};
use std::net::{TcpListener, TcpStream};
use std::sync::{Arc, Mutex, PoisonError};
use std::time::Duration;

#[derive(Clone, Debug)]
pub struct Req {
    pub method: String,
    pub path: String,
    pub headers: Vec<(String, String)>,
    pub body: Vec<u8>,
}

impl Req {
    /// Every value of a header, in order; the name is case-insensitive.
    pub fn header_values(&self, name: &str) -> Vec<String> {
        self.headers
            .iter()
            .filter(|(k, _)| k.eq_ignore_ascii_case(name))
            .map(|(_, v)| v.clone())
            .collect()
    }

    pub fn header(&self, name: &str) -> Option<String> {
        self.header_values(name).into_iter().next()
    }

    pub fn json(&self) -> serde_json::Value {
        serde_json::from_slice(&self.body).expect("body is JSON")
    }
}

pub struct Server {
    pub url: String,
    pub addr: std::net::SocketAddr,
    requests: Arc<Mutex<Vec<Req>>>,
}

impl Server {
    /// Answers every request `status` with `body` after `delay`.
    pub fn start(status: u16, body: impl Into<Vec<u8>>, delay: Duration) -> Server {
        Self::start_raw(delay, status, Vec::new(), body.into())
    }

    /// Answers `status` with extra response `headers`.
    pub fn start_raw(
        delay: Duration,
        status: u16,
        headers: Vec<(String, String)>,
        body: Vec<u8>,
    ) -> Server {
        let ln = TcpListener::bind("127.0.0.1:0").expect("bind");
        let addr = ln.local_addr().unwrap();
        let requests = Arc::new(Mutex::new(Vec::new()));
        let rec = Arc::clone(&requests);
        std::thread::spawn(move || {
            for conn in ln.incoming() {
                let Ok(conn) = conn else { return };
                let rec = Arc::clone(&rec);
                let headers = headers.clone();
                let body = body.clone();
                std::thread::spawn(move || {
                    serve(conn, &rec, delay, status, &headers, &body);
                });
            }
        });
        Server {
            url: format!("http://{addr}"),
            addr,
            requests,
        }
    }

    pub fn ok() -> Server {
        Self::start(200, "", Duration::ZERO)
    }

    pub fn requests(&self) -> Vec<Req> {
        self.requests
            .lock()
            .unwrap_or_else(PoisonError::into_inner)
            .clone()
    }

    pub fn hits(&self) -> usize {
        self.requests().len()
    }

    /// The only request received.
    pub fn one(&self) -> Req {
        let mut r = self.requests();
        assert_eq!(r.len(), 1, "want exactly one request, got {}", r.len());
        r.remove(0)
    }
}

fn serve(
    mut conn: TcpStream,
    rec: &Mutex<Vec<Req>>,
    delay: Duration,
    status: u16,
    headers: &[(String, String)],
    body: &[u8],
) {
    let mut buf = Vec::new();
    let mut chunk = [0u8; 4096];
    let head_end = loop {
        if let Some(i) = buf.windows(4).position(|w| w == b"\r\n\r\n") {
            break i + 4;
        }
        match conn.read(&mut chunk) {
            Ok(0) | Err(_) => return,
            Ok(n) => buf.extend_from_slice(&chunk[..n]),
        }
    };
    let head = String::from_utf8_lossy(&buf[..head_end]).to_string();
    let mut lines = head.split("\r\n");
    let mut first = lines.next().unwrap_or("").split(' ');
    let method = first.next().unwrap_or("").to_string();
    let path = first.next().unwrap_or("").to_string();
    let hdrs: Vec<(String, String)> = lines
        .filter_map(|l| l.split_once(':'))
        .map(|(k, v)| (k.trim().to_string(), v.trim().to_string()))
        .collect();
    let want = hdrs
        .iter()
        .find(|(k, _)| k.eq_ignore_ascii_case("content-length"))
        .and_then(|(_, v)| v.parse::<usize>().ok())
        .unwrap_or(0);
    let mut body_in = buf[head_end..].to_vec();
    while body_in.len() < want {
        match conn.read(&mut chunk) {
            Ok(0) | Err(_) => break,
            Ok(n) => body_in.extend_from_slice(&chunk[..n]),
        }
    }
    rec.lock()
        .unwrap_or_else(PoisonError::into_inner)
        .push(Req {
            method,
            path,
            headers: hdrs,
            body: body_in,
        });
    std::thread::sleep(delay);
    let mut resp = format!(
        "HTTP/1.1 {status} X\r\nContent-Length: {}\r\nConnection: close\r\n",
        body.len()
    );
    for (k, v) in headers {
        resp.push_str(&format!("{k}: {v}\r\n"));
    }
    resp.push_str("\r\n");
    conn.write_all(resp.as_bytes()).ok();
    conn.write_all(body).ok();
    conn.flush().ok();
}
