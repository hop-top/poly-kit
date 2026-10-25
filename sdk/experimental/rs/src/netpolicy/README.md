# netpolicy

## What it answers

May this process reach the network right now? It carries the `--offline`
marker, decides which hosts and dials it permits, and owns the reqwest
client construction path that enforces it for user traffic. Response
caching is `hop_top_kit::httpcache`; the JSON client is `hop_top_kit::api`.

## Use it when

- a CLI registers `--offline` → `NetPolicy::new(offline)`, threaded to every client it builds
- you send HTTP from this crate → `GuardedClient::new(policy)` (feature `api`), never a bare `reqwest::Client`
- you open your own socket (websocket, SMTP, SQL, raw TLS) → `policy.check_dial(network, addr)?` before dialing
- you need the decision for a host alone → `policy.allows_host(host)` or `is_loopback(host)`
- a caller must tell a refusal from a transport failure → `NetError::as_offline()`, or match `OfflineError`

## Quick start

Verified by `tests/netpolicy_core.rs` (`check_dial_table`):

```rust
let off = NetPolicy::offline();
assert_eq!(off.check_dial("tcp", "localhost:25"), Ok(()));
let err = off.check_dial("tcp", "db.example.com:3306").unwrap_err();
assert_eq!(
    err.to_string(),
    "dial tcp db.example.com:3306: network disabled by --offline"
);
```

## Contract

- Feature `netpolicy` has no dependencies and builds on wasm32: `NetPolicy`, `is_loopback`,
  `check_dial`, `OfflineError`, `OFFLINE_MESSAGE`. Feature `api` enables it and adds
  `GuardedClient`, `RequestBuilder`, `NetError`, `NetPolicy::allows`.
- Default policy is online. Loopback (`localhost` exact, 127.0.0.0/8, `::1`, v4-mapped) and
  unix sockets stay reachable offline; DNS names never count as loopback.
- A portless bracketed host is loopback only when the brackets hold a loopback IPv6 address.
- Refusals name method (or `dial <network>`), scheme, host with port and path only; never query,
  fragment or userinfo.
- Not covered: a `reqwest::Client` built outside this module, and sockets whose caller skips
  `check_dial`. The telemetry HTTPS sink is deliberately unguarded (logging-class egress).
- Parity: [`contracts/netpolicy-v1`](../../../../../contracts/netpolicy-v1/README.md), run by
  `tests/netpolicy_conformance.rs`.

## Neighbours

- `hop_top_kit::api` (src/api.rs): the JSON client, issued through `GuardedClient`
- `hop_top_kit::httpcache` (src/httpcache/): send from inside `fetch` with a `GuardedClient`
- `hop_top_kit::telemetry` (src/telemetry/): builds its own client, not refused offline

## See also

- [`go/core/netpolicy`](../../../../../go/core/netpolicy/README.md), the Go reference
- [cli-parity-guide.md](../../../../../docs/adopters/guides/cli-parity-guide.md), "Global Flags"
