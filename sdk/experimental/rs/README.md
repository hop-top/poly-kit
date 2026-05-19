# rs

experimental Rust client SDK.

## URI facade

The Rust URI facade is experimental and feature-gated:

```toml
[dependencies]
hop-top-kit = { version = "0.1", features = ["uri"] }
```

`hop_top_kit::uri` delegates to `hop-top-uri`; it does not reimplement
URI parsing, action routing, completion, or handler generation. The
current workspace uses a local path dependency until `hop-top-uri` is
published to crates.io.

## Telemetry

The Rust telemetry SDK is feature-gated. It mirrors the Go canonical
runtime (`hops/main/go/runtime/telemetry`) at the data-only seams and
is **default-denied**: nothing is emitted until both
`KIT_TELEMETRY_MODE` (or `<APP>_TELEMETRY_MODE`) AND the persisted
consent file say so.

```toml
[dependencies]
hop-top-kit = { version = "0.4.0-experimental.2", features = ["telemetry"] }
```

```bash
cargo build --features telemetry
cargo test  --features telemetry
```

### Runtime requirement

The client uses `tokio::sync::mpsc` and `tokio::spawn` for its
background drain. **A tokio runtime MUST be live at `Client::new`**.
Adopters without an existing runtime should wrap construction in a
`tokio::runtime::Builder::new_current_thread().enable_all().build()`
context until the planned `tokio-current-thread` helper ships.

### Quick start

```rust
use hop_top_kit::telemetry::{Client, ClientOptions, SinkKind};
use serde_json::json;

#[tokio::main]
async fn main() {
    let client = Client::new(ClientOptions {
        sink: SinkKind::Jsonl,
        sink_file: Some("/tmp/kit-events.jsonl".into()),
        ..Default::default()
    })
    .expect("telemetry client");

    // record() is fire-and-forget. Never blocks. Returns Ok(()) even
    // when the queue is full (dropped count surfaces via
    // client.dropped_count()).
    client.record("app.start", json!({"version": "1.0"})).unwrap();
}
```

### Custom redactor escape hatch

The default redactor (`telemetry::redact`) is best-effort: emails,
IPv4/IPv6, `$HOME` paths, and a handful of bearer-token prefixes
(`sk-`, `ghp_`/`gho_`/`ghu_`/`ghs_`/`ghr_`, `xoxb-`). Adopters with
stricter PII policies pass a custom redactor that runs **before** the
default one (defense in depth):

```rust
use hop_top_kit::telemetry::{Client, ClientOptions, SinkKind};
use serde_json::Value;

let redactor = Box::new(|v: Value| -> Value {
    // strip everything except a fixed allowlist
    serde_json::json!({})
});

let _ = Client::new(ClientOptions {
    sink: SinkKind::Jsonl,
    sink_file: Some("/tmp/kit-events.jsonl".into()),
    redactor: Some(redactor),
    ..Default::default()
});
```

Compliance-sensitive adopters should route SDK events through a Go-side
collector that re-emits via `go/core/redact` — see ADR-0038 §3 for the
recommended topology.

### Sinks

- `SinkKind::Jsonl` — append-only `.jsonl` file with 10 MB rotation.
  Default; pairs well with a Go-side collector that tails the spool.
- `SinkKind::Https` — POST `application/x-ndjson` to `endpoint`, 5s
  connect / 10s overall timeout, one retry on 5xx / transport.

### Env vars

| Env var | Purpose |
|---------|---------|
| `KIT_TELEMETRY_MODE` | `off | anon | full` (SDK-level). |
| `<APP>_TELEMETRY_MODE` | Overrides KIT-level when `KIT_APP_PREFIX` is set. |
| `KIT_TELEMETRY_ENDPOINT` | HTTPS sink target (used by `ClientOptions::from_env`). |
| `KIT_TELEMETRY_SINK` | `https | jsonl` (used by `ClientOptions::from_env`). |
| `KIT_TELEMETRY_SINK_FILE` | JSONL path (used by `ClientOptions::from_env`). |
| `KIT_TELEMETRY_QUEUE_SIZE` | Bounded channel capacity, defaults to 1024. |
| `XDG_CONFIG_HOME` | Locates the consent file (`<HERE>/kit/telemetry.yaml`). |
| `XDG_STATE_HOME` | Locates the install_id file (`<HERE>/kit/telemetry/installation_id`). |

### Cross-references

- Canonical contract: ADR-0038
  (SDK delta-from-Go).
- Schema doc: [`sdk/docs/telemetry-event-schema.md`](../../docs/telemetry-event-schema.md).
- Go canonical implementation:
  [`go/runtime/telemetry/README.md`](../../../go/runtime/telemetry/README.md).
