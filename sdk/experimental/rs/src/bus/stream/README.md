# bus/stream

## What it answers

How does a bus link exchange event frames with offsets, resume after a drop and carry control requests,
without owning a socket? Submodules of `bus::stream`, the I/O-free half of Go's `stream_*.go` and the link
logic of `network_*.go`: wire primitives (`wire.rs`), frames (`frame.rs`, `control.rs`), the handshake
(`handshake.rs`, `ack.rs`), subscriptions (`subs.rs`), positions (`cursor.rs`), the send log and outbox
(`log.rs`), and `StreamClient`, a dialer's link state machine for one address (`client.rs`, `dial.rs`,
`requests.rs`).

## Use it when

- writing a transport (native WebSocket or a browser one): drive a `StreamClient` per outbound address
- changing stream semantics: start here, then run the vectors

## Quick start

```sh
cargo test --features bus --test 'bus_stream_conformance*'
cargo test --features bus --test 'bus_stream_client*'
```

## Contract

- Go is canonical: frames, error codes and resume decisions are pinned by `contracts/bus-v1/streams`; a
  failing vector means this port drifted. The design is `docs/contributors/architecture/bus-streams.md`.
- Sans-IO: nothing here touches a socket, a clock, a timer, a lock or a random source, so the same code
  runs natively and on `wasm32-unknown-unknown`. Bytes and events go in, decisions come out: frames to
  write, closes to send, events to deliver. `StreamClient` holds no lock; wrap it in the transport's own
  synchronisation.
- The transport owns the I/O and calls the client in order: `begin_handshake` (or `begin_legacy`), the
  reply to `on_ack` with the same `HandshakeId`, every frame to `on_frame`, `heartbeat_tick` on its timer,
  `next_batch` with `write_ok` / `write_failed`, `link_down` when the socket closes, and `disconnect` to
  forget the address for good. A stale or late ack is refused, never applied to a later dial.
- Transport duties the core cannot do:
  - mint the stream id: Go mints a `stm_` TypeID for an empty one, but this core has no randomness, so
    `RingLog::new` takes the id (1-128 printable ASCII, `valid_stream_id`). A log that loses its
    contents must change its id.
  - context per handler: pass `delivery_context` to handlers the link's reader runs inline (sync
    handlers, observers), so a request back to that address fails `Reentrant` instead of deadlocking.
    Async handlers and spawned tasks run off that thread: give them
    `StreamClient::clear_delivery(&ctx)`, the context without the delivery marker, so their requests
    go through.
  - never dial one address twice at once: one `StreamClient` per address, one socket per dial.
    `begin_handshake` returns `HandshakeError::Busy` while a dial or link is live; wait for
    `link_down` before redialing. After `disconnect`, a new connect is a new `StreamClient`.
- The adapter owns `kit.runtime.stream.*` and `kit.runtime.link.*` (`is_reserved_topic`): they never enter
  a log, and inbound frames carrying them are dropped.

## Neighbours

- `cloudevents/` (../cloudevents/): the frame codec event frames are built on
- `event/` (../event/): the `Event` envelope and its timestamp grammar

## See also

- [bus README](../README.md)
- [Stream vectors](../../../../../../contracts/bus-v1/streams/README.md)
- [Bus streams design](../../../../../../docs/contributors/architecture/bus-streams.md)
