# parity

Cross-language TUI constants and symbols.

## What belongs in `parity.json`

`parity.json` is a loaded contract, not documentation. A block belongs here
only when both hold:

1. Its values are **cross-language constants** — Go, TypeScript and Python
   must all agree on them, and a drift between ports is a bug.
2. A **loader field exists** for it. In Go that means a field on
   `parity.Data` plus an entry in `parity.Blocks`; `TestParityNoUnloadedBlocks`
   fails when a block is declared without one.

Language-specific API descriptions do not qualify — they name symbols only
one port has, so no other port can drift from them. Document those in prose
here instead.

## Declared blocks

| Block | Loaded by | Consumed at runtime |
|-------|-----------|---------------------|
| `status` | Go, TS, Python | Go `tui/styles`, TS `tui/status.ts`, Python `tui/status.py` |
| `spinner` | Go, TS | TS `tui/spinner.ts` |
| `anim` | Go, TS, Python | TS `tui/anim.ts`, Python `tui/anim.py` |
| `help` | Go, TS, Python | Go `console/cli`, TS `cli.ts`, Python `cli.py` |
| `verbosity` | Go | not yet wired — values still hardcoded per port |
| `streams` | Go | not yet wired — values still hardcoded per port |

`verbosity` and `streams` are loaded but not yet read by the runtime. Each
port currently hardcodes the same values (`-V` count → info/debug/trace,
`--quiet` → warn; `--stream` writing `[name] ` prefixed lines to stderr).
Loading them first makes the contract honest and gives the ports a single
value to migrate onto; wiring each port to read from the loader is follow-up
work.

## Styled tables (Go only — prose, not a loaded block)

The `output` package's styled-table behavior used to live in `parity.json` as
a `table` block. It was never a parity contract: every key named a Go
identifier with no TypeScript or Python counterpart, so no other port could
drift from it. It is recorded here instead.

- Plain `tabwriter` is the default renderer.
- Callers opt into the lipgloss-backed renderer via `WithTableStyle`.
- The styled path is gated on writer-is-TTY, so non-TTY writers (pipes,
  files, tests) always emit ANSI-free, diff-friendly output.
- `RowEmphasis` marks a row index with one of `none`, `primary`,
  `secondary`, `muted`. It is a no-op on the plain path.
- Default border style is `normal`.

See `go/console/output/tablestyle.go` for the implementation.

## `extends`

`extends` lists sibling contract files that the parity test suite also
covers. It is a **test-suite registry, not a loader directive** — nothing
merges the referenced file into `parity.Values` at runtime. Each listed
contract carries its own loader and its own sync test; for
`scope-defaults.json` those are the per-language scope packages and
`TestScopeDefaultsContractSync` / `TestScopeDefaultsRegistered`.
