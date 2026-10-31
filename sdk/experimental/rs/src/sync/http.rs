//! The HTTP transport: diffs as JSON over `/sync/push`, `/sync/pull` and
//! `/sync/ping`. Go's transport_http.go.
//!
//! Every request goes through the netpolicy [`GuardedClient`] the `api`
//! client is built on, so `--offline` refuses a remote host here too.
//!
//! # Where this differs from Go
//!
//! - [`Transport`] is blocking. The sync methods drive reqwest on a
//!   small current-thread runtime of the transport's own, built on first
//!   use, and so panic-free but refused (with an error) when called from
//!   inside an async runtime: use [`HttpTransport::push_async`],
//!   [`pull_async`](HttpTransport::pull_async) and
//!   [`ping_async`](HttpTransport::ping_async) there.
//! - The 30 second client timeout is reqwest's; the blocking methods do
//!   not watch the `Context` mid-request, only before it.
//! - A trailing `/` on the base URL is dropped.

use std::future::Future;
use std::sync::{Mutex, MutexGuard};
use std::time::Duration;

use reqwest::Method;

use crate::bus::Context;
use crate::netpolicy::{GuardedClient, NetPolicy, RequestBuilder};

use super::clock::Timestamp;
use super::diff::Diff;
use super::remote::Transport;
use super::transport::TransportError;

const TIMEOUT: Duration = Duration::from_secs(30);

/// Pushes and pulls diffs over HTTP. Go's `HTTPTransport`.
pub struct HttpTransport {
    base_url: String,
    client: GuardedClient,
    auth_token: Option<String>,
    runtime: Mutex<Option<tokio::runtime::Runtime>>,
}

impl HttpTransport {
    /// A transport for the peer at `base_url`, with network access
    /// permitted.
    ///
    /// # Panics
    ///
    /// If the TLS backend cannot be initialised, like
    /// `reqwest::Client::new`; [`HttpTransport::with_policy`] reports it.
    pub fn new(base_url: &str) -> Self {
        Self::with_policy(base_url, NetPolicy::default()).expect("build http client")
    }

    /// A transport under an explicit network policy: what the CLI layer
    /// calls once it registers `--offline`.
    pub fn with_policy(base_url: &str, policy: NetPolicy) -> Result<Self, TransportError> {
        let client = GuardedClient::build(policy, reqwest::Client::builder().timeout(TIMEOUT))
            .map_err(|e| TransportError::new(format!("create client: {}", e.without_url())))?;
        Ok(HttpTransport {
            base_url: base_url.trim_end_matches('/').to_string(),
            client,
            auth_token: None,
            runtime: Mutex::new(None),
        })
    }

    /// Sets the bearer token sent with every request. Go's
    /// `WithAuthToken`.
    pub fn with_auth_token(mut self, token: &str) -> Self {
        self.auth_token = Some(token.to_string());
        self
    }

    fn request(&self, method: Method, path: &str) -> RequestBuilder {
        let req = self
            .client
            .request(method, format!("{}{path}", self.base_url));
        match &self.auth_token {
            Some(t) => req.bearer_auth(t),
            None => req,
        }
    }

    /// POSTs `diffs` to `/sync/push`.
    pub async fn push_async(&self, diffs: &[Diff]) -> Result<(), TransportError> {
        let body = serde_json::to_vec(diffs)
            .map_err(|e| TransportError::new(format!("marshal diffs: {e}")))?;
        let resp = self
            .request(Method::POST, "/sync/push")
            .header("Content-Type", "application/json")
            .body(body)
            .send()
            .await
            .map_err(|e| TransportError::new(format!("push: {e}")))?;
        check("push", resp.status())
    }

    /// GETs the diffs after `since` from `/sync/pull`, the watermark in
    /// `since_physical`, `since_logical` and `since_node`.
    pub async fn pull_async(&self, since: &Timestamp) -> Result<Vec<Diff>, TransportError> {
        let resp = self
            .request(Method::GET, "/sync/pull")
            .query(&[
                ("since_physical", since.physical.to_string()),
                ("since_logical", since.logical.to_string()),
                ("since_node", since.node_id.clone()),
            ])
            .send()
            .await
            .map_err(|e| TransportError::new(format!("pull: {e}")))?;
        check("pull", resp.status())?;
        let body = resp
            .bytes()
            .await
            .map_err(|e| TransportError::new(format!("decode diffs: {}", e.without_url())))?;
        let diffs: Option<Vec<Diff>> = serde_json::from_slice(&body)
            .map_err(|e| TransportError::new(format!("decode diffs: {e}")))?;
        Ok(diffs.unwrap_or_default())
    }

    /// GETs `/sync/ping`.
    pub async fn ping_async(&self) -> Result<(), TransportError> {
        let resp = self
            .request(Method::GET, "/sync/ping")
            .send()
            .await
            .map_err(|e| TransportError::new(e.to_string()))?;
        check("ping", resp.status())
    }

    fn runtime(&self) -> MutexGuard<'_, Option<tokio::runtime::Runtime>> {
        self.runtime.lock().unwrap_or_else(|e| e.into_inner())
    }

    /// Runs `fut` to completion on this transport's runtime.
    fn block<R>(
        &self,
        ctx: &Context,
        fut: impl Future<Output = Result<R, TransportError>>,
    ) -> Result<R, TransportError> {
        if let Some(e) = ctx.err() {
            return Err(TransportError::new(e.to_string()));
        }
        if tokio::runtime::Handle::try_current().is_ok() {
            return Err(TransportError::new(
                "sync: blocking transport call inside an async runtime; use the _async methods",
            ));
        }
        let mut slot = self.runtime();
        if slot.is_none() {
            let rt = tokio::runtime::Builder::new_current_thread()
                .enable_all()
                .build()
                .map_err(|e| TransportError::new(format!("start runtime: {e}")))?;
            *slot = Some(rt);
        }
        slot.as_ref().map_or_else(
            || Err(TransportError::new("sync: runtime unavailable")),
            |rt| rt.block_on(fut),
        )
    }
}

impl Drop for HttpTransport {
    fn drop(&mut self) {
        // Dropping a runtime inside async code panics; this never blocks.
        if let Some(rt) = self.runtime().take() {
            rt.shutdown_background();
        }
    }
}

fn check(what: &str, status: reqwest::StatusCode) -> Result<(), TransportError> {
    if status.as_u16() == 200 {
        Ok(())
    } else {
        Err(TransportError::new(format!(
            "{what}: status {}",
            status.as_u16()
        )))
    }
}

impl Transport for HttpTransport {
    fn push(&self, ctx: &Context, diffs: &[Diff]) -> Result<(), TransportError> {
        self.block(ctx, self.push_async(diffs))
    }

    fn pull(&self, ctx: &Context, since: &Timestamp) -> Result<Vec<Diff>, TransportError> {
        self.block(ctx, self.pull_async(since))
    }

    fn ping(&self, ctx: &Context) -> Result<(), TransportError> {
        self.block(ctx, self.ping_async())
    }
}
