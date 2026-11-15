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
use hop_top_kit_engine::{KitEngine, KitEngineOptions};

#[tokio::main]
async fn main() -> Result<(), hop_top_kit_engine::EngineError> {
    // Spawns `kit serve`, reads the startup line, waits for /health.
    let engine = KitEngine::start(KitEngineOptions {
        app: Some("myapp".into()),
        ..Default::default()
    })
    .await?;

    let key = engine.identity().public_key().await?;
    let peers = engine.peers().list().await?;
    engine.sync().add_remote("origin", "https://sync.example.com", "both", "").await?;

    // Attach to an engine that is already running instead:
    // KitEngine::connect(port, Some(token), Some(shutdown_token)).await?

    engine.stop().await?; // POST /shutdown, then reaps the child
    Ok(())
}
```

`stop` consumes the handle. A dropped handle kills a spawned child.
`KitEngineOptions::bin_path` selects the `kit` binary (default: `kit` on
PATH).

With the `events` feature, `engine.events("notes.created")` returns an
`EventStream`; `next().await` yields the `data` of each matching frame.

## Development

```sh
cargo test                      # unit + mocked-HTTP tests
cargo test --features parity    # needs KIT_BIN=/path/to/kit
```

Dependency pins follow `sdk/rs`.
