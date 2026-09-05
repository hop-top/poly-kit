# output

## What it answers

How a command turns a structured value into bytes on a stream in the format the caller picked: table, json, yaml, csv, text, or a `--template`. It also owns the structured error envelope and the exit-code table. Wrong module for terminal widgets (`@hop-top/kit/tui`), progress events (`@hop-top/kit/progress`), or any flag outside the output suite (`@hop-top/kit/cli`).

## Use it when

- one-off render, no flags involved: `render(w, format, data)`
- Commander program that must honour `--format`, `--format-opt`, `--cols`, `--template`, `--output|-o`, `--format-help`: `registerOutputFlags(program)` then `dispatch(program, data)`
- fail with a machine-readable error under `--format json|yaml`: `notFoundError`, `usageError`, `wrapError` and friends, rendered by `renderError`
- add a format: implement `Formatter`, `defaultRegistry.register(f)`; built-ins live in [`formatters/`](formatters/README.md)

## Quick start

```ts
import { render, TABLE_FORMAT, JSON_FORMAT } from '@hop-top/kit/output';

render(process.stdout, TABLE_FORMAT, [{ id: '1', name: 'Alice' }]);
render(process.stdout, JSON_FORMAT, { ok: true });
```

## Contract

- `render(w, format, v)` renders every column in payload key order. `ColumnSpec[]` and `--cols` are reachable only through `dispatch`.
- `--cols` selects and reorders; the user's order wins over the `ColumnSpec` list.
- `--template` and `--cols` are mutually exclusive; `dispatch` throws.
- Format resolution: explicit `--format`, else the `--output` extension, else `table`.
- Error envelope keys are wire snake_case; empty optionals stay off the wire (Go `omitempty`).
- Exit codes are exported constants (`EXIT_GENERIC` 1, `EXIT_TRANSIENT` 6, `EXIT_RATE_LIMITED` 64, `EXIT_PROVENANCE_MISSING` 65); import them, never hardcode.
- Parity: [`sdk/tests/cross-lang/fixtures/ordering.json`](../../../tests/cross-lang/fixtures/ordering.json), replayed by `sdk/tests/cross-lang/run-order.sh`.

## Neighbours

- `@hop-top/kit/cli`: program factory, help layout, `Theme`
- `@hop-top/kit/tui`: styled TTY widgets (status, badge, spinner)
- `@hop-top/kit/progress`: Factor 9 progress events
- `@hop-top/kit/provenance`: `_meta` provenance wrapper around payloads
- `@hop-top/kit/stream`: stdout/stderr discipline
- Go reference: [`go/console/output`](../../../../go/console/output/README.md); Python port: [`hop_top_kit/output`](../../../py/hop_top_kit/output/README.md)

## See also

- [`sdk/ts/README.md`](../../README.md), section "Output formatting": flag surface, column-ordering rules, conformance status
- [`docs/adopters/reference/ts-api-reference.md`](../../../../docs/adopters/reference/ts-api-reference.md)
- [`sdk/tests/cross-lang/README.md`](../../../tests/cross-lang/README.md): column-ordering harness
