# bus-v1/streams

## What it answers

What every kit SDK must accept, reject, decide and emit for bus network streams: the handshake with capability advertisement, control frames and their error codes, subscription state, the per-link delivery predicate, the acceptor's resume decision, the dialer's cursor rules, the bounded inbound queue, the outbox, and offsets on event frames. The design is [bus network streams](../../../docs/contributors/architecture/bus-streams.md). Wrong place for CloudEvents codec rules (`../cloudevents`) and topic grammar (`..`).

## Use it when

- you port bus streams to an SDK: pass the pure vectors before writing any socket code
- you change Go stream behaviour: add a vector first, then make every port pass it

## Quick start

```sh
go test ./go/runtime/bus/ -run StreamConformance
```

## Contract

Go is canonical; a runner failure means the implementation drifted. Every file carries `version: "v1"` and a `description` stating what it pins. Runners are strict: unknown fields, another version, and a file or transcript without a runner fail the run.

| File | Pins |
|------|------|
| `offsets.json` | offset token grammar: JSON string, decimal, no sign or leading zeros, at most 2^64-1 |
| `patterns.json` | subscribe pattern validity |
| `handshake.json` | auth frame and ack decoding, lenient caps, strict session, negotiation, plain vs extended ack |
| `control.json` | frame classification on stream and legacy links; acceptor and dialer handling of every control type; error codes; applying subscribe and unsubscribe |
| `filter.json` | per-link delivery predicate and the gap-marker bypass |
| `resume.json` | acceptor resume decision table |
| `cursor.json` | dialer cursor rules from link-up through event, reset and position frames |
| `inbound.json` | bounded inbound queue: overflow closes and resumes after the last delivered offset |
| `outbox.json` | ordered flush and drop-oldest counting over the send log |
| `event_frames.json` | `offset` / `kitoffset` on event frames: encode, strip, validate on stream links |
| `wire/*.jsonl` | transcripts replayed against a Go acceptor over a real WebSocket |

Conventions:

- Offsets are decimal strings everywhere in vectors, as on the wire.
- JSON outputs compare as values; the plain `auth_ok` ack also compares as bytes.
- Error codes are contract; error messages are not.
- The pure vectors need no sockets and no clock.

## Runners

| Port | Runner |
|------|--------|
| Go | `go/runtime/bus/stream_conformance_test.go`, `stream_wire_conformance_test.go` (`TestStreamConformance*`) |
