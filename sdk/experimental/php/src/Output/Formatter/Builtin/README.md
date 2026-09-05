# Builtin

## What it answers

Which formatters ship by default, what `--format-opt` keys each accepts, and how a custom formatter
registers beside them? Five formatters are registered by `Builtins::register()` on the default
registry. Flag parsing and dispatch live in `HopTop\Kit\Output`.

## Use it when

- you want the standard set on a fresh registry: `Builtins::register($registry)`
- you replace one built-in: `$registry->override(new MyJson())` under the same key
- you add a format: `$registry->register($f)`; a key clash throws, so pick a new key

## Quick start

```php
// after require vendor/autoload.php
use HopTop\Kit\Output\Builtins;
use HopTop\Kit\Output\Formatter\Formatter;
use HopTop\Kit\Output\Registry;

$registry = new Registry();
Builtins::register($registry);
echo implode(' ', $registry->keys()), "\n"; // csv json table text yaml

$registry->register(new class implements Formatter {
    public function key(): string { return 'count'; }
    public function extensions(): array { return []; }
    public function options(): array { return []; }
    public function render(mixed $writer, mixed $data, array $opts, array $cols): void
    {
        fwrite($writer, count((array) $data) . "\n");
    }
});

$rows = [['name' => 'alpha'], ['name' => 'beta']];
$registry->lookup('json')->render(STDOUT, $rows, ['indent' => 0], ['name']);
// [{"name":"alpha"},{"name":"beta"}]
$registry->lookup('count')->render(STDOUT, $rows, [], ['name']);
// 2
```

## Contract

| Key | Extensions | `--format-opt` keys (default) |
|-----|------------|-------------------------------|
| `table` | none | `header` bool (true) |
| `json` | `.json` | `indent` int (2, 0 = compact) |
| `yaml` | `.yaml`, `.yml` | `inline` int (4), block to inline switch depth |
| `csv` | `.csv` | `delimiter` string (`,`), `no-header` bool, `quote-all` bool, `crlf` bool |
| `text` | `.txt` | `style` enum `kv`, `lines`, `paragraph` (`kv`); `separator` string (`=`) |

- Extensions drive format inference from `-o file.ext`; `table` has none and is never inferred
- Every built-in renders only the final `$cols` list it receives, in that order
- Zero rows: `table`, `csv` and `text` emit nothing, not even a header; `json` emits `[]`, `yaml` an
  empty mapping
- Parity: [`sdk/tests/cross-lang/fixtures/ordering.json`](../../../../../../tests/cross-lang/fixtures/ordering.json),
  runner `sdk/tests/cross-lang/runners/php/order.php`

## Neighbours

- `HopTop\Kit\Output\Formatter`: the interface and option vocabulary these implement
- `HopTop\Kit\Output`: `Registry::default()`, where these are registered on first use

## See also

- [Output formatting](../../../../README.md#output-formatting)
- [Go reference: `go/console/output`](../../../../../../../go/console/output/README.md)
