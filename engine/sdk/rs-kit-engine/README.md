# hop-top-kit-engine

`kit serve` gives your Rust app the same capabilities as a native Go kit
app: identity, local storage, sync, peer discovery, encryption, without
reimplementing anything. This crate is the Rust client for the sidecar;
API shape matches the [TypeScript](../ts-kit-engine/README.md) and
[Python](../py-kit-engine/README.md) SDKs.

## Features

| Feature  | Default | Adds                                                  |
|----------|---------|-------------------------------------------------------|
| `http`   | yes     | `KitEngine` lifecycle, collections, sync, peers, identity (reqwest, tokio, serde) |
| `events` | no      | WebSocket event stream (implies `http`; tokio-tungstenite) |
| `parity` | no      | Enables integration tests that need a real `kit serve` binary (`KIT_BIN`); parity harness only |

## Quickstart

```rust
use hop_top_kit_engine::EngineError;
// Engine and collection APIs land with the `http` feature; see the
// module docs once published.
```

## Development

```sh
cargo test                      # unit + mocked-HTTP tests
cargo test --features parity    # needs KIT_BIN=/path/to/kit
```

Dependency pins follow `sdk/rs`.
