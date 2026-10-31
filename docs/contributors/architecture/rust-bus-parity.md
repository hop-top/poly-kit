# kit-rs bus parity

Status: accepted. Supersedes, in part, the earlier decision to port only
the core of `go/runtime/bus` to Rust with synchronous dispatch.

## Context

`hop_top_kit::bus` began as a port of the in-process core of
[`go/runtime/bus`](../../../go/runtime/bus/README.md): the topic type,
the two validators, the event envelope, qualifiers, and synchronous
in-memory dispatch. The module docs recorded that as a deliberate scope
cut, on the grounds that no Rust consumer needed the WebSocket
`NetworkAdapter`, the async handler pool, or a SQLite adapter.

That premise no longer holds. A networked Rust consumer now needs kit's
bus over the network: an embedding desktop shell, on desktop and mobile,
whose Rust core also builds for `wasm32-unknown-unknown` and runs in a
browser. It is multi-threaded, publishes from several threads, talks to
Go peers, and must keep working across disconnects. The core-only port
cannot serve it.

## Decision

kit-rs reaches behavioural parity with Go for everything that consumer
needs. Go stays canonical: Rust matches Go's behaviour, error texts and
wire format, verified by shared vectors under `contracts/` wherever they
exist.

In scope:

- network adapter (peer relay, star topology, auth handshake)
- async dispatch with a bounded pool and a draining `close`
- thread-safe bus core (`Send + Sync`)
- adapter seam
- sinks
- builder
- enforcement mode parsed from string, env and config
- topic builder and parse
- event envelope
- retry and backoff
- netpolicy core, without `reqwest`
- CloudEvents shaping
- stream semantics: remote subscribe, resume offsets, offline buffering
- acknowledged publishes, dialer side: `caps.acks`, the `puback` frame,
  the ack tracker and the rewind on reconnect. The acceptor side
  (pubacks, the dedupe memory and its principal bound) waits for a
  stream acceptor, as the stream wire transcripts do
- domain state machine and repository port
- full config (not a bus-only subset)
- a browser WebSocket transport

Out of scope: the SQLite bus adapter. Nothing in the consumer needs it,
and it stays unported.

## Feature layout

- The wasm-safe core (topics, envelope, mode, builder, thread-safe
  dispatch, retry policy, netpolicy core) keeps building for
  `wasm32-unknown-unknown` with `--no-default-features --features bus`.
  It takes no runtime or socket dependency.
- Tokio and native socket pieces sit behind their own features
  (`bus-network`, `bus-network-axum`, `bus-network-webpki-roots`); the
  async dispatch pool itself needs no runtime. Enabling `bus` alone
  never pulls them in.
- The browser WebSocket transport sits behind its own feature, separate
  from the native socket feature, so a wasm build selects exactly one
  transport.
- The crate default feature set stays empty.

## Thread-safe core

`SharedBus` matches Go's `memBus` on results but not on mechanism. Go
runs sync handlers under a read lock, and `Unsubscribe` and `Close`
take the write lock. Rust snapshots the matching subscriptions under a
mutex, counts the publish in flight, and runs handlers with the mutex
released. Unsubscribe and close wait on that count.

- Publish checks the topic, then the closed flag, then dispatches. It
  never reads its `Context`; handlers receive it, as in Go.
- Close marks the bus closed, then waits for in-flight sync publishes
  with no deadline, which is Go's `mu.Lock()`. The context bounds only
  the async drain that follows: on expiry close returns Go's
  `ctx.Err()` text and the bus stays closed. When close returns, no
  sync handler is running. With the context already done and nothing
  left to drain, Go's `select` returns nil or `ctx.Err()` at random;
  Rust returns `Ok`.
- Subscribe after close registers nothing and drops the handler; Go
  appends it to the closed bus. A handler holding a clone of the bus
  would otherwise keep it alive through a cycle.
- Unsubscribe waits for the in-flight publishes that matched the
  subscription, not for every in-flight publish as Go's lock does. It
  waits even when it returns `false` because close or another
  unsubscribe removed the subscription first, since Go's closure takes
  the lock either way. It gives Go's guarantee: on return, the handler
  is neither running nor about to run. It can return while an
  unrelated handler is still running, which Go's cannot.
