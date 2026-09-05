# php

Experimental PHP client SDK for hop-top kit.

## Install

```sh
composer require hop-top/kit
```

Requires PHP 8.4. Optional PSR-15 MCP hosting needs a PSR-7
implementation of your choice (`nyholm/psr7` in the dev requirements).

## Quick start

```php
use HopTop\Kit\Cli\KitCommand;
use HopTop\Kit\Output\Flags;
use HopTop\Kit\Output\Formatter\ColumnSpec;
use Symfony\Component\Console\Application;
use Symfony\Component\Console\Attribute\AsCommand;

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
Flags::register($app);      // --format, --format-opt, --cols, --template, -o
$app->add(new ItemsCommand());
```

Already extending another base class? Call `HopTop\Kit\Output\KitOutput::for()`
instead of `KitCommand`.

## Modules

Every path is relative to `src/`; namespaces sit under `HopTop\Kit`.

| Path | What it is | Start here when |
|------|------------|-----------------|
| [`Cli/`](src/Cli/README.md) | `KitCommand` base for Symfony Console commands | you write a kit-powered command |
| [`Serve/`](src/Serve/README.md) | serve hierarchy and service lifecycle | your CLI hosts long-running services |
| [`Output/`](src/Output/README.md) | `--format` flag family, dispatcher, registry, `CliError` | a command renders one payload as table, json or yaml |
| [`Output/Formatter/`](src/Output/Formatter/README.md) | `Formatter` interface, `ColumnSpec`, options, projection | you implement or configure a formatter |
| [`Output/Formatter/Builtin/`](src/Output/Formatter/Builtin/README.md) | the shipped formatters and their `--format-opt` keys | you need a formatter option or a custom formatter |
| [`Mcp/`](src/Mcp/README.md) | dual-spec MCP surface over PSR-15 | MCP clients must call your commands as tools |
| [`Net/`](src/Net/README.md) | `--offline` marker and Guzzle enforcement | a request must honour `--offline` |
| [`Api/`](src/Api/README.md) | JSON API client with the offline guard pre-installed | you call an HTTP API from a kit CLI |
| [`Id/`](src/Id/README.md) | TypeID primitive | you mint or parse prefixed identifiers |
| [`Telemetry/`](src/Telemetry/README.md) | consent-gated usage events and redaction | you record usage under user consent |
| [`Telemetry/Sink/`](src/Telemetry/Sink/README.md) | JSONL, HTTPS and null sinks | you choose where envelopes go |
| [`Uri/`](src/Uri/README.md) | URI facade delegating to `hop-top/cite` | you parse or format kit URIs |
| [`Tui/`](src/Tui/README.md) | placeholder class, no members yet | never; reserved for status output |

## Contract

- `output` ships `table`, `json` and `yaml`; column order comes from an explicit `ColumnSpec` list, `--cols` reorders as well as selects, and `header` must equal `key`.
- MCP exposure is default-closed: the default policy blocks every destructive leaf on every remote surface.
- `--offline` is the highest-precedence network override, exempts loopback, and throws `OfflineException` on a guarded Guzzle stack or PSR-18 client. It does not cover raw sockets, PDO or `file_get_contents()`, and `HttpsSink` is deliberately unguarded.
- Telemetry is default-denied: nothing emits without both a granted consent decision and a non-`off` mode.

## See also

- [PHP SDK reference](https://github.com/hop-top/poly-kit/blob/main/docs/adopters/reference/php-sdk.md):
  the URI facade, output formatting rules and worked examples, the MCP
  mount over PSR-15, offline enforcement in full, telemetry sinks and
  redaction
- [Serve lifecycle contract](https://github.com/hop-top/poly-kit/blob/main/docs/contracts/serve-lifecycle.md), [CLI parity guide](https://github.com/hop-top/poly-kit/blob/main/docs/adopters/guides/cli-parity-guide.md), [Serve MCP from any SDK](https://github.com/hop-top/poly-kit/blob/main/docs/adopters/guides/serve-mcp-from-any-sdk.md)
