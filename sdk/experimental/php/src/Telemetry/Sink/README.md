# Sink

## What it answers

Where do telemetry envelopes go once consent allows them: JSONL file, HTTPS endpoint, or nowhere?
`SinkInterface` is the publish-only contract; `JsonlSink`, `HttpsSink` and `NullSink` implement it.
Deciding whether an event may be recorded at all is the parent `HopTop\Kit\Telemetry`, not a sink.

## Use it when

- the default per-PID JSONL inbox is fine: do nothing, `Telemetry` builds `JsonlSink` for you
- you control the file path (tests, harnesses): `new JsonlSink(path: $p, registerShutdown: false)`
- events must reach a remote ingestor: `new HttpsSink($endpoint, $guzzleClient)` and flush it
  from a worker, never from a php-fpm request
- CI or staging must record nothing: `Telemetry::setSink(new NullSink())`

## Quick start

```php
// after require vendor/autoload.php
use HopTop\Kit\Telemetry\Sink\JsonlSink;
use HopTop\Kit\Telemetry\Sink\NullSink;
use HopTop\Kit\Telemetry\Telemetry;

$path = sys_get_temp_dir() . '/kit-readme-' . getmypid() . '.jsonl';
$sink = new JsonlSink(path: $path, cap: 16, registerShutdown: false);

$sink->enqueue(['schema_version' => '1', 'event' => 'demo']);
$sink->flush();
var_export($sink->stats()); // ['emitted' => 1, 'dropped' => 0, 'queued' => 0, 'path' => '...']
echo "\n";

Telemetry::setSink(new NullSink()); // CI or staging: every envelope is dropped
```

## Contract

- `enqueue()` never blocks or throws: a full queue (`cap`, default 1024) increments `dropped`
- `flush()` swallows I/O and transport failures and accounts for them in `stats()`
- `stats()` always carries `emitted`, `dropped`, `queued`; `JsonlSink` adds `path`
- `JsonlSink` default path: `$XDG_STATE_HOME/kit/telemetry/inbox/php-<pid>.jsonl`, `LOCK_EX`
  appends, rotation to `<path>.1` at 10 MiB, shutdown flush registered unless disabled
- `HttpsSink` posts NDJSON batches of 50, sets only `connect_timeout`, `timeout` and
  `http_errors`, registers no shutdown flush, and must not be given an offline-guarded client
- Parity: [`sdk/tests/cross-lang/fixtures/input.json`](../../../../../tests/cross-lang/fixtures/input.json),
  written through `JsonlSink` by `sdk/tests/cross-lang/runners/php/record.php`

## Neighbours

- `HopTop\Kit\Telemetry`: consent, mode, redaction and the envelope shape
- `HopTop\Kit\Net`: the offline guard that `HttpsSink` deliberately bypasses

## See also

- [Sink selection](../../../README.md#sink-selection)
- [FPM caveat](../../../README.md#fpm-caveat-https-sink)
- [Go reference: `go/runtime/telemetry`](../../../../../../go/runtime/telemetry/README.md)
