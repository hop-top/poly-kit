# Formatter

## What it answers

What must a formatter implement, and how are columns, options and projections described so the parent
dispatcher can drive it? `Formatter` is the interface; `ColumnSpec`, `OptionSpec`, `OptionType`,
`Options` and `Projection` are the vocabulary. Flag parsing, writer resolution and the registry live
one level up in `HopTop\Kit\Output`.

## Use it when

- you write a formatter: implement `key()`, `extensions()`, `options()`, `render()`
- you declare a `--format-opt` key: return an `OptionSpec` with a type, usage and default
- you need the column list a formatter will receive: `Projection::resolveEffectiveCols()`
- you reshape rows to that list: `Projection::normalize()` then `Projection::projectRow()`

## Quick start

```php
// after require vendor/autoload.php
use HopTop\Kit\Output\Formatter\ColumnSpec;
use HopTop\Kit\Output\Formatter\Options;
use HopTop\Kit\Output\Formatter\OptionSpec;
use HopTop\Kit\Output\Formatter\OptionType;
use HopTop\Kit\Output\Formatter\Projection;

$specs = [new OptionSpec(name: 'indent', type: OptionType::Int, usage: 'spaces', default: 2)];
var_export(Options::parse(['indent=4'], $specs)); // ['indent' => 4]
echo "\n";

$columns = [ColumnSpec::of('name', 'name'), ColumnSpec::of('count', 'count')];
var_export(Projection::resolveEffectiveCols([], $columns));        // ['name', 'count']
echo "\n";
var_export(Projection::resolveEffectiveCols(['count'], $columns)); // ['count']
echo "\n";
```

## Contract

- `render(mixed $writer, mixed $data, array $opts, array $cols)`: `$writer` is a stream resource,
  `$opts` is already coerced and defaulted, `$cols` is final (empty means payload key order)
- `ColumnSpec` throws `InvalidArgumentException` when `header !== key`
- `Options::parse()` coerces `string`, `int`, `bool` (`true|false|1|0|yes|no|t|f|y|n`) and `enum`;
  a bare key is valid only for `bool`; unknown keys throw and list the valid set
- Parity: [`sdk/tests/cross-lang/fixtures/ordering.json`](../../../../../tests/cross-lang/fixtures/ordering.json),
  runner `sdk/tests/cross-lang/runners/php/order.php`

## Neighbours

- `HopTop\Kit\Output`: `Dispatcher` and `Registry`, the only callers of `render()`
- `HopTop\Kit\Output\Formatter\Builtin`: reference implementations of this interface

## See also

- [Column ordering](../../../../../../docs/adopters/reference/php-sdk.md#column-ordering)
- [Go reference: `go/console/output`](../../../../../../go/console/output/README.md)
