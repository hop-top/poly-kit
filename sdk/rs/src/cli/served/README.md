# cli/served

## What it answers

What does a kit-rs transport (socket, api, MCP) ask before it runs a command for a remote caller?
`ServedGate` (in `../served.rs`) ports Go's served permission slot in Go's order: the policy
engine's caller verdict with a read-only budget check, then `permissions:` rules, then the
adopter's check, then the budget charge. The first refusal stands, and a refused call spends
nothing.

## Files

| File | What it holds |
|------|---------------|
| `../served.rs` | `ServedGate`, the decider order, `with_usage_store` / `with_usage_file` |
| `ledger.rs` | `UsageLedger`, the `UsageStore` trait, `MemoryUsageStore` (default), `KvUsageStore` (feature `kv`) |
| `ledger/tests.rs` | ledger unit tests: stores, file reopen, Go key format, expiry |
| `tests.rs` | gate unit tests: decider order, caller mapping, budgets spent only on admitted calls |

## See also

- [cli README](../README.md): the command tree the gate guards
- [Served middleware reference](../../../../../docs/adopters/reference/served-middleware.md): the contract across SDKs
- [Serve lifecycle, `services.usage_store`](../../../../../docs/contracts/serve-lifecycle.md): where budgets are counted
