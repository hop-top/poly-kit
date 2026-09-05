# Output

## What it answers

How does a command render one payload as table, json or yaml under the standard `--format` flag family,
with the same column order in every port? `Flags` registers the flags, `Dispatcher` resolves them,
`Registry` holds formatters, `CliError` carries the exit-code taxonomy. Terminal styling belongs to
`HopTop\Kit\Tui`; the Symfony Console base class is `HopTop\Kit\Cli`. The `Output` class is an empty
placeholder.

## Use it when

- you bootstrap an application: `Flags::register($app)` once, before any command runs
- you cannot extend `KitCommand`: `KitOutput::for($input, $output)->render($data)`
- you ship a custom formatter: `Registry::default()->register($f)` or `Flags::setRegistry($app, $r)`
- a command fails: throw or render a `CliError` (`usage`, `notFound`, `conflict`, `transient`, ...)

## Quick start

```php
// after require vendor/autoload.php
use HopTop\Kit\Output\Flags;
use HopTop\Kit\Output\Formatter\ColumnSpec;
use HopTop\Kit\Output\KitOutput;
use Symfony\Component\Console\Application;
use Symfony\Component\Console\Command\Command;
use Symfony\Component\Console\Input\ArrayInput;
use Symfony\Component\Console\Input\InputInterface;
use Symfony\Component\Console\Output\OutputInterface;

$app = new Application('tool', '0.1.0');
$app->setAutoExit(false);
Flags::register($app); // --format, --format-opt, --format-help, --cols, --template, -o

$app->add(new class('items') extends Command {
    protected function execute(InputInterface $input, OutputInterface $output): int
    {
        KitOutput::for($input, $output)
            ->application($this->getApplication())
            ->columns([ColumnSpec::of('name', 'name'), ColumnSpec::of('count', 'count')])
            ->render([
                ['count' => 3, 'name' => 'alpha'],
                ['count' => 8, 'name' => 'beta'],
            ]);
        return self::SUCCESS;
    }
});

$app->run(new ArrayInput(['command' => 'items', '--cols' => ['count,name']]));
// count  name
// 3      alpha
// 8      beta
```

## Contract

- Column order: `--cols` wins and reorders, else the `ColumnSpec` list, else payload key order;
  `header` must equal `key`; zero rows emit nothing; `priority` is stored and ignored
- `--format` defaults to `table`; with `-o file.ext` the extension picks the formatter, and an
  explicit `--format` that disagrees with it is an error
- `--template` and `--cols` are mutually exclusive; `{key}` and `{*}` are the only placeholders
- `Registry::register()` throws on a duplicate key; use `override()` to replace a built-in
- `CliError` codes and exits: `USAGE` 2, `NOT_FOUND` 3, `CONFLICT` 4, `UNAUTHORIZED` 5,
  `TRANSIENT` 6, `RATE_LIMITED` 64, `PROVENANCE_MISSING` 65, `GENERIC` 1
- Parity: [`sdk/tests/cross-lang/fixtures/ordering.json`](../../../../tests/cross-lang/fixtures/ordering.json),
  runner `sdk/tests/cross-lang/runners/php/order.php`

## Neighbours

- `HopTop\Kit\Output\Formatter`: the interface, `ColumnSpec`, `Options`, `Projection`
- `HopTop\Kit\Output\Formatter\Builtin`: the five shipped formatters
- `HopTop\Kit\Cli`: `KitCommand::render()`, the usual entry point
- `HopTop\Kit\Tui`: symbols and styling, not payload rendering

## See also

- [Output formatting](../../README.md#output-formatting)
- [Go reference: `go/console/output`](../../../../../go/console/output/README.md)