- Re-entrancy, where Go deadlocks on everything but a nested publish:
  - Waits skip the calling thread's own in-flight publishes.
  - Unsubscribe from a handler does not wait for other threads. It takes
    effect at once, so no invocation starts afterwards on this thread or
    on another thread that has not reached the handler yet. A
    dispatching thread therefore never blocks in unsubscribe, and
    handlers removing each other cannot deadlock.
  - Close from a handler waits for other threads' sync publishes, so two
    handlers closing the bus at once deadlock, as in Go.
- On `wasm32-unknown-unknown` there is one thread, so these waits never
  block, and a `Context` has no deadline because there is no clock.

Async dispatch (`SharedBus::subscribe_async`, Go's `SubscribeAsync`)
is runtime-free: the caller passes a `Spawn`, which puts each handler
task on its own executor. A tokio adapter can follow behind a feature.

- Async handlers are launched after every sync handler of the publish
  succeeded, in subscription order, with the publish context. They
  return nothing, so they never veto or fail a publish, as in Go.
- The pool is a counter under the bus mutex, sized by `max_async`
  (Go's `WithMaxAsync`, default 256, zero meaning the default). A
  publish takes slots after its sync phase has left the drain count,
  so close never waits on a publisher stuck on a full pool, which is
  Go's fix for priority inversion. While the pool is full the
  publisher blocks, as in Go.
- With a slot taken the publish re-checks closed and, on a closed bus,
  launches nothing more and returns `Ok`. Only then is the task
  counted, which is Go's `wg.Add` after the slot. A task stays counted
  until it finishes or the executor drops it.
- Deviations: a publisher blocked on a full pool returns as soon as
  close starts, not when a slot frees; once unsubscribe returns no new
  task is admitted for that subscription, where Go may still launch
  one; close from an async handler does not wait for that handler.
- On `wasm32-unknown-unknown` the pool is uncapped, and close cannot
  block for tasks that run only once the caller yields: with tasks
  still running and a live context it returns `BusError::NotDrained`.
  The target only selects this single-threaded policy, so native unit
  tests run it; `make test-rs-wasm` runs a smoke test on wasm32 under
  Node with `wasm-bindgen-test`.

## Network adapter

`bus::network` (feature `bus-network`) ports Go's `NetworkAdapter` on
links without streams; Go peers that speak streams fall back to that
link with a Rust peer, in either role.

- One process-wide current-thread tokio runtime on a dedicated thread
  carries every adapter's sockets. Blocking methods hand work to it and
  wait, so they panic when called on it; `_async` variants serve async
  callers. Inbound publishes, authenticators and the error callback run
  on tokio's blocking pool, never on the socket thread.
- Outbound forwarding is the async `#` subscriber, as in Go before
  streams. The stream port replaces it with a log append and a writer
  per link, behind the same public API.
- TLS is rustls on ring with the platform's roots; Android callers pass
  a `rustls::ClientConfig` (or opt into Mozilla's roots), since rustls
  reads no root store there.
- Cross-language tests run Go and Rust hubs, clients and mixed relays
  (`make test-parity-bus-network`).

## Go-first rule

Two pieces exist in the target behaviour but not in Go today:
CloudEvents shaping, and stream semantics (remote subscribe, resume
offsets, offline buffering). They land in Go first, with conformance
vectors under `contracts/`. Rust then ports them against those same
vectors. Rust never defines wire behaviour that Go lacks.

## Consequences

- Rust and Go bus behaviour is held together by a shared conformance
  corpus, with a runner on each side.
- `bus::mem` no longer documents synchronous, single-threaded dispatch
  as a final decision. Async delivery lands behind its feature;
  synchronous in-memory dispatch remains the default for the
  wasm-safe core.
- Docs that said "core only; sync dispatch" or "network adapter not
  ported" are corrected as each piece lands.
- The consumer-facing surface grows; it stays experimental under
  `sdk/rs` until the parity work is complete.
