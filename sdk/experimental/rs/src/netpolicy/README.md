# netpolicy

## What it answers

May this process reach the network right now, and which hosts? It carries
the `--offline` marker and the `netpolicy.allow_hosts` allowlist, decides which hosts and dials it permits, and owns the reqwest
client construction path that enforces it for user traffic. Response
caching is `hop_top_kit::httpcache`; the JSON client is `hop_top_kit::api`.

## Use it when

- a CLI registers `--offline` → `NetPolicy::new(offline)`, threaded to every client it builds
- you send HTTP from this crate → `GuardedClient::new(policy)` (feature `api`), never a bare `reqwest::Client`
- you open your own socket (websocket, SMTP, SQL, raw TLS) → `policy.check_dial(network, addr)?` before dialing
- a tool must reach only named hosts → `NetPolicy::online().with_allow_hosts(["graph.microsoft.com", "*.pipedrive.com"])`; check the config with `validate_allow_hosts`
- refusals must be recorded → `policy.with_audit(|refusal| ...)`
- you need the decision for a host alone → `policy.allows_host(host)` or `is_loopback(host)`
- a caller must tell a refusal from a transport failure → `NetError::as_offline()` / `as_host_not_allowed()`, or match `OfflineError` / `HostNotAllowedError`

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

- Feature `netpolicy` depends only on `url` (host normalization) and builds on wasm32: `NetPolicy`,
  `is_loopback`, `check_dial`, `OfflineError`, `HostNotAllowedError`, `PolicyError`, `Refusal`,
  `validate_allow_hosts`. Feature `api` enables it and adds
  `GuardedClient`, `RequestBuilder`, `NetError`, `NetPolicy::allows`.
- Default policy is online. Loopback (`localhost` exact, 127.0.0.0/8, `::1`, v4-mapped) and
  unix sockets stay reachable offline; DNS names never count as loopback.
- Allowlist (`with_allow_hosts`, config key `netpolicy.allow_hosts`): entries are an exact host or
  `*.suffix`; the suffix form matches one or more labels below the suffix, never the suffix itself
  and never an IP literal. Hosts and entries compare lower case, without a trailing dot, IDNA in
  ASCII form (through `url`, the parser reqwest sends with). An empty list is no list. Invalid entries
  never match (`validate_allow_hosts` names them). Loopback and unix sockets pass; `--offline` wins
  when both apply. Refusal: `<method> <url>: host not allowed by netpolicy.allow_hosts`. `NetPolicy`
  is `Clone`, not `Copy`.
- Audit: `with_audit` sinks see every refusal, offline and allowlist, with the same stripped
  destination as the error.
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
