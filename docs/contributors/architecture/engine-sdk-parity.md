# Engine SDK polyglot parity

Status: proposed. Records which engine SDK is the reference for a new
port, what a PHP or Rust engine client must cover, and how it is held to
the existing clients.

## Context

`kit serve` is the sidecar that gives any process a local document store
with history and branches, sync remotes, peers, an identity, and an event
stream. Its HTTP and WebSocket protocol is specified in
[`engine-protocol.md`](../../adopters/reference/engine-protocol.md).

Two clients wrap it today:

| Client | Path | Package |
|--------|------|---------|
| TypeScript | [`engine/sdk/ts-kit-engine`](../../../engine/sdk/ts-kit-engine/README.md) | `@hop-top/kit-engine` |
| Python | [`engine/sdk/py-kit-engine`](../../../engine/sdk/py-kit-engine/README.md) | `hop-top-kit-engine` |

These are separate from the library SDKs under `sdk/`. A library SDK is a
port of kit's packages; an engine client talks to a running `kit serve`.
The [parity inventory](../sdk-parity-inventory.md) covers the library SDKs
and lists the engine clients in its last section.

PHP and Rust adopters have no engine client. A tool in either language
that wants the engine must write its own HTTP and WebSocket client against
the protocol document, and each such client drifts from it separately.

The library SDKs already show that a four-language surface is workable:
the telemetry stack ships in Python, TypeScript, Rust and PHP, held
together by a shared harness (see [`sdk-support-tiers.md`](../sdk-support-tiers.md)).

## Decision

1. TypeScript and Python are the reference engine clients. A PHP or Rust
   client follows them: it ports their surface and adds nothing of its own.
2. `kit serve` is the authority on wire behaviour. Where a reference
   client and the server disagree, the protocol document's own precedence
   rules apply, and the client is the one that changes.
3. No new engine feature lands as part of this work. A feature the
   reference clients lack is added to them first, then ported.
4. PHP and Rust clients join the existing engine parity test. A client
   that is not in that test is not released.

## Scope

The surface to port is what both reference clients ship today. Where they
differ, the intersection is required and the extras are optional, noted in
the table.

| Surface | Required methods | Notes |
|---------|------------------|-------|
| `KitEngine` | start (spawn), connect, stop; `port`, `pid`, `token`, shutdown token; accessors for the clients below | Spawns `kit serve --port 0`, reads the startup JSON line (`port`, `pid`, `token`, `shutdown_token`), checks `/health` |
| `Collection<T>` | create, get, list (limit, offset, sort, search), update, delete, history, history with topology, revert, branches (with `live`), fork, merge, prune, abandon | Same in both reference clients |
| `EventStream` | subscribe to `/events`, handler per event type, close | The TypeScript client reconnects after one second; the Python client takes a topic argument. Reconnect and topic filtering are not yet common, see open points |
| `SyncClient` | add remote, remove remote, status | Python also exposes push and pull of diffs; optional |
| `PeerClient` | list, trust, block | |
| `IdentityClient` | public key, verify | Python also exposes `whoami`; optional |

Binary handling is separate. The reference clients locate or download a
`kit` binary; how a PHP or Rust client does that is part of its design
review, not this decision.

## Wire compatibility

A new client sends and accepts the same bytes as the reference clients
against the same server. The engine parity test is the check:
[`engine/sdk/parity/engine_sdk_parity_test.go`](../../../engine/sdk/parity/engine_sdk_parity_test.go)
builds `kit`, starts one `kit serve`, runs a scripted scenario through each
client, and fails if the TypeScript and Python results differ (`make
test-parity-engine`).

Extending it means:

- a PHP runner and a Rust runner that run the same scenario;
- the comparison widened to all four results, against the one server
  instance;
- CI installing the PHP and Rust toolchains for that job, with no skip
  when either is missing.

## Language idioms

The client follows the host language rather than mirroring the
TypeScript shape.

| | PHP | Rust |
|-|-----|------|
| HTTP | a PSR-18 client | `reqwest` |
| WebSocket | `ratchet/pawl` or `amphp/websocket` | `tokio-tungstenite` |
| Packaging | Composer package `hop-top/kit-engine` | crate `hop-top-kit-engine` |
| Optional parts | event stream is an optional dependency | Cargo features: default is HTTP only; an `events` feature pulls the WebSocket dependencies |

The library and package names are proposed. Each library is subject to the dependency
review every new dependency gets.

## Publishing

PHP goes to Packagist and Rust to crates.io, on the prerelease line the
PHP and Rust library SDKs use today (`0.5.0-alpha.N`; Composer rejects the
`experimental` identifier). Both stay experimental under
[`sdk-support-tiers.md`](../sdk-support-tiers.md) until they clear its
checklist.

The existing engine clients are not release-please components; they take
the `kit` pin from the root package, as described in
[`RELEASING.md`](../../../RELEASING.md#the-engine-sdks-kit-pin). Whether
the new clients become components of their own or share that pin is open.

## Alternatives considered

| Option | Why not |
|--------|---------|
| Leave PHP and Rust adopters to write clients | Every adopter re-derives the protocol and drifts from it; the cost is paid once per tool |
| Generate clients from the protocol document | The protocol document is prose, not a schema. A schema is a larger change than the clients |
| Make the Go engine client the reference | There is none: Go code uses the engine packages in process |

## Consequences

- PHP and Rust adopters get a client that tracks the server.
- Each new client adds a runner to the engine parity test and a toolchain
  to its CI job.
- A change to the engine protocol is tested through four clients, so a
  protocol change costs more. The protocol document remains the place to
  decide it.
- The engine clients and the library SDKs stay separate packages with
  separate names.

## Open points

- The reference clients differ on the event stream: reconnect (TypeScript
  only) and a topic argument (Python only). They need a common contract
  before a third and fourth client copy one of them.
- TypeScript lacks sync push and pull and identity `whoami`, which Python
  has. Decide whether to add them to TypeScript or leave them Python-only.
- Whether the new clients are release components, or share the root `kit`
  pin.
- Which WebSocket library each language uses, pending dependency review.
