//! The default [`Fetch`]: kit's guarded HTTP client. The only module
//! of `authn` that names an HTTP or async-runtime crate.
//!
//! `KeySource::lookup` is synchronous, so a fetch blocks its caller as
//! Go's does. The request runs on a private current-thread runtime,
//! driven from a scoped helper thread, so `lookup` works from plain
//! threads, a multi-thread runtime's workers and a current-thread
//! runtime alike (none of which `block_on` allows directly). Redirects
//! are never followed: a key set or discovery document that redirects
//! is an error, not a hop to another host the policy never saw.

use std::time::Duration;

use super::{Fetch, MAX_DOCUMENT_BYTES};
use crate::authn::error::AuthnError;
use crate::netpolicy::{GuardedClient, NetPolicy};

/// Fetches documents through a [`GuardedClient`].
pub struct HttpFetch {
    client: GuardedClient,
    rt: Option<tokio::runtime::Runtime>,
}

impl HttpFetch {
    /// A fetcher under `policy` that gives up on a request after
    /// `timeout` and follows no redirect.
    ///
    /// # Errors
    ///
    /// [`AuthnError::Config`] when the client or its runtime cannot be
    /// built.
    pub fn new(policy: NetPolicy, timeout: Duration) -> Result<Self, AuthnError> {
        let client = GuardedClient::build(
            policy,
            reqwest::Client::builder()
                .redirect(reqwest::redirect::Policy::none())
                .timeout(timeout)
                // One fetch an hour: a pooled connection would sit idle
                // on a runtime that is not running between fetches.
                .pool_max_idle_per_host(0),
            crate::netpolicy::Redirects::None,
            reqwest::header::HeaderMap::new(),
        )
        .map_err(|e| AuthnError::Config(format!("http client: {e}")))?;
        let rt = tokio::runtime::Builder::new_current_thread()
            .enable_all()
            .build()
            .map_err(|e| AuthnError::Config(format!("fetch runtime: {e}")))?;
        Ok(Self {
            client,
            rt: Some(rt),
        })
    }

    async fn get_async(&self, url: &str) -> Result<Vec<u8>, String> {
        let mut resp = self
            .client
            .get(url)
            .header("Accept", "application/json")
            .send()
            .await
            .map_err(|e| e.to_string())?;
        if resp.status() != reqwest::StatusCode::OK {
            return Err(format!("GET {url}: status {}", resp.status().as_u16()));
        }
        let too_big = || format!("GET {url}: document larger than {MAX_DOCUMENT_BYTES} bytes");
        if resp
            .content_length()
            .is_some_and(|n| n > MAX_DOCUMENT_BYTES as u64)
        {
            return Err(too_big());
        }
        let mut body = Vec::new();
        while let Some(chunk) = resp.chunk().await.map_err(|e| format!("GET {url}: {e}"))? {
            if body.len() + chunk.len() > MAX_DOCUMENT_BYTES {
                return Err(too_big());
            }
            body.extend_from_slice(&chunk);
        }
        Ok(body)
    }
}

impl Fetch for HttpFetch {
    fn get(&self, url: &str) -> Result<Vec<u8>, String> {
        let rt = self.rt.as_ref().ok_or("fetcher closed")?;
        std::thread::scope(|s| {
            std::thread::Builder::new()
                .name("authn-fetch".into())
                .spawn_scoped(s, || rt.block_on(self.get_async(url)))
                .map_err(|e| format!("GET {url}: {e}"))?
                .join()
                .map_err(|_| format!("GET {url}: fetch panicked"))?
        })
    }
}

impl Drop for HttpFetch {
    fn drop(&mut self) {
        // Dropping a runtime inside an async context panics; this does
        // not wait.
        if let Some(rt) = self.rt.take() {
            rt.shutdown_background();
        }
    }
}
