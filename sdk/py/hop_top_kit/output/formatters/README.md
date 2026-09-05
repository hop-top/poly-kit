# output/formatters

## What it answers

Which bytes each built-in format emits and which `--format-opt` keys it accepts. Every module here is one `Formatter` registered against `default_registry` when `hop_top_kit.output` is imported. Wrong place for flag wiring or format resolution: that is [`../cli.py`](../cli.py) and [`../dispatch.py`](../dispatch.py).

## Use it when

- you need a formatter without the flag suite: `default_registry.lookup(key)` then `render(out, data, opts, cols)`
- you are adding a built-in: copy the closest module, register it in [`../registry.py`](../registry.py)
- you are checking what an option does: read the module docstring

| Key | Module | Options |
|-----|--------|---------|
| `json` | `json_formatter.py` | `indent` |
| `yaml` | `yaml_formatter.py` | PyYAML `safe_dump` |
| `table` | `table_formatter.py` | none |
| `csv` | `csv_formatter.py` | `delimiter`, `no-header`, `quote-all`, `crlf` |
| `text` | `text_formatter.py` | `style` (kv, lines, paragraph), `separator` (kv only) |

## Quick start

```python
import sys

from hop_top_kit.output import default_registry

print(default_registry.keys())
csv = default_registry.lookup("csv")
csv.render(sys.stdout, [{"id": "1", "name": "Alice"}], {"delimiter": ";"}, [])
```

## Contract

- `render` trusts `opts`: callers run `parse_options` against `options()` first.
- Key order for every formatter: `columns` sets it, `cols` reorders and selects, payload insertion order is the fallback.
- `table` emits nothing for zero rows, not even a header.
- `csv` encoding is hand-rolled: `csv.writer` does not quote a field beginning with whitespace, the other runtimes do.
- `text` mirrors `go/console/output/text.go` byte for byte.
- Parity: [`sdk/tests/cross-lang/fixtures/ordering.json`](../../../../tests/cross-lang/fixtures/ordering.json).

## Neighbours

- [`../registry.py`](../registry.py): `Registry`, `default_registry`, `override`
- [`../format_help.py`](../format_help.py): `--format-help` catalog
- TypeScript port: [`sdk/ts/src/output/formatters`](../../../../ts/src/output/formatters/README.md)

## See also

- [`../README.md`](../README.md)
- [`sdk/py/README.md`](../../../README.md), section "Built-in formatters"
