# exit-taxonomy-v1

## What it answers

Which class symbols kit's exit-code taxonomy defines, which numeric exit code each carries, and which default transience each resolves to — in every language kit ships. A divergence is a behavior break, not a missing constant: adopters and agents branch on `exit_code` and `transience`, so a port that disagrees hands out wrong retry guidance. Wrong place for serve outcomes; those route onto this taxonomy and live in [`../parity/serve.json`](../parity/serve.json).

## Use it when

- you add, renumber or reclassify a class in `go/console/output/envelope`: regenerate this file, then carry the change into all five ports in the same commit
- you allocate a new slot in the >6 extension band from any package: regenerate, so the allocation is visible in one place before a second feature claims the number
- you port the error envelope to a new SDK: load these rows in a contract test before anything else

## Quick start

```sh
make test-parity-taxonomy
```

## Contract

`taxonomy.json` is **generated** from `go/console/output/envelope` — the single source of truth — and is never hand-edited. Regenerate with:

```sh
go test ./go/console/output/envelope/ -run TestGenerateExitTaxonomyContract \
    -update-taxonomy-contract
```

| Key | Holds |
|-----|-------|
| `classes` | every standard class, ascending by exit: `class`, `exit`, `transience` |
| `extension_band` | every allocated slot above 6, with the `owner` package that declares it |
| `transiences` | the three-value transience vocabulary |

Ports MUST export every class in `classes`, resolve each to its `exit`, and answer its `transience`. They MUST export the `extension_band` slots `envelope` owns and MUST NOT claim the slots owned by the conformance trees (66 `LEAK_DETECTED`, 67 `CONFIG`, 68 `GRADE_FAIL`, 69 `GRADE_UNGRADABLE`) — those rows are recorded so a future allocation cannot double-book a number, not so ports mint them.

Loaders, one per port, all run by `make test-parity-taxonomy`:

| Port | Loader |
|------|--------|
| Go | `go/console/output/envelope/taxonomy_contract_gen_test.go` (drift gate, not a replay) |
| TypeScript | `sdk/ts/test/output/taxonomy-contract.test.ts` |
| Python | `sdk/py/tests/test_taxonomy_contract.py` |
| Rust | `sdk/experimental/rs/tests/taxonomy_contract.rs` (feature `output`) |
| PHP | `sdk/experimental/php/tests/Output/TaxonomyContractTest.php`, run only when `php` and `composer` are on PATH |

The two halves compose: a class added to Go turns the Go gate red, and regenerating turns all four SDK loaders red until they carry it too.

## Neighbours

- `go/console/output/envelope/`: the Go implementation and the reference behavior this file is generated from
- `../parity/serve.json`: serve outcomes, which route onto this taxonomy rather than restating it
- `sdk/*/output`: the four ports whose tables this file pins

## See also

- [Parity contracts reference](../../docs/adopters/reference/parity-contract.md): the full record, including why this file is generated rather than authored
- [`go/console/output/envelope/exitcodes.go`](../../go/console/output/envelope/exitcodes.go): `ExitClasses`, `ExitCodeForClass`, `ExtensionBand`
