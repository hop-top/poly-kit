# parity

## What it answers

What must not drift between the Go, TypeScript and Python ports (and, for
the MCP wire, Rust and PHP): cross-language TUI constants and symbols, the
scope defaults, the serve lifecycle conformance record and the MCP wire
fixture. Language-specific API descriptions do not belong here; they name
symbols only one port has, so no other port can drift from them.

## Use it when

- you add a constant all ports must agree on → add a `parity.json` block
  plus a loader field in every port (Go `parity.Data` + `parity.Blocks`,
  TS `ParityData` + `PARITY_BLOCKS`, Python accessor + `BLOCKS`)
- you ship a `serve` behavior in a port → flip its row in `serve.json`
  from `PENDING` to `SHIPPED`
- you change the Go MCP surface → regenerate
  `sdk/tests/cross-lang/fixtures/mcp-wire.json`
- you add a sibling contract file with its own loader → register it in
  `extends`

## Quick start

```bash
go test ./go/transport/cmdsurface/ -run TestGenerateMCPWireFixtures \
    -update-mcp-fixtures
```

Regenerating re-baselines every other port against new Go behavior; a diff
in the fixture must be explainable as an intended Go-side change.

## Contract

| File | Holds | Guard |
|------|-------|-------|
| `parity.json` | blocks `status`, `spinner`, `anim`, `help`, `verbosity`, `streams` | `TestParityNoUnloadedBlocks`, `TestParityLoadedBlocksNonZero`, `sdk/ts/src/tui/parity.test.ts`, `sdk/py/tests/test_parity.py` |
| `scope-defaults.json` | default deny patterns for the scope packages | `TestScopeDefaultsContractSync`, `TestScopeDefaultsRegistered` |
| `serve.json` | serve lifecycle `constants`, `behaviors`, per-port `ports` status | `TestServeContractMatchesGo` (pinned against `go/console/serve`) |
| `sdk/tests/cross-lang/fixtures/mcp-wire.json` | MCP wire bytes: 18 `cases`, 1 `sequences` entry with 5 steps | `make test-parity-mcp` (five runners) |

- All three ports read `parity.json` from this directory (Go `//go:embed`,
  TS bundler inlining, Python `hop_top_kit.parity`); there is no copy.
- `extends` is a test-suite registry, not a loader directive: nothing
  merges a listed file into `parity.Values` at runtime.
- `PENDING` in `serve.json` passes; `PENDING` under `go`, a missing
  behavior key, or an unknown status fails.
- MCP responses are compared as bytes (sorted keys, trailing newline), and
  both `cases` and `sequences` must run.
- Styled tables (`go/console/output/tablestyle.go`) are Go-only prose, not
  a block.

## Neighbours

- `go/console/tui/styles`, `sdk/ts/src/tui`, `sdk/py/hop_top_kit/tui`:
  consumers of the loaded blocks
- `go/console/cli/scope`: consumer of `scope-defaults.json`
- `go/console/serve`: reference implementation `serve.json` is pinned to
- `go/transport/cmdsurface`, `sdk/*/mcp`: MCP wire implementations

## See also

- [Parity contracts reference](../../docs/adopters/reference/parity-contract.md):
  full record of every file, guard and defect class
- [CLI parity guide](../../docs/adopters/guides/cli-parity-guide.md)
- [Serve lifecycle contract](../../docs/contracts/serve-lifecycle.md)
- [Serve MCP from any SDK](../../docs/adopters/guides/serve-mcp-from-any-sdk.md)
