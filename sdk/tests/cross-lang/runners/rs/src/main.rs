//! Rust runner for the cross-language telemetry contract harness.
//!
//! Constructs a `telemetry::Client` configured for the jsonl sink, reads
//! the shared `fixtures/input.json`, calls `record()`, and waits for
//! shutdown. The orchestrator handles all env / pre-seeding.

use std::fs;
use std::path::PathBuf;
use std::time::Duration;

use hop_top_kit::telemetry::client::{Client, ClientOptions, SinkKind};

#[tokio::main]
async fn main() -> Result<(), Box<dyn std::error::Error>> {
    let cross_lang: PathBuf = std::env::current_exe()
        .map(|p| {
            // walk up `target/debug/<bin>` → `target/debug` → `target` → `rs` → ..
            let mut q = p.clone();
            for _ in 0..3 {
                q.pop();
            }
            q
        })
        .unwrap_or_else(|_| PathBuf::from("."));
    // Allow override via env so the orchestrator can point us at the
    // fixtures even when the binary lives elsewhere.
    let cross_lang = std::env::var_os("KIT_CROSS_LANG_DIR")
        .map(PathBuf::from)
        .unwrap_or_else(|| {
            // Fall back to walking up from CARGO_MANIFEST_DIR-style heuristics.
            // env!() at build time would resolve to the runner's manifest;
            // its parent is `runners/rs`, two more up is `cross-lang`.
            let manifest = PathBuf::from(env!("CARGO_MANIFEST_DIR"));
            manifest
                .parent()
                .and_then(|p| p.parent())
                .map(PathBuf::from)
                .unwrap_or(cross_lang)
        });

    let fixtures = cross_lang.join("fixtures");
    let input_path = std::env::var_os("KIT_CROSS_LANG_INPUT")
        .map(PathBuf::from)
        .unwrap_or_else(|| fixtures.join("input.json"));
    let raw = fs::read_to_string(&input_path)?;
    let payload: serde_json::Value = serde_json::from_str(&raw)?;
    let event = payload["event"].as_str().unwrap_or("");
    let attrs = payload["attrs"].clone();

    let sink_file = std::env::var("KIT_TELEMETRY_SINK_FILE")
        .map_err(|_| "KIT_TELEMETRY_SINK_FILE must be set by the orchestrator")?;

    let opts = ClientOptions {
        sink: SinkKind::Jsonl,
        sink_file: Some(sink_file),
        queue_size: 16,
        sdk_version: Some("cross-lang-test".to_string()),
        ..Default::default()
    };
    let client = Client::new(opts)?;
    client.record(event, attrs)?;
    // shutdown takes ownership; sleep window covers the drain.
    client.shutdown(Duration::from_secs(2)).await?;
    Ok(())
}
