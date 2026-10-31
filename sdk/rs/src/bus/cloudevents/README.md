# bus/cloudevents

## What it answers

How does a bus `Event` become a CloudEvents 1.0 structured-mode document and back, and how are
network frames told apart and encoded? Submodules of `bus::cloudevents`: the JSON codec
(`json.rs`), the `Event` mapping (`mapping.rs`), attribute rules (`rules.rs`) and the frame codec
(`frame.rs`).

## Use it when

- changing CloudEvents shaping or frame handling: start here, then run the vectors

## Quick start

```sh
cargo test --features bus --test bus_cloudevents_conformance
cargo test --features bus --lib bus::cloudevents
```

## Contract

- Go is canonical: behaviour, error texts and wire shapes are pinned by
  `contracts/bus-v1/cloudevents`; a failing vector means this port drifted.
- Decoding reads member text, as Go's `map[string]json.RawMessage`: `data` keeps its exact text
  and extension integers are judged on their literal.
- Pure and wasm-safe: no clock reads. An event without an id is minted an id natively and refused
  (`cloudevents: id: cannot mint`) on `wasm32-unknown-unknown`.
- Differences from Go are listed in the module docs of `cloudevents.rs`.

## Neighbours

- `event/` (../event/): the `Event` envelope and its timestamp grammar

## See also

- [bus README](../README.md)
- [CloudEvents vectors](../../../../../../contracts/bus-v1/cloudevents/README.md)
