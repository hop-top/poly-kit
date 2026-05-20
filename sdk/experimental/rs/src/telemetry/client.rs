//! Non-blocking telemetry client.
//!
//! Provides the SDK-side `Client` that adopters call from request /
//! command paths. Constraints:
//!
//! - `record()` is fire-and-forget. NEVER blocks, NEVER awaits, NEVER
//!   panics on a full queue.
//! - Construction spawns a single background drain task via
//!   `tokio::spawn`. The Rust SDK requires a tokio runtime to be live
//!   at `Client::new` call time; adopters without a runtime should
//!   wrap with the documented `tokio-current-thread` helper (TBD —
//!   tracked separately in the sdk-telemetry track).
//! - Two sinks: HTTPS (one retry, 5s connect / 10s overall timeout)
//!   and JSONL append-with-rotation. Sink selection is at construction
//!   time via `ClientOptions::sink`.
//! - Mode/consent gates short-circuit BEFORE any allocation work.
//!   `Mode::Off` or `Consent::denied` collapses `record()` to a
//!   no-op (Ok(())) with no envelope build, no channel send.
//! - Optional custom redactor runs FIRST; the default
//!   `telemetry::redact::redact` pass runs after (defense in depth).
//!
//! Envelope shape: mirrors the Go canonical `Event` at the seams the
//! SDK can fill (`sdk_lang = "rs"`, `sdk_version`, `installation_id`,
//! `mode`, `occurred_at`). The current spec uses `event` + `attrs` as
//! the caller-facing pair; the cross-language harness is the
//! eventual gate that reconciles this with the Go `command_path` /
//! `args` / `flags` layout. Until then the experimental SDK ships
//! `event` + `attrs` to match the polyglot py/ts experimental clients.

use serde::Serialize;
use serde_json::Value;
use std::sync::atomic::{AtomicU64, Ordering};
use std::sync::Arc;
use std::time::{Duration, SystemTime, UNIX_EPOCH};
use tokio::sync::mpsc;

use crate::telemetry::consent::load_consent;
use crate::telemetry::install_id::get_install_id;
use crate::telemetry::mode::{resolve_mode, Mode};
use crate::telemetry::redact::redact;

/// Sink choice selected at `Client::new` time. JSONL is the local
/// debug / spool path; Https is the production path against a
/// kit-side collector.
#[derive(Debug, Clone, Copy, PartialEq, Eq)]
pub enum SinkKind {
    Https,
    Jsonl,
}

/// Construction-time options. `from_env()` mirrors the Go side's
/// env-driven resolver; adopters can also build this struct directly
/// for testing.
pub struct ClientOptions {
    /// Endpoint URL for the Https sink. Required when `sink ==
    /// SinkKind::Https`; ignored otherwise.
    pub endpoint: Option<String>,
    /// Selected sink.
    pub sink: SinkKind,
    /// Path for the Jsonl sink. Required when `sink ==
    /// SinkKind::Jsonl`; ignored otherwise.
    pub sink_file: Option<String>,
    /// Bounded channel capacity. Defaults to 1024 when constructed via
    /// `from_env` without `KIT_TELEMETRY_QUEUE_SIZE`.
    pub queue_size: usize,
    /// SDK version reported on the wire. Defaults to the crate's
    /// `CARGO_PKG_VERSION` when None.
    pub sdk_version: Option<String>,
    /// Optional custom redactor that runs BEFORE the default redactor.
    /// Stored boxed so the `Client` stays `Send + Sync`.
    pub redactor: Option<Box<dyn Fn(Value) -> Value + Send + Sync>>,
}

impl Default for ClientOptions {
    fn default() -> Self {
        Self {
            endpoint: None,
            sink: SinkKind::Jsonl,
            sink_file: None,
            queue_size: 1024,
            sdk_version: None,
            redactor: None,
        }
    }
}

