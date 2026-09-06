# telemetry

## What it answers

Did the user agree to this egress, and how are usage events redacted, queued
and shipped under that consent? Network reachability is
`hop_top_kit::netpolicy`; the HTTP client itself is `hop_top_kit::api`.

## Use it when

- a command wants to emit a usage event without blocking → `Client::record(event, attrs)`
- the process must end without losing spooled events → `client.shutdown(timeout).await`
- the operator configures the sink by environment → `ClientOptions::from_env()`
- the emission tier must be checked before doing work → `resolve_mode()`
- the persisted consent decision must be read → `load_consent()` (read-only; the Go `kit-consent` tool writes)
- a payload carries app-specific secrets → set `ClientOptions::redactor`, which runs before the default pass

## Quick start

Verified by `tests/client.rs` (`record_writes_jsonl_line`), which seeds a granted consent file
and `KIT_TELEMETRY_MODE=full` before this excerpt runs:

```rust
let client = Client::new(ClientOptions {
    sink: SinkKind::Jsonl,
    sink_file: Some(sink_path.to_string_lossy().into_owned()),
    queue_size: 16,
    ..Default::default()
})
.expect("client construction");

client.record("test.event", json!({"k": "v"})).unwrap();
client.record("test.event2", json!({"n": 1})).unwrap();

// Give the drain task a beat to flush + then shutdown.
client.shutdown(Duration::from_millis(200)).await.unwrap();
```

## Contract

- Feature `telemetry` pulls in `api` plus `tokio`, `reqwest`, `serde`, `serde_json`, `serde_yaml`,
  `sha2`, `getrandom`, `dirs`, `regex`, `thiserror`. Authority: the crate
  [feature table](../../README.md#features).
- A tokio runtime must be live at `Client::new`; construction spawns the drain task.
- Default-denied: `record` is a no-op unless the mode is `anon` or `full` AND the consent file
  grants. Any missing or unparsable consent file denies.
- Mode precedence: `<APP>_TELEMETRY_MODE` (when `KIT_APP_PREFIX` is set), then `KIT_TELEMETRY_MODE`,
  then off. Unknown tokens fall through to the next source.
- `anon` strips `attrs` to null even after a custom redactor populated them.
- `record` never blocks: a full queue increments `dropped_count()` and returns `Ok`.
- Sink defaults to JSONL, so a misconfigured stack spools locally instead of posting to a wrong URL.
- Parity: [`sdk/tests/cross-lang/fixtures/input.json`](../../../../../sdk/tests/cross-lang/fixtures/input.json)
  and `consent.yaml`, `install_id.bytes` in the same directory, replayed by
  [`sdk/tests/cross-lang/runners/rs`](../../../../../sdk/tests/cross-lang/runners/rs/).

## Neighbours

- `hop_top_kit::netpolicy` (src/netpolicy.rs): the `--offline` marker every reqwest call in this crate honours
- `hop_top_kit::api` (src/api.rs): the guarded client the HTTPS sink is built on
- `hop_top_kit::id` (src/id/): TypeIDs for records; `install_id` here is a different, opaque token

## See also

- Crate README, [Telemetry](../../../../../docs/adopters/reference/rs-sdk.md#telemetry)
- [`docs/adopters/guides/telemetry.md`](../../../../../docs/adopters/guides/telemetry.md)
- [`sdk/docs/telemetry-event-schema.md`](../../../../docs/telemetry-event-schema.md)
- [`go/runtime/telemetry`](../../../../../go/runtime/telemetry/README.md), the Go reference
