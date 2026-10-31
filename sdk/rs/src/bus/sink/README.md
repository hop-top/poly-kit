# bus/sink

## What it answers

Where do published events go besides the subscribers? This is `bus::sink`: the `Sink` trait, the
`TeeBus` that fans each accepted event out to sinks, `JsonlSink` (one JSON line per event),
`StdoutSink` (one human-readable line) and the `KIT_BUS_SINK` env switch `DynBus::new` reads.
`gofmt.rs` holds Go's `%v` and `encoding/json` text for a payload, so output matches Go's bytes.

## Use it when

- you change what a sink writes: update the Go sink first, regenerate the goldens, then this port
- you add an env sink kind: Go's `env_sink.go` first, then `env.rs`

## Quick start

```sh
cargo test --features bus --test bus_sinks --test bus_sinks_env --test bus_sinks_conformance
```

## Contract

- Go is canonical: `contracts/bus-v1/sinks` pins the stdout and JSONL bytes and the env-sink reporter
  lines. A failing golden means this port drifted.
- A sink drains only after the wrapped bus accepted the publish; sink errors go to the reporter and
  never fail the publish. Close closes the bus, then every sink: the bus error wins, else the first
  sink error.
- `JsonlSink::open` (append, mode 0600) and the env switch are not built on wasm32.

## Neighbours

- `compose.rs` (../compose.rs): applies the env sink last, outside the adapter and network layers

## See also

- [bus README](../README.md)
- [sink goldens](../../../../../../contracts/bus-v1/sinks/README.md)