impl ClientOptions {
    /// Resolve options from environment:
    ///
    /// - `KIT_TELEMETRY_ENDPOINT` → `endpoint`.
    /// - `KIT_TELEMETRY_SINK` (`https` or `jsonl`, case-insensitive)
    ///   → `sink`. Defaults to `jsonl` so misconfigured prod stacks
    ///   spool locally instead of POSTing to a wrong URL.
    /// - `KIT_TELEMETRY_SINK_FILE` → `sink_file`.
    /// - `KIT_TELEMETRY_QUEUE_SIZE` → `queue_size` (parse failure
    ///   falls back to 1024).
    pub fn from_env() -> Self {
        let endpoint = std::env::var("KIT_TELEMETRY_ENDPOINT").ok();
        let sink = std::env::var("KIT_TELEMETRY_SINK")
            .ok()
            .map(|v| v.trim().to_lowercase())
            .map(|v| match v.as_str() {
                "https" => SinkKind::Https,
                _ => SinkKind::Jsonl,
            })
            .unwrap_or(SinkKind::Jsonl);
        let sink_file = std::env::var("KIT_TELEMETRY_SINK_FILE").ok();
        let queue_size = std::env::var("KIT_TELEMETRY_QUEUE_SIZE")
            .ok()
            .and_then(|v| v.parse::<usize>().ok())
            .filter(|n| *n > 0)
            .unwrap_or(1024);
        Self {
            endpoint,
            sink,
            sink_file,
            queue_size,
            sdk_version: None,
            redactor: None,
        }
    }
}

/// Wire-format envelope. Field ordering is part of the contract for
/// the cross-language harness; do not reorder without updating the
/// shared schema doc.
#[derive(Debug, Serialize, Clone)]
pub struct Envelope {
    pub schema_version: String,
    pub sdk_lang: String,
    pub sdk_version: String,
    pub installation_id: String,
    pub mode: String,
    pub occurred_at: String,
    pub event: String,
    pub attrs: Value,
}

/// Client construction / lifecycle errors. `record()` itself returns
/// `Ok(())` on dropped-queue (per the non-blocking contract); only the
/// initial setup paths surface real errors.
#[derive(Debug, thiserror::Error)]
pub enum ClientError {
    #[error("client: drain task closed the channel")]
    Closed,
    #[error("client: failed to resolve install_id: {0}")]
    InstallId(#[from] std::io::Error),
    #[error("client: sink misconfigured: {0}")]
    SinkConfig(String),
}

/// Telemetry client. Cheap to `Clone` — internals are `Arc`'d. The
/// drain task lives for the lifetime of the last clone; dropping all
/// clones closes the channel and triggers graceful shutdown of the
/// drain task.
#[derive(Clone)]
pub struct Client {
    tx: mpsc::Sender<Envelope>,
    dropped: Arc<AtomicU64>,
    redactor: Option<Arc<dyn Fn(Value) -> Value + Send + Sync>>,
    install_id: String,
    sdk_version: String,
}

impl Client {
    /// Construct a `Client`. Spawns the background drain task via
    /// `tokio::spawn`, so a tokio runtime MUST be live in the calling
    /// context.
    ///
    /// Resolves `installation_id` once at construction time and caches
    /// it. Subsequent `record()` calls reuse the cached value so the
    /// hot path stays allocation-light.
    pub fn new(opts: ClientOptions) -> Result<Self, ClientError> {
        let install_id = get_install_id()?;
        let sdk_version = opts
            .sdk_version
            .unwrap_or_else(|| env!("CARGO_PKG_VERSION").to_string());

        let (tx, rx) = mpsc::channel::<Envelope>(opts.queue_size);
        let redactor = opts.redactor.map(Arc::from);
        let dropped = Arc::new(AtomicU64::new(0));

        // Build the sink first so config errors surface synchronously
        // (rather than from inside the drain task where they'd silently
        // increment dropped).
        let sink = build_sink(opts.sink, opts.endpoint, opts.sink_file)?;

        // Spawn drain. Requires a live tokio runtime — Client::new
        // panics here with a clear message if there is none.
        tokio::spawn(drain_loop(rx, sink));

        Ok(Self {
            tx,
            dropped,
            redactor,
            install_id,
            sdk_version,
        })
    }

