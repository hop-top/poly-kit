# bus-v1/streams/wire

## What it answers

Exact frame sequences between a dialer and a Go acceptor: the plain ack for legacy dialers, the extended ack with replay, a reset in the ack, and a subscribe round trip. Wrong place for pure rules; those are the JSON vectors one level up.

## Use it when

- you port the acceptor side of bus streams and want byte-level evidence it speaks the same protocol
- you change Go's handshake or control frames: update the transcript, then every port

## Contract

Each `.jsonl` file starts with a header (`version`, `description`, `setup` with `log_size` and events published before the dialer connects), then one step per line: `send` (dialer to acceptor), `recv` (next frame from the acceptor, compared as a JSON value, plus exact `bytes` when given; an error `message` is not compared unless listed), or `publish` (an event the acceptor publishes). The acceptor runs with stream id `stm_wire`, origin `hub` and `OpenAuth`. Runner: `go/runtime/bus/stream_wire_conformance_test.go`.
