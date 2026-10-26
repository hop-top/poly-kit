# bus/network

## What it answers

How does a bus share events with peers in other processes, Go or Rust? `NetworkAdapter` is Go's
`NetworkAdapter` over a native WebSocket: local events go to every linked peer, peer events are
published locally, and origin ids stop loops. In-process dispatch is the parent `bus` module; the
offline marker is `hop_top_kit::netpolicy`.

## Use it when

- a bus should dial peers at build time → `DynBus::builder().with_network(addrs)`, options via `network_options`
- you dial or serve by hand → `NetworkAdapter::new(bus.clone(), NetworkOptions::new())`, then `connect`, `dial` or `listen`
- your HTTP framework owns the socket → `check_upgrade(&req)`, send `Upgrade::response()`, then `accept_upgraded(io, addr)`
- you serve with axum → `any(net.axum_handler())` (feature `bus-network-axum`)
- peers must prove a shared secret → `NetworkOptions::auth(StaticTokenAuth::new(t))`, or `auth_from_env(&names)`
- a hub bridges several peers → `NetworkOptions::relay(true)`; only some topics may leave → `TopicFilter`
- you run your own tokio runtime → `TokioSpawn` is the `Spawn` for `subscribe_async`

## Quick start

Verified by `tests/bus_network_e2e.rs` (`e2e_bidirectional`):

```rust
let a = NetworkAdapter::new(bus_a.clone(), NetworkOptions::new().origin_id("A"));
let addr = a.listen("127.0.0.1:0")?;
let b = NetworkAdapter::new(bus_b.clone(), NetworkOptions::new().origin_id("B"));
b.connect(&Context::background(), &format!("ws://{addr}/"))?;
bus_a.publish(&Event::new("crm.sales.deal.created", "crm", json!({"id": 1})))?; // reaches bus_b
```

## Contract

- Feature `bus-network`: tokio (current-thread runtime on one `kit-bus-network` thread), tokio-tungstenite,
  rustls on ring with the platform roots. Native only; the wasm32 core (`bus`, `netpolicy`) pulls none of it.
- Wire: Go's links without streams. Auth frame `{"auth":"<token>"}`, ack exactly `{"type":"auth_ok"}`,
  kit event frames out, kit or CloudEvents frames in. Pinned by
  [`contracts/bus-v1/streams/wire/plain_ack.jsonl`](../../../../../../contracts/bus-v1/streams/wire/README.md).
- Connect checks the context's `NetPolicy` before dialing; offline is remembered for reconnects.
- Auth ack within 10 s; refusals close 1008, internal failures 1011. Writes time out after 5 s; three
  failures in a row evict a link. Reads are limited to 32768 bytes (1009). Close sends 1001.
- A dropped outbound link reconnects (100 ms to 30 s jittered, 10 s per attempt, one loop per address)
  until connected, `disconnect`, or `close`. A failed `connect` starts no loop; `dial` does.
- Blocking methods panic on the network thread; use the `_async` twins from async code.
- Not yet ported: streams (resume, subscriptions, outbox), `kit.runtime.link.*` events, CloudEvents
  outbound frames, the error callback, the browser transport.
- Parity: Go's network tests ported by name in `tests/bus_network*.rs`.

## Neighbours

- `hop_top_kit::bus` (src/bus/): the bus this adapter bridges; `DynBus` composes it
- `hop_top_kit::bus::stream` (src/bus/stream/): the I/O-free stream core this transport will drive
- `hop_top_kit::netpolicy` (src/netpolicy/): the offline policy `connect` enforces

## See also

- [`go/runtime/bus`](../../../../../../go/runtime/bus/README.md), the Go reference
- [cross-lang harness peer](../../../../../tests/cross-lang/bus-network/gonode/main.go)
