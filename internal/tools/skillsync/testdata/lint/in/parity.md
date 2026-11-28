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
| Go | 4 done |
| TypeScript | 3 done, 1 none |
| Python | 1 done, 3 none |
| Rust | 1 done, 1 none, 2 n/a |
| PHP | 1 partial, 2 none, 1 n/a |

## CLI and console

| Capability | Go | TS | Py | Rust | PHP | Evidence |
|---|---|---|---|---|---|---|
| CLI factory | done | done | done | done | partial | make test-parity-cli |
| Logging | done | done | none | n/a | none | ts:log.ts; no py module |

## Serving and transports

| Capability | Go | TS | Py | Rust | PHP | Evidence |
|---|---|---|---|---|---|---|
| Serve lifecycle | done | done | none | none | none | serve.json: 13 behaviors |
| Job queue | done | none | none | n/a | n/a | no module in any SDK |