    /// Record an event. Fire-and-forget; never blocks.
    ///
    /// Short-circuits when:
    /// - `resolve_mode() == Mode::Off`.
    /// - `load_consent().allowed == false`.
    ///
    /// On queue saturation increments `dropped_count()` and returns
    /// `Ok(())`.
    pub fn record(&self, event: &str, attrs: Value) -> Result<(), ClientError> {
        let mode = resolve_mode();
        if mode == Mode::Off {
            return Ok(());
        }
        if !load_consent().allowed {
            return Ok(());
        }

        let mut redacted = if let Some(r) = &self.redactor {
            (r)(attrs)
        } else {
            attrs
        };
        redacted = redact(redacted);

        // Anon-tier defensive strip ("Anon vs Full payload boundary"):
        // drop free-form attrs when mode == anon, even if a custom
        // redactor populated them.
        if mode == Mode::Anon {
            redacted = Value::Null;
        }

        let envelope = Envelope {
            schema_version: "1".to_string(),
            sdk_lang: "rs".to_string(),
            sdk_version: self.sdk_version.clone(),
            installation_id: self.install_id.clone(),
            mode: mode_str(mode).to_string(),
            occurred_at: now_rfc3339(),
            event: event.to_string(),
            attrs: redacted,
        };

        match self.tx.try_send(envelope) {
            Ok(()) => Ok(()),
            Err(mpsc::error::TrySendError::Full(_)) => {
                self.dropped.fetch_add(1, Ordering::Relaxed);
                Ok(())
            }
            Err(mpsc::error::TrySendError::Closed(_)) => Err(ClientError::Closed),
        }
    }

    /// Monotonic count of events the queue refused since construction.
    pub fn dropped_count(&self) -> u64 {
        self.dropped.load(Ordering::Relaxed)
    }

    /// Best-effort flush. Closes the sender side and waits up to
    /// `timeout` for the drain task to clear what it can. Calls after
    /// `shutdown` are effectively no-ops (the channel is closed).
    pub async fn shutdown(self, timeout: Duration) -> Result<(), ClientError> {
        // Drop our copy of the sender so the drain loop sees the
        // channel-closed sentinel. Other clones (if any) keep it open
        // until they drop.
        drop(self.tx);
        // Give the drain task a window to flush. We have no direct
        // handle on the task (the spawn returned () in new()); best
        // effort here is a sleep + return.
        tokio::time::sleep(timeout).await;
        Ok(())
    }
}

fn mode_str(m: Mode) -> &'static str {
    match m {
        Mode::Off => "off",
        Mode::Anon => "anon",
        Mode::Full => "full",
    }
}

/// RFC3339 (UTC, nanosecond precision, trailing `Z`) timestamp built
/// from `SystemTime::now()`. Avoids a chrono dep — the format is
/// hand-rolled to match the canonical `occurred_at` shape.
fn now_rfc3339() -> String {
    let now = SystemTime::now()
        .duration_since(UNIX_EPOCH)
        .unwrap_or_default();
    let secs = now.as_secs() as i64;
    let nanos = now.subsec_nanos();

    // Convert epoch seconds to civil date via a small days-since-epoch
    // computation. Anchored at 1970-01-01.
    let days = secs.div_euclid(86_400);
    let secs_of_day = secs.rem_euclid(86_400);
    let hour = secs_of_day / 3600;
    let minute = (secs_of_day % 3600) / 60;
    let second = secs_of_day % 60;
    let (year, month, day) = civil_from_days(days);
    format!(
        "{:04}-{:02}-{:02}T{:02}:{:02}:{:02}.{:09}Z",
        year, month, day, hour, minute, second, nanos
    )
}

/// Howard Hinnant's days-from-civil algorithm, inverse direction.
/// Public domain. Returns (year, month, day) for a day count where
/// day 0 = 1970-01-01.
fn civil_from_days(z: i64) -> (i32, u32, u32) {
    let z = z + 719_468;
    let era = if z >= 0 { z } else { z - 146_096 } / 146_097;
    let doe = (z - era * 146_097) as u32; // [0, 146096]
    let yoe = (doe - doe / 1460 + doe / 36524 - doe / 146_096) / 365; // [0, 399]
    let y = yoe as i64 + era * 400;
    let doy = doe - (365 * yoe + yoe / 4 - yoe / 100); // [0, 365]
    let mp = (5 * doy + 2) / 153; // [0, 11]
    let d = doy - (153 * mp + 2) / 5 + 1; // [1, 31]
    let m = if mp < 10 { mp + 3 } else { mp - 9 }; // [1, 12]
    let y = if m <= 2 { y + 1 } else { y };
    (y as i32, m, d)
}

// ===== sinks =====================================================

enum Sink {
    Https(HttpsSink),
    Jsonl(JsonlSink),
}

struct HttpsSink {
    client: reqwest::Client,
    endpoint: String,
}

