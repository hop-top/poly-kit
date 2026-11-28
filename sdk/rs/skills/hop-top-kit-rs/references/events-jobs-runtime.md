# Events, jobs and runtime

Open when components must react to events, state changes must replicate, or
a tool notifies or records usage. Long form: [Rust SDK reference](https://github.com/hop-top/poly-kit/blob/next/docs/adopters/reference/rs-sdk.md#bus).

| Need | Feature | Notes |
|------|---------|-------|
| In-process pub/sub with topic grammar | `bus` | wasm-safe |
| Peers over WebSocket | `bus-network`; `bus-network-axum`, `bus-network-webpki-roots` | native; blocking calls panic inside its runtime, use the `_async` variants |
| Entity state machine with veto and post events | `domain` | wasm-safe |
| Two stores converging without a server | `sync`; `sync-http` | HTTP transport is native |
| Notification sinks | `notify`; `notify-webhook`, `notify-email`, `notify-os` | core wasm-safe |
| Consent-gated usage events | `telemetry` | heavy: pulls `api` |

Gaps taken from the parity page:

- Event bus: no SQLite adapter, no remote streams, no outbound CloudEvents.
- Domain building blocks: the state machine only, no other building block.
- Entity sync: no durable cursor, no `BusPublisher`, no SQLite repository.
- Notifications and telemetry have modules but nothing pins them to Go: test
  the behaviour you rely on.

The job queue and the peer mesh have no Rust module: see
[other ports](other-ports.md).
