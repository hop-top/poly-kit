# webhook-v1

## What it answers

What an inbound webhook receiver must do for the three sender shapes kit supports (Microsoft Graph, Pipedrive v2, generic HMAC): status, error code, published topic, key and encoding for each request of a scripted sequence, and the exact digests an HMAC signature has. Wrong place for transport details such as listeners and TLS.

## Use it when

- you port `go/transport/webhook` to a new SDK: run every case and digest here
- you change a refusal code, a topic, the dedupe key or the replay rule: this file changes in the same commit

## Quick start

```sh
make test-parity-webhook
```

## Contract

`vectors.json` holds:

- `hmac_vectors`: expected signature headers for a secret built at runtime from `secret_parts` (joined; never written whole), over several hash, encoding, prefix and timestamp shapes. Computed independently of both ports.
- `cases`: per case a `preset` (`graph`, `pipedrive`, `hmac`), its `config`, and a request sequence. A request may be signed (`sign`, `tamper`) or carry `basic` credentials; `{{SECRET}}` is replaced by the runtime secret. `want` states the response (`status`, `code`, `body`, `body_json`, `content_type`) and the cumulative `published` count, plus the last event's `topic`, `key`, `kind` and `encoding`.
- Fixtures hold no provider-format keys: the secret is assembled by the runner.
- The clock is `now`; the idempotency store is in memory with a one-hour TTL; `publish_failures` makes the first N publishes fail.

| Port | Runner |
|------|--------|
| Go | `go/transport/webhook/conformance_test.go` |
| Rust | `sdk/rs/tests/webhook_conformance.rs` (feature `webhook`) |

## Neighbours

- `../parity/api.json`: the REST server the receiver mounts on

## See also

- [Rust SDK reference](../../docs/adopters/reference/rs-sdk.md)
