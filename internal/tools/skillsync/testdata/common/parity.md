# Capability parity

> Per-SDK parity for every capability. Go is the reference.

## Status vocabulary

| Status | Meaning |
|---|---|
| done | A parity contract pins the SDK's behavior to Go |
| partial | The SDK ships a subset |
| none | No module for the capability in the SDK tree |
| n/a | The capability is a language seam with no counterpart |
| unverified | A module exists but nothing pins it to Go |

## Summary

| SDK | Counts |
|---|---|
| Go | 8 done |
| TypeScript | 4 none, 2 done, 1 unverified, 1 n/a |
| Python | 4 none, 2 done, 1 unverified, 1 n/a |
| Rust | 2 done, 2 unverified, 2 none, 1 partial, 1 n/a |
| PHP | 5 none, 1 done, 1 partial, 1 n/a |

## CLI and console

| Capability | Go | TS | Py | Rust | PHP | Evidence |
|---|---|---|---|---|---|---|
| CLI factory | done | done | done | partial | partial | make test-parity-cli; php wires no global-flag layer |
| Logging | done | unverified | unverified | unverified | none | ts:log.ts py:log.py rs:cli/log.rs; no php module |
| Markdown rendering | done | none | none | none | none | no module in any SDK |
| Command annotations | done | n/a | n/a | n/a | n/a | cobra-only seam |

## Serving and transports

| Capability | Go | TS | Py | Rust | PHP | Evidence |
|---|---|---|---|---|---|---|
| Serve lifecycle | done | done | done | done | done | serve.json: 13 behaviors |
| Job queue | done | none | none | none | none | no module in any SDK |
| Served authentication | done | none | none | done | none | make test-parity-authn (Go/Rs); rs:authn/ |
| Unix socket | done | none | none | unverified | none | rs:socket/ (no corpus) |
