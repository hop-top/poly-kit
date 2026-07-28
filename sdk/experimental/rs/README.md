# rs

experimental Rust client SDK.

## Modules

- [`src/id/`](src/id/) — TypeID primitive (cross-language; see
  [ADR 0001](../../../docs/adr/0001-typeid-primitive.md))

## URI facade

The Rust URI facade is experimental and feature-gated:

```toml
[dependencies]
hop-top-kit = { version = "0.1", features = ["uri"] }
```

`hop_top_kit::uri` delegates to `hop-top-cite`; it does not reimplement
URI parsing, action routing, completion, or handler generation.
`hop-top-cite` is published on crates.io.

## httpcache wire contract

The JSON fixtures in [`contracts/httpcache-v1/`](../../../contracts/httpcache-v1/)
are the **contract of record** for `httpcache`, not the Go source. Go is
currently the only implementation, so a second port does not conform to a
settled format — it pins the format for every later one. Read the fixtures
first and treat any disagreement with the Go source as a bug in whichever
side the fixtures do not cover.

| Fixture | Pins |
|---------|------|
| `keying.json` | `prefix + sha256(method + " " + url)`, plus which URL transforms are and are not applied |
| `entry.json` | The on-store envelope: `status`, `headers`, `body`, and the framing-header strip/recompute rule |
| `cacheability.json` | Which requests and responses may be stored |

Three details diverge from what a serializer's defaults would produce:

- **`body` is a base64 string, not an array of byte numbers.** Serde renders
  `Vec<u8>` as `[104,105]` by default; the contract requires `"aGk="`.
  Encode and decode explicitly at the field boundary.
- **Standard base64 alphabet, with padding.** Not URL-safe, not unpadded.
  A URL-safe decoder rejects conforming envelopes and emits unreadable ones.
- **`headers` maps to a *list* of strings**, so duplicate `Set-Cookie` and
  `Vary` values survive. A map to a bare string is lossy and non-conforming.

Keying takes the method and URL **verbatim** — no method upper-casing, host
lower-casing, default-port stripping, query sorting, dot-segment resolution,
or fragment stripping. A URL type that normalizes on re-serialization will
silently produce keys Go cannot read; `keying.json` has a case for each.

`serde` + `serde_json` + `base64` + `sha2` cover the whole contract; nothing
exotic is needed.

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
collector that re-emits via `go/core/redact`.

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
| `XDG_CONFIG_HOME` | Locates the consent file (`<HERE>/kit/config.yaml` at `kit.telemetry.consent`; legacy `<HERE>/kit/telemetry.yaml` read as fallback). |
| `XDG_STATE_HOME` | Locates the install_id file (`<HERE>/kit/telemetry/installation_id`). |

### Cross-references

- Schema doc: [`sdk/docs/telemetry-event-schema.md`](../../docs/telemetry-event-schema.md).
- Go canonical implementation:
  [`go/runtime/telemetry/README.md`](../../../go/runtime/telemetry/README.md).

<!-- release: track hop-top-cite 0.1.0 -->
