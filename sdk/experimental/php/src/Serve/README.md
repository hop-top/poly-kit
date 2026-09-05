# Serve

## What it answers

How does a CLI host long-running services under one `serve` verb with a shared lifecycle: start order,
readiness, failure policy, shutdown, exit codes? `ServeCommand` mounts the verb, `Supervisor` runs the
lifecycle, `Resolver` picks the services. One-shot commands belong to `HopTop\Kit\Cli`; this namespace
is only for processes that stay up until cancelled.

## Use it when

- your tool ships listeners or workers: implement `Service`, register it in a `ServiceRegistry`, and
  mount `ServeCommand::fromConfig($registry, $config['services'])`
- a service must start after another: also implement `DeclaresDependencies`
- a service must refuse bad config before start: implement `ValidatesConfig`
- you embed the lifecycle in your own runner without Symfony: call `Supervisor::run()` directly

## Quick start

```php
// after require vendor/autoload.php
use HopTop\Kit\Serve\Cancellation;
use HopTop\Kit\Serve\Service;
use HopTop\Kit\Serve\ServiceConfig;
use HopTop\Kit\Serve\ServiceRegistry;
use HopTop\Kit\Serve\Supervisor;

final class ApiService implements Service
{
    private int $ticks = 0;

    public function name(): string { return 'api'; }
    public function start(): void { /* bind the listener */ }
    public function ready(): bool { return true; }
    public function tick(): bool { return ++$this->ticks < 3; } // false: finished on its own
    public function stop(): void { /* close the listener */ }
}

$registry = new ServiceRegistry();
$registry->register(new ApiService());

$supervisor = new Supervisor($registry); // lifecycle events go to stderr by default
$result = $supervisor->run(
    new Cancellation(),                   // cancel() from a signal handler to shut down
    ['api'],                              // normally Resolver::resolve(...)->selected
    ['api' => new ServiceConfig(enabled: true)],
);

echo $result->outcome->value, ' exit=', $result->exitCode(), "\n"; // clean-stop exit=0
```

## Contract

- Names match `^[a-z][a-z0-9-]*$`; `all`, `none` and `list` are reserved (`Names`)
- `serve` alone supervises every configured and enabled service; `serve <name>` runs that one even
  when disabled; two names is a usage error (exit 2); `--list` inspects
- Config keys: `services.<name>.enabled` (false), `ready_timeout` (30s), `stop_timeout` (30s),
  `services.failure_policy` (`fail-fast` or `isolate`), `services.shutdown_timeout` (60s)
- Exit codes come from `Outcome`: clean 0; invalid selection, config, no services 2;
  unknown service 3; policy denied 5; start failure, crash, shutdown timeout 1
- Topics: `kit.serve.service.{started,ready_reported,failed,stopped}` and
  `kit.serve.supervisor.{ready_reported,stopped}`, logged through `ServeLogger`
- Parity: [`contracts/parity/serve.json`](../../../../../contracts/parity/serve.json), `ports.php`
  row, pinned by `tests/Serve/`

## Neighbours

- `HopTop\Kit\Cli`: `KitCommand`, the base `ServeCommand` extends
- `HopTop\Kit\Output`: `CliError`, the source of every exit code above
- `HopTop\Kit\Mcp`: a PSR-15 handler you would host inside one of these services

## See also

- [Serve lifecycle contract](../../../../../docs/contracts/serve-lifecycle.md)
- [Migrate to served commands](../../../../../docs/adopters/guides/migrate-to-served-commands.md)
- [Go reference: `go/console/serve`](../../../../../go/console/serve/)
