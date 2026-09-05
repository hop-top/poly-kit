# output/formatters

## What it answers

Which bytes each built-in format emits and which `--format-opt` keys it accepts. Every file here is one `Formatter` registered against `defaultRegistry` when [`../builtins.ts`](../builtins.ts) loads. Wrong place for flag wiring or format resolution: that is [`../dispatch.ts`](../dispatch.ts) and [`../flags.ts`](../flags.ts).

## Use it when

- you need a formatter without the flag suite: `defaultRegistry.lookup(key)` then `render(out, data, opts, cols)`
- you are adding a built-in: copy the closest file, export a `Formatter`, register it in `../builtins.ts`
- you are checking what an option does: read the file's header comment

| Key | Extensions | Options |
|-----|------------|---------|
| `json` | `.json` | `indent` (int, default 2) |
| `yaml` | `.yaml`, `.yml` | `flow-level` (int, default -1) |
| `table` | none | none |
| `csv` | `.csv` | `delimiter`, `no-header`, `quote-all`, `crlf` |
| `text` | `.txt` | `style` (kv, lines, paragraph), `separator` (kv only) |

## Quick start

```ts
import { defaultRegistry } from '@hop-top/kit/output';

console.log(defaultRegistry.keys());
const csv = defaultRegistry.lookup('csv')!;
csv.render(process.stdout, [{ id: '1', name: 'Alice' }], { delimiter: ';' }, []);
```

## Contract

- `render` trusts `opts`: callers run `parseOptions` against `options()` first.
- `json` and `yaml` preserve the payload's outer shape (object stays object, array stays array) and pass non-object payloads through untouched; [`project.ts`](project.ts) is the shared shim.
- `table`, `csv` and `text` take column order from the `ColumnSpec` list, else the first row's key order; `cols` narrows and reorders.
- `csv` encoding is hand-rolled: a quoted field's bytes are preserved verbatim in both line-ending modes.
- `text` mirrors `go/console/output/text.go` byte for byte.
- Parity: [`sdk/tests/cross-lang/fixtures/ordering.json`](../../../../tests/cross-lang/fixtures/ordering.json).

## Neighbours

- [`../registry.ts`](../registry.ts): `Registry`, `defaultRegistry`, duplicate-key fail-loud
- [`../template.ts`](../template.ts): `--template` (eta), not a formatter
- [`../format_help.ts`](../format_help.ts): `--format-help` catalog
- Python port: [`hop_top_kit/output/formatters`](../../../../py/hop_top_kit/output/formatters/README.md)

## See also

- [`../README.md`](../README.md)
- [`sdk/ts/README.md`](../../../README.md), section "Built-in formatters"
