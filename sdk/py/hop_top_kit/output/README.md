# output

## What it answers

How a command turns a structured value into bytes on a stream in the format the caller picked: table, json, yaml, csv, text, or a `--template`. It also owns the structured error envelope and the exit-code table. Wrong module for terminal widgets (`hop_top_kit.tui`), progress events (`hop_top_kit.progress`), or any flag outside the output suite (`hop_top_kit.cli`).

## Use it when

- one-off render, no flags involved: `render(w, format, data)`
- Typer app that must honour `--format`, `--format-opt`, `--cols`, `--template`, `--output|-o`, `--format-help`: `register_output_flags(app)` from `hop_top_kit.output.cli`, then `dispatch(ctx, data, columns=...)` from `hop_top_kit.output.dispatch`
- fail with a machine-readable error under `--format json|yaml`: `not_found_error`, `usage_error`, `wrap_error` and friends, rendered by `render_error`
- add a format: implement `Formatter`, `default_registry.register(f)`; built-ins live in [`formatters/`](formatters/README.md)

## Quick start

```python
import sys

from hop_top_kit.output import render

render(sys.stdout, "table", [{"id": "1", "name": "Alice"}])
render(sys.stdout, "json", {"ok": True})
```

## Contract

- `render(w, format, v)` renders every column in payload insertion order. `columns` (`list[ColumnSpec]`) and `--cols` are reachable only through `dispatch`.
- `--cols` selects and reorders; the user's order wins over the `ColumnSpec` list.
- `--template` (Jinja2) and `--cols` are mutually exclusive.
- Format resolution: explicit `--format`, else the `--output` extension, else `table`; an explicit `--format` that disagrees with the `--output` extension raises `typer.BadParameter`.
- `register_output_flags` intercepts `app.command` and `app.add_typer`, so call it before registering commands.
- Exit codes are exported constants (`EXIT_GENERIC`, `EXIT_TRANSIENT`, `EXIT_RATE_LIMITED`, `EXIT_PROVENANCE_MISSING`); import them, never hardcode.
- Parity: [`sdk/tests/cross-lang/fixtures/ordering.json`](../../../tests/cross-lang/fixtures/ordering.json), replayed by `sdk/tests/cross-lang/run-order.sh`.

## Neighbours

- `hop_top_kit.cli`: `create_app`, help layout, `Theme`
- `hop_top_kit.tui`: styled TTY widgets (status, badge, spinner)
- `hop_top_kit.progress`: Factor 9 progress events
- `hop_top_kit.provenance`: `_meta` provenance wrapper around payloads
- Go reference: [`go/console/output`](../../../../go/console/output/README.md); TypeScript port: [`sdk/ts/src/output`](../../../ts/src/output/README.md)

## See also

- [`sdk/py/README.md`](../../README.md), section "Output formatting": quickstart with Typer, column ordering, `--template`, custom formatters
- [`docs/adopters/reference/py-api-reference.md`](../../../../docs/adopters/reference/py-api-reference.md)
- [`sdk/tests/cross-lang/README.md`](../../../tests/cross-lang/README.md): column-ordering harness
