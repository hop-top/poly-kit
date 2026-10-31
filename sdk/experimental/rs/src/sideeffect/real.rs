//! Production implementations of the seams. Each delegates to the
//! standard library or a kit primitive without instrumentation. The CLI
//! wires these by default; switching to dryrun or testfake happens at the
//! boundary, and command code never sees the swap.
//!
//! [`Http`] (feature `sideeffect-http`) goes through the netpolicy
//! guarded client, so `--offline` refuses it.

use std::io;
use std::path::Path;
use std::process::Stdio;
use std::sync::Arc;

use serde_json::Value;

use super::seams::{self as seams, Command, ExecError, PublishError};
use crate::bus::{Adapter, Context, Event};

/// The production [`Fs`](seams::Fs), over `std::fs`.
#[derive(Clone, Copy, Debug, Default)]
pub struct Fs;

impl seams::Fs for Fs {
    fn write_file(&self, path: &Path, data: &[u8], mode: u32) -> io::Result<()> {
        use std::io::Write;
        let mut opts = std::fs::OpenOptions::new();
        opts.write(true).create(true).truncate(true);
        #[cfg(unix)]
        {
            use std::os::unix::fs::OpenOptionsExt;
            opts.mode(mode);
        }
        #[cfg(not(unix))]
        let _ = mode;
        opts.open(path)?.write_all(data)
    }

    fn mkdir_all(&self, path: &Path, mode: u32) -> io::Result<()> {
        let mut b = std::fs::DirBuilder::new();
        b.recursive(true);
        #[cfg(unix)]
        {
            use std::os::unix::fs::DirBuilderExt;
            b.mode(mode);
        }
        #[cfg(not(unix))]
        let _ = mode;
        b.create(path)
    }

    fn rename(&self, from: &Path, to: &Path) -> io::Result<()> {
        std::fs::rename(from, to)
    }

    fn remove(&self, path: &Path) -> io::Result<()> {
        // Go's os.Remove: unlink a file, else rmdir a directory.
        if std::fs::symlink_metadata(path)?.is_dir() {
            std::fs::remove_dir(path)
        } else {
            std::fs::remove_file(path)
        }
    }
}

/// The production [`Bus`](seams::Bus): publishes on a kit bus
/// [`Adapter`]. The default has none, and every publish then returns
/// [`PublishError::NilPublisher`], a clear error instead of a panic
/// during a partial bootstrap.
#[derive(Clone, Default)]
pub struct Bus {
    adapter: Option<Arc<dyn Adapter>>,
}

impl Bus {
    pub fn new(adapter: Arc<dyn Adapter>) -> Self {
        Bus {
            adapter: Some(adapter),
        }
    }
}

impl seams::Bus for Bus {
    fn publish(
        &self,
        ctx: &Context,
        topic: &str,
        source: &str,
        payload: Value,
    ) -> Result<(), PublishError> {
        let adapter = self.adapter.as_ref().ok_or(PublishError::NilPublisher)?;
        Ok(adapter.publish(ctx, &Event::new(topic, source, payload))?)
    }
}

/// The production [`Exec`](seams::Exec), over `std::process`. As in Go,
/// the child's stdin and stdout (for `run`) are the null device.
#[derive(Clone, Copy, Debug, Default)]
pub struct Exec;

fn build(cmd: &Command) -> std::process::Command {
    let mut c = std::process::Command::new(&cmd.program);
    c.args(&cmd.args).envs(cmd.env.iter().cloned());
    if let Some(dir) = &cmd.current_dir {
        c.current_dir(dir);
    }
    c.stdin(Stdio::null());
    c
}

fn spawn_error(e: io::Error) -> ExecError {
    ExecError::Spawn {
        kind: e.kind(),
        message: e.to_string(),
    }
}

impl seams::Exec for Exec {
    fn run(&self, cmd: &Command) -> Result<(), ExecError> {
        let status = build(cmd)
            .stdout(Stdio::null())
            .stderr(Stdio::null())
            .status()
            .map_err(spawn_error)?;
        if status.success() {
            return Ok(());
        }
        Err(ExecError::Exit {
            code: status.code(),
            stderr: Vec::new(),
        })
    }

    fn output(&self, cmd: &Command) -> Result<Vec<u8>, ExecError> {
        let out = build(cmd).output().map_err(spawn_error)?;
        if out.status.success() {
            return Ok(out.stdout);
        }
        Err(ExecError::Exit {
            code: out.status.code(),
            stderr: out.stderr,
        })
    }
}

#[cfg(feature = "sideeffect-http")]
pub use self::http::Http;

#[cfg(feature = "sideeffect-http")]
mod http {
    use crate::netpolicy::{GuardedClient, NetError, NetPolicy};
    use crate::sideeffect::seams::{self, BoxFuture, HttpError, Request, Response};

    /// The production [`Http`](seams::Http), over the netpolicy guarded
    /// client: under an offline policy a request off loopback is refused
    /// ([`HttpError::is_offline`]). Redirects, proxies and timeouts are
    /// reqwest's defaults; build the client yourself for others.
    #[derive(Clone, Debug)]
    pub struct Http {
        client: GuardedClient,
    }

    impl Http {
        /// An online client.
        pub fn new() -> Result<Self, reqwest::Error> {
            Self::with_policy(NetPolicy::default())
        }

        /// A client under `policy`.
        pub fn with_policy(policy: NetPolicy) -> Result<Self, reqwest::Error> {
            Ok(Self::with_client(GuardedClient::new(policy)?))
        }

        /// Uses an already configured guarded client.
        pub fn with_client(client: GuardedClient) -> Self {
            Http { client }
        }
    }

    fn error(e: NetError) -> HttpError {
        match e {
            NetError::Offline(o) => HttpError::offline(o.to_string()),
            NetError::Transport(t) => HttpError::new(t.to_string()),
        }
    }

    impl seams::Http for Http {
        fn send(&self, req: Request) -> BoxFuture<'_, Result<Response, HttpError>> {
            Box::pin(async move {
                // An empty method is GET, as in net/http.
                let method = if req.method.is_empty() {
                    "GET"
                } else {
                    &req.method
                };
                let method = reqwest::Method::from_bytes(method.as_bytes())
                    .map_err(|e| HttpError::new(format!("invalid method {method:?}: {e}")))?;
                let mut b = self.client.request(method, req.url.as_str());
                for (k, v) in &req.headers {
                    b = b.header(k, v);
                }
                if !req.body.is_empty() {
                    b = b.body(req.body);
                }
                let resp = b.send().await.map_err(error)?;
                let status = resp.status().as_u16();
                let headers = resp
                    .headers()
                    .iter()
                    .filter_map(|(k, v)| Some((k.to_string(), v.to_str().ok()?.to_string())))
                    .collect();
                // The body error carries the full URL (query included),
                // which the guard documents callers must not surface.
                let body = resp.bytes().await.map_err(|e| {
                    HttpError::new(format!("read response body: {}", e.without_url()))
                })?;
                Ok(Response {
                    status,
                    headers,
                    body: body.to_vec(),
                })
            })
        }
    }
}
