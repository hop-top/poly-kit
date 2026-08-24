# parity

Cross-language TUI constants and symbols, plus the other cross-language
contracts recorded on this page: the [MCP wire
contract](#mcp-wire-contract-sdktestscross-langfixturesmcp-wirejson) and
the [styled-table prose](#styled-tables-go-only-prose-not-a-loaded-block).

## What belongs in `parity.json`

`parity.json` is a loaded contract, not documentation. A block belongs here
only when both hold:

1. Its values are **cross-language constants** — Go, TypeScript and Python
   must all agree on them, and a drift between ports is a bug.
2. A **loader field exists in every consuming port**. In Go that means a
   field on `parity.Data` plus an entry in `parity.Blocks`; in TypeScript a
   property on `ParityData` plus an entry in `PARITY_BLOCKS`
   (`sdk/ts/src/tui/parity.ts`); in Python an accessor plus an entry in
   `BLOCKS` (`sdk/py/hop_top_kit/parity.py`). Each port carries its own
   block-registry guard, so a block declared without a loader fails in all
   three:

   | Port | Guard |
   |------|-------|
   | Go | `TestParityNoUnloadedBlocks`, `TestParityLoadedBlocksNonZero` |
   | TypeScript | `sdk/ts/src/tui/parity.test.ts` |
   | Python | `sdk/py/tests/test_parity.py` |

   The guards assert two separate things, and both matter: that the block is
   *known*, and that it *loaded a non-empty value*. A block wired up under a
   mismatched key name (`quiet_override` → `quietOverride`) parses clean and
   leaves a zero value behind — a presence-only check sails straight past it.

Language-specific API descriptions do not qualify — they name symbols only
one port has, so no other port can drift from them. Document those in prose
here instead.

## Declared blocks

Every block below is modelled by all three loaders — that is what the
block-registry guards enforce. "Consumed at runtime" is the narrower
question of which port actually *reads* the loaded value.

| Block | Loaded by | Consumed at runtime |
|-------|-----------|---------------------|
| `status` | Go, TS, Python | Go `tui/styles`, TS `tui/status.ts`, Python `tui/status.py` |
| `spinner` | Go, TS, Python | TS `tui/spinner.ts` |
| `anim` | Go, TS, Python | TS `tui/anim.ts`, Python `tui/anim.py` |
| `help` | Go, TS, Python | Go `console/cli`, TS `cli.ts`, Python `cli.py` |
| `verbosity` | Go, TS, Python | not yet wired — values still hardcoded per port |
| `streams` | Go, TS, Python | not yet wired — values still hardcoded per port |

`verbosity` and `streams` are loaded but not yet read by the runtime. Each
port currently hardcodes the same values (`-V` count → info/debug/trace,
`--quiet` → warn; `--stream` writing `[name]&#32;` prefixed lines to stderr).
Loading them first makes the contract honest and gives the ports a single
value to migrate onto; wiring each port to read from the loader is follow-up
work.

## How each port reads this file

All three ports read **this file**, not a copy of it. There is one source of
truth on disk.

| Port | Mechanism |
|------|-----------|
| Go | `//go:embed parity.json` in `contracts/parity/parity.go` |
| TypeScript | `import` of the canonical path; tsup/esbuild inlines the JSON into the emitted bundle, so the published package stays self-contained |
| Python | `hop_top_kit.parity` resolves the path once and every consumer imports from there |

TypeScript previously vendored `sdk/ts/src/tui/parity.json` and kept it in
step with a sync test. That copy is gone: a second file kept correct by a
test is a drift class that only exists because the copy exists. Removing it
removes the class.

Python previously derived the path three separate times — `cli.py`
(`parents[3]`), `tui/status.py` and `tui/anim.py` (both `parents[4]`). Three
walks to one file break differently when a module moves, so they are now a
single `hop_top_kit/parity.py`.

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

## MCP wire contract (`sdk/tests/cross-lang/fixtures/mcp-wire.json`)

The dual-spec MCP surface is a parity contract, but not a `parity.json`
block: it pins **wire bytes**, not shared constants, so it carries its own
fixture file and its own per-language runners. It is recorded here because
this page is where a reader looks for "what must not drift between ports".

Five implementations serve it:

| Port | Implementation | Runner |
|------|----------------|--------|
| Go | `go/transport/cmdsurface/surface_mcp*.go` | `TestGenerateMCPWireFixtures` (drift gate) |
| TypeScript | `sdk/ts/src/mcp/` | `src/mcp/conformance.test.ts` |
| Python | `sdk/py/hop_top_kit/mcp/` | `tests/test_mcp_conformance.py` |
| Rust | `sdk/experimental/rs/src/mcp/` | `tests/mcp_wire_conformance.rs` (feature `mcp`) |
| PHP | `sdk/experimental/php/src/Mcp/` | `tests/Mcp/WireConformanceTest.php` |

`make test-parity-mcp` runs all five. PHP is optional — it runs only when
`php` and `composer` are on PATH, matching how the other experimental-SDK
gates behave.

Go's entry is a **drift gate, not a replay**: the fixture is generated from
the Go surface, so Go's job is to prove the checked-in file still matches
what it emits. The other four replay it.

### Byte-identical means byte-identical

Each case posts `request` verbatim with `headers` applied and asserts the
response equals `response` **as bytes**, with no JSON decode/re-encode
before comparing. Go emits objects with lexicographically sorted keys and a
trailing newline; a runtime whose serializer differs must reorder to match,
not normalize the comparison away. A port that compares parsed structures
passes while emitting bytes no Go client would accept.

### Both sections must run

The file has two sections, and running only the first is the easy mistake:

- **`cases`** (18) each get a **fresh mount**, so no case can observe state
  left by another.
- **`sequences`** (1, with 5 steps) are the deliberate exception: ordered
  steps replayed against **one long-lived mount** — which is how adopters
  actually deploy.

Two real defect classes pass every single case and are caught only by the
sequence:

1. **A port that caches its leaf set.** `tools/list` must re-read the
   bridge's leaves per request; Go's `Leaf` wraps a live `*cobra.Command`
   and re-walks its flags every time.
2. **A port that attaches lazy flags non-idempotently.** Cobra attaches
   `--help` on a command's *first execution*, so two byte-identical
   `tools/list` requests on one mount legitimately differ across an
   intervening `tools/call` — and must differ *consistently*.

The shipped sequence, `legacy/lazy-help-flag-on-long-lived-mount`, replays
list → invoke → list → invoke → list. A cache passes every case and fails
step 3; non-idempotent attachment passes every case and step 3, then fails
step 5. A runner that only replays `cases` is not testing the contract.

### Regenerating

The fixture is generated, not hand-edited:

```bash
go test ./go/transport/cmdsurface/ -run TestGenerateMCPWireFixtures \
    -update-mcp-fixtures
```

Regenerating is a deliberate act: it re-baselines every other port against
new Go behavior, so a diff in this file should always be explainable as an
intended Go-side change.

Adopter-facing documentation of the surface itself lives in
[`docs/adopters/guides/serve-mcp-from-any-sdk.md`](../../docs/adopters/guides/serve-mcp-from-any-sdk.md)
(polyglot) and
[`docs/adopters/guides/expose-cli-over-mcp.md`](../../docs/adopters/guides/expose-cli-over-mcp.md)
(the Go reference).

## `extends`

`extends` lists sibling contract files that the parity test suite also
covers. It is a **test-suite registry, not a loader directive** — nothing
merges the referenced file into `parity.Values` at runtime. Each listed
contract carries its own loader and its own sync test; for
`scope-defaults.json` those are the per-language scope packages and
`TestScopeDefaultsContractSync` / `TestScopeDefaultsRegistered`.
