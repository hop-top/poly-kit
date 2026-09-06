# Cli

## What it answers

How does a Symfony Console command become a kit command: a data-only `handle()` body whose return value
is rendered through the standard output flag family? Use `KitCommand` when you own the base class.
When you already extend another base class, call `KitOutput::for()` from `HopTop\Kit\Output` instead.
The `Cli` class is an empty placeholder; the global-flag layer (`--offline`, verbosity) is not wired in
this port yet.

## Use it when

- a new command returns rows or a map: extend `KitCommand`, implement `handle()`, call `$this->render()`
- the command needs a fixed column order and `--cols` validation: pass `columns:` as a `ColumnSpec` list
- the command is `serve`: use `HopTop\Kit\Serve\ServeCommand`, which already extends `KitCommand`

## Quick start

```php
// after require vendor/autoload.php
use HopTop\Kit\Cli\KitCommand;
use HopTop\Kit\Output\Flags;
use HopTop\Kit\Output\Formatter\ColumnSpec;
use Symfony\Component\Console\Application;
use Symfony\Component\Console\Attribute\AsCommand;
use Symfony\Component\Console\Input\ArrayInput;

#[AsCommand(name: 'items')]
final class ItemsCommand extends KitCommand
{
    protected function handle(): int
    {
        $this->render(
            [['name' => 'alpha', 'count' => 1]],
            columns: [ColumnSpec::of('name', 'name'), ColumnSpec::of('count', 'count')],
        );
        return self::SUCCESS;
    }
}

$app = new Application('tool', '0.1.0');
$app->setAutoExit(false);
Flags::register($app);
$app->add(new ItemsCommand());

$app->run(new ArrayInput(['command' => 'items', '--format' => 'json']));
// [
//   {
//     "name": "alpha",
//     "count": 1
//   }
// ]
```

## Contract

- `Flags::register($app)` must run before the command executes; without it `render()` sees no
  `--format` option and falls back to `table` with no `--cols` support
- `handle()` replaces `execute()`; `$this->input` and `$this->output` are set before it runs
- `render()` honors `--format`, `--format-opt`, `--format-help`, `--cols`, `--template` and `-o`
  exactly as `HopTop\Kit\Output\Dispatcher` documents them
- Parity: none recorded; Go `go/console/cli` is the reference

## Neighbours

- `HopTop\Kit\Output`: the flag suite, dispatcher, registry and `CliError` exit-code taxonomy
- `HopTop\Kit\Serve`: the kit-owned `serve` command built on `KitCommand`
- `HopTop\Kit\Net`: the `--offline` marker a future global-flag layer will set

## See also

- [Output formatting](../../../../../docs/adopters/reference/php-sdk.md#output-formatting)
- [CLI parity guide](../../../../../docs/adopters/guides/cli-parity-guide.md)
- [Go reference: `go/console/cli`](../../../../../go/console/cli/README.md)