struct JsonlSink {
    path: std::path::PathBuf,
}

const JSONL_ROTATE_BYTES: u64 = 10 * 1024 * 1024;

fn build_sink(
    kind: SinkKind,
    endpoint: Option<String>,
    sink_file: Option<String>,
) -> Result<Sink, ClientError> {
    match kind {
        SinkKind::Https => {
            let endpoint = endpoint
                .ok_or_else(|| ClientError::SinkConfig("https sink requires endpoint".into()))?;
            let client = reqwest::Client::builder()
                .connect_timeout(Duration::from_secs(5))
                .timeout(Duration::from_secs(10))
                .build()
                .map_err(|e| ClientError::SinkConfig(format!("reqwest build: {e}")))?;
            Ok(Sink::Https(HttpsSink { client, endpoint }))
        }
        SinkKind::Jsonl => {
            let path = sink_file
                .ok_or_else(|| ClientError::SinkConfig("jsonl sink requires sink_file".into()))?;
            Ok(Sink::Jsonl(JsonlSink {
                path: std::path::PathBuf::from(path),
            }))
        }
    }
}

async fn drain_loop(mut rx: mpsc::Receiver<Envelope>, sink: Sink) {
    match sink {
        Sink::Https(s) => {
            while let Some(env) = rx.recv().await {
                let _ = s.send(&env).await;
            }
        }
        Sink::Jsonl(s) => {
            while let Some(env) = rx.recv().await {
                let _ = s.append(&env).await;
            }
        }
    }
}

impl HttpsSink {
    async fn send(&self, env: &Envelope) -> Result<(), reqwest::Error> {
        let body = serde_json::to_string(env).unwrap_or_default();
        // One retry on 5xx / transport.
        let mut last_err: Option<reqwest::Error> = None;
        for _ in 0..2 {
            match self
                .client
                .post(&self.endpoint)
                .header("Content-Type", "application/x-ndjson")
                .body(format!("{body}\n"))
                .send()
                .await
            {
                Ok(resp) if resp.status().is_server_error() => {
                    // retry on 5xx
                    continue;
                }
                Ok(_) => return Ok(()),
                Err(e) => {
                    last_err = Some(e);
                    continue;
                }
            }
        }
        if let Some(e) = last_err {
            return Err(e);
        }
        Ok(())
    }
}

impl JsonlSink {
    async fn append(&self, env: &Envelope) -> std::io::Result<()> {
        use tokio::io::AsyncWriteExt;
        // Rotate when the existing file exceeds 10 MB. Best-effort:
        // any rotate failure just falls through to append on the
        // current file rather than blocking emission.
        if let Ok(meta) = tokio::fs::metadata(&self.path).await {
            if meta.len() >= JSONL_ROTATE_BYTES {
                let mut rotated = self.path.clone();
                rotated.set_extension("jsonl.1");
                let _ = tokio::fs::rename(&self.path, &rotated).await;
            }
        }
        if let Some(parent) = self.path.parent() {
            let _ = tokio::fs::create_dir_all(parent).await;
        }
        let mut f = tokio::fs::OpenOptions::new()
            .create(true)
            .append(true)
            .open(&self.path)
            .await?;
        let mut line = serde_json::to_string(env).unwrap_or_default();
        line.push('\n');
        f.write_all(line.as_bytes()).await?;
        Ok(())
    }
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn rfc3339_format_is_well_formed() {
        let s = now_rfc3339();
        // Cheap structural check: YYYY-MM-DDTHH:MM:SS.nnnnnnnnnZ
        assert_eq!(s.len(), 30, "got: {s}");
        assert_eq!(&s[4..5], "-");
        assert_eq!(&s[7..8], "-");
        assert_eq!(&s[10..11], "T");
        assert_eq!(&s[19..20], ".");
        assert!(s.ends_with('Z'));
    }

    #[test]
    fn civil_from_days_epoch() {
        assert_eq!(civil_from_days(0), (1970, 1, 1));
    }

    #[test]
    fn options_from_env_defaults_when_unset() {
        // Don't mutate env in unit tests (race-prone); just smoke-test
        // the default path: a default ClientOptions has queue_size
        // 1024 and Jsonl sink.
        let o = ClientOptions::default();
        assert_eq!(o.queue_size, 1024);
        assert_eq!(o.sink, SinkKind::Jsonl);
    }
}
