# cli/served/ledger

## What it answers

Is the usage ledger behind `ServedGate` budgets correct? This directory holds the unit tests for
`../ledger.rs`. The ledger is Go's `cmdsurface.UsageLedger` over a pluggable `UsageStore`, and it
keeps entries in Go's format, `kit/usage/<escaped key>/<window>/<start>` holding
`{"ops":N,"bytes":N}`, so a file written by one SDK reads in the other.

## Files

| File | What it holds |
|------|---------------|
| `tests.rs` | budgets on memory and kv stores, file ledger across reopen, default kind, Go key format and escaping, expiry, `usage_store` kind parsing |

## See also

- [cli/served README](../README.md): the gate that charges the ledger
- [`go/transport/cmdsurface`](../../../../../../go/transport/cmdsurface/README.md), the Go reference
