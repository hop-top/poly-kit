# bus/event

## What it answers

Which timestamps does a bus `Event` accept, and what text does it keep for them? This holds the
private submodules of `bus::event`. `timestamp.rs` has the RFC 3339 grammar, the canonical form
Go's `time.Time` re-encodes, and the clock formatting `Event::new_at` writes. The envelope itself
is `event.rs` in the parent directory.

## Use it when

- you change how an event timestamp is validated or canonicalized: start here, then run the
  vectors

## Quick start

```sh
cargo test --features bus --test bus_conformance
cargo test --features bus --test bus_event
```

## Contract

- Go is canonical: `contracts/bus-v1/event_timestamp.json` pins accepted and refused inputs and
  their canonical text. A failing vector means this port drifted.
- Refusals are `invalid RFC 3339 timestamp` and `invalid zone offset`, as Go's `Event` decode
  reports them.
- The code is pure and wasm-safe. Only `format_rfc3339` takes a time, and the caller supplies it.

## Neighbours

- `cloudevents/` (../cloudevents/): the CloudEvents mapping that carries the timestamp as `time`

## See also

- [bus README](../README.md)
- [bus-v1 vectors](../../../../../contracts/bus-v1/README.md)
