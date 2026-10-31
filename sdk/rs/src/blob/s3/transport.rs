//! The one HTTP seam of the S3 backend.
//!
//! [`S3Store`](super::S3Store) builds a fully signed [`HttpRequest`] and
//! hands it to a [`Transport`]. The default, [`GuardedTransport`], issues it
//! through the netpolicy [`GuardedClient`], so `--offline` and the host
//! allowlist bind S3 traffic like any other user traffic. A test, or a
//! caller who needs a proxy or a record/replay layer, supplies its own via
//! [`S3Store::with_transport`](super::S3Store::with_transport).

use std::error::Error;
use std::fmt::Debug;
use std::future::Future;
use std::time::Duration;

use crate::netpolicy::{GuardedClient, NetPolicy, Redirects};

/// Boxed transport failure. For [`GuardedTransport`] it downcasts to
/// [`NetError`](crate::netpolicy::NetError), so a refusal stays matchable.
pub type TransportError = Box<dyn Error + Send + Sync>;

/// One outgoing HTTP request. Header names are lowercase.
#[derive(Debug, Clone, PartialEq, Eq)]
pub struct HttpRequest {
    /// `GET`, `PUT`, `HEAD` or `DELETE`.
    pub method: String,
    /// Absolute URL, query included.
    pub url: String,
    /// Headers to send, `Authorization` among them. `Host` is derived from
    /// the URL by the client and is not listed.
    pub headers: Vec<(String, String)>,
    /// Request body, possibly empty.
    pub body: Vec<u8>,
}

/// The response to an [`HttpRequest`]. Header names are lowercase.
#[derive(Debug, Clone, PartialEq, Eq)]
pub struct HttpResponse {
    /// HTTP status code.
    pub status: u16,
    /// Response headers.
    pub headers: Vec<(String, String)>,
    /// Response body, fully buffered.
    pub body: Vec<u8>,
}

impl HttpResponse {
    /// First header named `name` (lowercase), if any.
    pub fn header(&self, name: &str) -> Option<&str> {
        self.headers
            .iter()
            .find(|(k, _)| k == name)
            .map(|(_, v)| v.as_str())
    }
}

/// Sends one HTTP request and returns the whole response. A non-2xx status
/// is a normal [`HttpResponse`], not an error.
pub trait Transport: Send + Sync + Debug {
    /// Perform `req`.
    fn send(&self, req: HttpRequest) -> Result<HttpResponse, TransportError>;
}

const CONNECT_TIMEOUT: Duration = Duration::from_secs(10);
const REQUEST_TIMEOUT: Duration = Duration::from_secs(300);

/// [`Transport`] over the netpolicy [`GuardedClient`].
///
/// The blob [`Store`](crate::blob::Store) trait is synchronous and the
/// guarded client is async, so this owns a one-worker runtime and blocks on
/// it. Called from inside an async runtime it hands the wait to a helper
/// thread rather than panicking, but that parks the caller's thread: use a
/// blocking-capable context (`spawn_blocking`) from async code.
///
/// Redirects are not followed: S3 answers a wrong-region request with a 301
/// naming another host, and a signed request must not follow it.
#[derive(Debug)]
pub struct GuardedTransport {
    client: GuardedClient,
    rt: Option<tokio::runtime::Runtime>,
}

impl GuardedTransport {
    /// Build a transport that enforces `policy` on every request.
    pub fn new(policy: NetPolicy) -> Result<Self, super::super::BlobError> {
        let client = GuardedClient::build(
            policy,
            reqwest::Client::builder()
                .connect_timeout(CONNECT_TIMEOUT)
                .timeout(REQUEST_TIMEOUT),
            Redirects::None,
            reqwest::header::HeaderMap::new(),
        )
        .map_err(|e| super::super::BlobError::InvalidConfig(format!("http client: {e}")))?;
        let rt = tokio::runtime::Builder::new_multi_thread()
            .worker_threads(1)
            .thread_name("kit-blob-s3")
            .enable_all()
            .build()
            .map_err(|e| super::super::BlobError::io("start runtime", e))?;
        Ok(GuardedTransport {
            client,
            rt: Some(rt),
        })
    }

    /// The policy this transport enforces.
    pub fn policy(&self) -> NetPolicy {
        self.client.policy()
    }

    fn block_on<F>(&self, fut: F) -> F::Output
    where
        F: Future + Send,
        F::Output: Send,
    {
        let rt = self.rt.as_ref().expect("runtime present until drop");
        if tokio::runtime::Handle::try_current().is_err() {
            return rt.block_on(fut);
        }
        // Inside a runtime `block_on` panics; wait on another thread.
        std::thread::scope(|s| match s.spawn(|| rt.block_on(fut)).join() {
            Ok(v) => v,
            Err(p) => std::panic::resume_unwind(p),
        })
    }
}

impl Drop for GuardedTransport {
    fn drop(&mut self) {
        // A plain drop panics when run on an async worker thread.
        if let Some(rt) = self.rt.take() {
            rt.shutdown_background();
        }
    }
}

impl Transport for GuardedTransport {
    fn send(&self, req: HttpRequest) -> Result<HttpResponse, TransportError> {
        let method = reqwest::Method::from_bytes(req.method.as_bytes())?;
        self.block_on(async {
            let mut b = self.client.request(method, req.url.as_str());
            for (k, v) in &req.headers {
                b = b.header(k, v);
            }
            if !req.body.is_empty() {
                b = b.body(req.body);
            }
            let resp = b.send().await?;
            let status = resp.status().as_u16();
            let headers = resp
                .headers()
                .iter()
                .filter_map(|(k, v)| Some((k.as_str().to_string(), v.to_str().ok()?.to_string())))
                .collect();
            let body = resp
                .bytes()
                .await
                .map_err(reqwest::Error::without_url)?
                .to_vec();
            Ok(HttpResponse {
                status,
                headers,
                body,
            })
        })
    }
}
