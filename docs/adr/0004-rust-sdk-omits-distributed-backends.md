# ADR 0004: Rust SDK omits distributed backends

- **Status**: Accepted
- **Date**: 2026-07-28

## Context

The Go runtime is kit's canonical implementation; the Rust SDK ports
selected subsystems from it. Several of those subsystems ship, in Go,
with backends that reach across a process or machine boundary:

- `go/runtime/bus` has a `NetworkAdapter`: a WebSocket peer relay with
  reconnect/backoff, star topology, and an auth handshake, plus a
  SQLite-backed adapter for durable local fan-out.
- The kv subsystem has etcd and TiDB backends alongside its local ones.
- The blob subsystem has an S3 backend alongside local filesystem
  storage.

Porting a subsystem does not oblige us to port every backend behind it.
Each of the above is a substantial piece of machinery — connection
lifecycle, retry and backoff policy, partial-failure semantics,
credential handling, wire-protocol compatibility with the Go side — and
each has to be kept correct in a second language, in a second concurrency
model, indefinitely.

The question is what the Rust SDK owes its consumers today. The answer,
for all of the above: nothing. No Rust consumer talks to a bus peer, an
etcd cluster, a TiDB node, or an S3 bucket. The first Rust consumer is a
single-user CLI that publishes a handful of in-process events per
invocation and exits.

## Decision

The Rust SDK ports the **core** of these subsystems and omits their
distributed backends.

For the bus, that means: the `Topic` type and its wildcard matching, both
topic validators, the `Event` envelope, `Qualifiers`, and in-memory
publish/subscribe dispatch are ported. The `NetworkAdapter` and the
SQLite adapter are not. The same reasoning excludes kv's etcd and TiDB
backends and blob's S3 backend.

This is a scope decision, not a portability judgement. S3 in particular
is cleanly portable and has good Rust client libraries; it is simply
unnecessary today, and unnecessary code is a maintenance liability rather
than an asset.

Should cross-process eventing become a real Rust requirement, the
preferred design is a Unix-domain-socket or SQLite-backed adapter rather
than a reimplementation of the WebSocket star topology. Both are
dramatically simpler, both suit the single-machine multi-process shape
that an actual Rust requirement would most likely have, and neither
carries the auth-handshake and reconnect surface that the network
adapter needs in order to be safe over a real network. A demonstrated
need, from a named consumer, should precede any such work.

The Go implementation remains canonical for all of these subsystems.
Cross-language parity is defined at the data seams — topic notation,
envelope JSON shape, validation rules — not at the transport layer.

## Consequences

- The Rust `bus` feature stays dependency-light: it adds no crates
  beyond `serde` and `serde_json`, which the SDK already carries. No
  async runtime, no WebSocket stack, no TLS stack, and therefore no
  corresponding audit or upgrade burden.
- A Rust process cannot participate in a bus peer network, and a Rust
  process cannot reach etcd, TiDB, or S3 through kit. Adopters needing
  any of those must use the Go implementation, or reach the service
  through its own client library outside kit.
- Events published from Rust remain wire-compatible with Go subscribers
  by construction: the envelope is the same JSON shape, so a future
  adapter can be added behind the existing publish surface without
  changing what publishers or subscribers see.
- Rust dispatch is synchronous, which is coupled to this decision: the
  goroutine-pool machinery in the Go bus exists largely to serve
  concurrent delivery in long-lived server processes, the same class of
  consumer the omitted backends serve. Neither is a fit for the Rust
  consumer profile today. See the `bus::mem` module documentation in the
  Rust SDK for the dispatch rationale in full.

## Acknowledged quirks

- Two topic validators exist in Go and both are ported, with different
  strictness: the published-topic contract (4 segments, `^[a-z][a-z0-9_]*$`,
  length cap, wildcards rejected) and the construction-time convention
  (additionally requiring a past-tense action segment). They are not
  redundant; the stricter one guards topic-map construction so
  misconfigured prefixes fail during wiring rather than at runtime.
- The past-tense whitelist is a hand-maintained list of irregular verbs
  and participles. It is duplicated in the Rust port rather than derived,
  so adding a verb means editing both languages. This is accepted: the
  list changes rarely, and a shared data file would cost more machinery
  than it saves.
- The Go envelope types its payload as `any`, so in-process Go
  subscribers see the original value while cross-process subscribers see
  the JSON-decoded form. Rust has no equivalent erasure that survives a
  wire hop, so the Rust payload is a `serde_json::Value` unconditionally.
  Rust in-process and cross-process subscribers therefore see the same
  shape — a small divergence from Go, in the direction of fewer
  surprises.
