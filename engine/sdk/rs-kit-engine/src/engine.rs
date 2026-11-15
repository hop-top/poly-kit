use std::marker::PhantomData;
use std::path::PathBuf;
use std::process::Stdio;
use std::time::Duration;

use serde::de::DeserializeOwned;
use serde::{Deserialize, Serialize};
use tokio::io::{AsyncBufReadExt, BufReader};
use tokio::process::{Child, Command};

use crate::collection::Collection;
use crate::error::{bearer, check, url};
use crate::identity::IdentityClient;
use crate::peers::PeerClient;
use crate::sync::SyncClient;
use crate::EngineError;

/// How to launch `kit serve`.
#[derive(Debug, Clone)]
pub struct KitEngineOptions {
    pub app: Option<String>,
    pub data: Option<String>,
    /// 0 lets the OS pick.
    pub port: u16,
    pub daemon: bool,
    pub encrypt: bool,
    pub no_peer: bool,
    pub no_sync: bool,
    /// `kit` binary; defaults to `kit` on PATH.
    pub bin_path: Option<PathBuf>,
    /// Max wait for the startup line.
    pub startup_timeout: Duration,
    /// Max wait for the child to exit after `/shutdown`, before kill.
    pub stop_timeout: Duration,
}

impl Default for KitEngineOptions {
    fn default() -> Self {
        Self {
            app: None,
            data: None,
            port: 0,
            daemon: false,
            encrypt: false,
            no_peer: false,
            no_sync: false,
            bin_path: None,
            startup_timeout: Duration::from_secs(30),
            stop_timeout: Duration::from_secs(10),
        }
    }
}

/// First line of `kit serve` stdout.
#[derive(Debug, Deserialize)]
struct Startup {
    port: u16,
    pid: u32,
    #[serde(default)]
    token: String,
    #[serde(default)]
    shutdown_token: String,
}

/// Handle on a running `kit serve`, spawned or attached.
pub struct KitEngine {
    pub port: u16,
    pub pid: u32,
    pub token: Option<String>,
    pub shutdown_token: Option<String>,
    pub base_url: String,
    process: Option<Child>,
    stop_timeout: Duration,
    client: reqwest::Client,
}

fn non_empty(s: String) -> Option<String> {
    (!s.is_empty()).then_some(s)
}

impl KitEngine {
    /// Spawns `kit serve`, reads the startup line, gates on `/health`.
    pub async fn start(opts: KitEngineOptions) -> Result<Self, EngineError> {
        let bin = opts
            .bin_path
            .clone()
            .unwrap_or_else(|| PathBuf::from("kit"));
        let mut cmd = Command::new(bin);
        cmd.arg("serve").arg("--port").arg(opts.port.to_string());
        if let Some(a) = &opts.app {
            cmd.args(["--app", a]);
        }
        if let Some(d) = &opts.data {
            cmd.args(["--data", d]);
        }
        for (on, flag) in [
            (opts.daemon, "--daemon"),
            (opts.encrypt, "--encrypt"),
            (opts.no_peer, "--no-peer"),
            (opts.no_sync, "--no-sync"),
        ] {
            if on {
                cmd.arg(flag);
            }
        }
        cmd.stdin(Stdio::null())
            .stdout(Stdio::piped())
            .stderr(Stdio::null())
            .kill_on_drop(true);
        let mut child = cmd.spawn()?;
        let stdout = child.stdout.take().expect("stdout piped");
        let mut lines = BufReader::new(stdout).lines();

        let line = match tokio::time::timeout(opts.startup_timeout, lines.next_line()).await {
            Ok(Ok(Some(l))) => l,
            Ok(Ok(None)) => {
                let status = child.wait().await.ok();
                return Err(EngineError::Startup(format!(
                    "kit serve exited before printing startup line ({status:?})"
                )));
            }
            Ok(Err(e)) => return Err(e.into()),
            Err(_) => {
                let _ = child.kill().await;
                return Err(EngineError::Startup(
                    "timed out waiting for startup line".into(),
                ));
            }
        };
        let info: Startup = match serde_json::from_str(&line) {
            Ok(i) => i,
            Err(_) => {
                let _ = child.kill().await;
                return Err(EngineError::Startup(format!(
                    "invalid startup JSON: {line}"
                )));
            }
        };
        // Keep draining so the engine never blocks or takes SIGPIPE on stdout.
        tokio::spawn(async move { while let Ok(Some(_)) = lines.next_line().await {} });

        let client = reqwest::Client::new();
        let base_url = format!("http://127.0.0.1:{}", info.port);
        let health = async {
            check(client.get(url(&base_url, &["health"])?).send().await?).await?;
            Ok::<_, EngineError>(())
        };
        if let Err(e) = health.await {
            let _ = child.kill().await;
            return Err(e);
        }
        Ok(Self {
            port: info.port,
            pid: info.pid,
            token: non_empty(info.token),
            shutdown_token: non_empty(info.shutdown_token),
            base_url,
            process: Some(child),
            stop_timeout: opts.stop_timeout,
            client,
        })
    }

    /// Attaches to a running engine after a `/health` check.
    pub async fn connect(
        port: u16,
        token: Option<String>,
        shutdown_token: Option<String>,
    ) -> Result<Self, EngineError> {
        #[derive(Deserialize)]
        struct Health {
            #[serde(default)]
            pid: u32,
        }
        let client = reqwest::Client::new();
        let base_url = format!("http://127.0.0.1:{port}");
        let resp = check(client.get(url(&base_url, &["health"])?).send().await?).await?;
        let pid = resp.json::<Health>().await.map(|h| h.pid).unwrap_or(0);
        Ok(Self {
            port,
            pid,
            token,
            shutdown_token,
            base_url,
            process: None,
            stop_timeout: KitEngineOptions::default().stop_timeout,
            client,
        })
    }

    /// `POST /shutdown` with the shutdown token (falls back to the auth
    /// token), then reaps the child if this handle spawned one; a child
    /// that outlives `stop_timeout` is killed.
    pub async fn stop(mut self) -> Result<(), EngineError> {
        let tok = self.shutdown_token.as_deref().or(self.token.as_deref());
        let rb = self.client.post(url(&self.base_url, &["shutdown"])?);
        check(bearer(rb, tok).send().await?).await?;
        if let Some(mut child) = self.process.take() {
            if tokio::time::timeout(self.stop_timeout, child.wait())
                .await
                .is_err()
            {
                child.kill().await?;
            }
        }
        Ok(())
    }

    pub fn collection<T: DeserializeOwned + Serialize>(
        &self,
        type_name: &str,
    ) -> Collection<'_, T> {
        Collection {
            client: &self.client,
            base: &self.base_url,
            type_name: type_name.to_owned(),
            token: self.token.as_deref(),
            _doc: PhantomData,
        }
    }

    pub fn sync(&self) -> SyncClient<'_> {
        SyncClient {
            client: &self.client,
            base: &self.base_url,
            token: self.token.as_deref(),
        }
    }

    pub fn peers(&self) -> PeerClient<'_> {
        PeerClient {
            client: &self.client,
            base: &self.base_url,
            token: self.token.as_deref(),
        }
    }

    pub fn identity(&self) -> IdentityClient<'_> {
        IdentityClient {
            client: &self.client,
            base: &self.base_url,
            token: self.token.as_deref(),
        }
    }

    /// Event stream for `topic`; connects lazily on first `next()`.
    #[cfg(feature = "events")]
    pub fn events(&self, topic: &str) -> crate::events::EventStream {
        crate::events::EventStream::new(format!("ws://127.0.0.1:{}/events", self.port), topic)
    }
}
