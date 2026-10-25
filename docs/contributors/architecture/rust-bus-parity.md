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
- domain state machine
- full config (not a bus-only subset)
- a browser WebSocket transport

Out of scope: the SQLite bus adapter. Nothing in the consumer needs it,
and it stays unported.

## Feature layout

- The wasm-safe core (topics, envelope, mode, builder, thread-safe
  dispatch, retry policy, netpolicy core) keeps building for
  `wasm32-unknown-unknown` with `--no-default-features --features bus`.
  It takes no runtime or socket dependency.
- Tokio and native socket pieces (async dispatch pool, native network
  adapter) sit behind their own features. Enabling `bus` alone never
  pulls them in.
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
  the async drain that follows; that drain is empty until async
  dispatch lands. When close returns, no sync handler is running.
  With the context already done and nothing left to drain, Go's
  `select` returns nil or `ctx.Err()` at random; Rust returns `Ok`.
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
  `sdk/experimental/rs` until the parity work is complete.
