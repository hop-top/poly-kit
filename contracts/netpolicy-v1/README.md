# netpolicy-v1

## What it answers

What the `--offline` policy and the `netpolicy.allow_hosts` allowlist in `kit/core/netpolicy` must decide, independent of language: which hosts are loopback, which hosts an allowlist admits, which dials are refused, which request URLs pass an offline guard, which headers survive a cross-origin redirect, and the exact refusal texts. Wrong place for transport wiring (`Install`, `ObservabilityTransport`, the reqwest client builder, `NewClient`'s construction); those are per-runtime and have no vectors.

## Use it when

- you port or change loopback classification, allowlist matching, `CheckDial` or the refusal text in any SDK: run both runners
- you change netpolicy behaviour in Go: add a vector first, then make every port pass it

## Quick start

```sh
make test-parity-netpolicy
# or separately:
go test ./go/core/netpolicy/ -run '^TestConformanceNetpolicy'
cargo test --features api --test netpolicy_conformance   # from sdk/rs
```

## Contract

Go is canonical. The vectors are the source of truth: a runner failure means the implementation drifted, not that the vector should be edited. The file carries `version: "v1"` and a `description` stating the rules it pins.

| Section of `vectors.json` | Pins |
|---------------------------|------|
| `offline_message` | the sentinel text every refusal ends with (`ErrOffline`) |
| `is_loopback[]` `{host, loopback}` | host with or without a port; `localhost` exact (case-sensitive); 127.0.0.0/8, `::1`, v4-mapped loopback; DNS names remote; a portless bracketed host is loopback only when the brackets hold a loopback IPv6 address (`[::1]` yes, `[127.0.0.1]` no) |
| `check_dial[]` `{offline, network, addr, error}` | the dial decision; `unix`, `unixgram`, `unixpacket` exempt (exact, lower case); `error` is the exact refusal `dial <network> <addr>: network disabled by --offline`, empty when permitted |
| `url_allows[]` `{url, allowed}` | whether an offline guard lets a request to `url` through |
| `refusal[]` `{method, url, error}` | the exact offline refusal of a request: method, scheme, host with port and path; never query, fragment or userinfo |
| `allow_message` | the sentinel text every allowlist refusal ends with (`ErrHostNotAllowed`) |
| `host_allowed[]` `{allow_hosts, host, allowed}` | allowlist matching: exact entries and `*.suffix` (one or more labels below the suffix; never the suffix itself, never an IP literal); lower case, trailing dot, IDNA, ports and IP spelling normalized; loopback always passes; an empty list restricts nothing; invalid entries never match |
| `allow_check_dial[]` `{allow_hosts, network, addr, error}` | the dial decision under an allowlist; `error` is the exact refusal `dial <network> <addr>: host not allowed by netpolicy.allow_hosts`, empty when permitted |
| `allow_url[]` `{allow_hosts, method, url, error}` | the exact allowlist refusal of a request, empty when it passes |
| `redirect_message`, `redirect_headers[]` `{headers, cross_origin, refused}` | a guarded client follows a redirect to another origin (scheme, host or port) only when the request carries nothing beyond `Accept`, `Accept-Encoding`, `Accept-Language`, `User-Agent`, `Content-Type`; any other header (`Authorization`, `Proxy-Authorization`, `Cookie`, `X-Api-Key`, custom) refuses the hop before anything reaches the second origin, with a typed error ending in `redirect_message`. Same-origin hops forward every header |

Runners are strict: unknown fields, a `version` other than `v1` and a corpus file no runner consumes all fail the run.

### Known URL-layer differences

Not vectored, because the URL parsers differ before the policy runs. Go keeps `URL.Host` verbatim; Rust's url crate normalises it first. So offline Rust lets `http://LOCALHOST/`, `http://127.1/` and `http://0x7f.0.0.1/` through (they normalise to loopback) where Go refuses them. Only real loopback is reached either way. The url crate also drops a scheme's default port and adds a `/` path to a bare authority, so `https://example.invalid` is named `https://example.invalid/` in a Rust refusal and `https://example.invalid` in Go's.

## Runners

| Port | Runner |
|------|--------|
| Go | `go/core/netpolicy/conformance_test.go` (`TestConformanceNetpolicy*`) |
| Rust | `sdk/rs/tests/netpolicy_conformance.rs` (`url_allows`, `refusal`, `redirect_headers` need feature `api`) |

## See also

- [`go/core/netpolicy/netpolicy.go`](../../go/core/netpolicy/netpolicy.go), [`dialer.go`](../../go/core/netpolicy/dialer.go)
- [`sdk/rs/src/netpolicy/core.rs`](../../sdk/rs/src/netpolicy/core.rs)
