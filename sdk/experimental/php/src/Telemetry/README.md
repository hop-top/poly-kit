# Telemetry

## What it answers

Did the user agree to this egress, and how are usage events redacted, queued and shipped under that
consent? `Telemetry::record()` checks `DO_NOT_TRACK`, the mode and the persisted consent, then stamps
an envelope and enqueues it on the active sink. Network reachability is `HopTop\Kit\Net`; the HTTP
client itself is `HopTop\Kit\Api`. This port never prompts: consent is written by the Go CLI.

## Use it when

- a command wants to count itself: `Telemetry::record('cli.invoked', $attrs)`
- a long-lived worker must not wait for shutdown: `Telemetry::flush()` periodically
- you need HTTPS delivery: build `Sink\HttpsSink` yourself and `Telemetry::setSink()` it
- project data has its own PII shapes: `Telemetry::setRedactor(new Redactor($callback))`
- you want misconfiguration surfaced: `Telemetry::setSinkErrReporter(fn ($m) => error_log($m))`

## Quick start

```php
// after require vendor/autoload.php
use HopTop\Kit\Telemetry\Consent;
use HopTop\Kit\Telemetry\Sink\JsonlSink;
use HopTop\Kit\Telemetry\Telemetry;

// Preconditions the Go CLI normally provides: a granted consent file under
// XDG_CONFIG_HOME and a mode. Shown here so the run is self-contained.
$tmp = sys_get_temp_dir() . '/kit-readme-' . getmypid();
mkdir($tmp . '/config/kit', 0o700, true);
putenv('XDG_CONFIG_HOME=' . $tmp . '/config');
putenv('XDG_STATE_HOME=' . $tmp . '/state');
putenv('KIT_TELEMETRY_MODE=full');
file_put_contents(Consent::path(), "kit:\n  telemetry:\n    consent:\n      state: granted\n");

$sink = new JsonlSink(path: $tmp . '/events.jsonl', registerShutdown: false);
Telemetry::setSink($sink);

Telemetry::record('cli.invoked', ['path' => getenv('HOME') . '/proj', 'mail' => 'a@b.io']);
Telemetry::flush();

echo file_get_contents($tmp . '/events.jsonl');
// {"schema_version":"1","sdk_lang":"php","installation_id":"...","mode":"full",
//  "occurred_at":"...","event":"cli.invoked","attrs":{"path":"$HOME/proj","mail":"<redacted:email>"}}
```

## Contract

- Off by default. Any of `DO_NOT_TRACK`, `KIT_TELEMETRY_MODE=off`, or consent not `granted` in
  `$XDG_CONFIG_HOME/kit/config.yaml` at `kit.telemetry.consent` makes `record()` a no-op
- `anon` drops `attrs`; `full` keeps them after `Redactor` (emails, IPs, `$HOME`, token prefixes)
- Envelope keys: `schema_version`, `sdk_lang`, `installation_id`, `mode`, `occurred_at`, `event`,
  `attrs?`; `installation_id` is the SHA-256 of 32 random bytes kept under `$XDG_STATE_HOME`
- `KIT_TELEMETRY_SINK` accepts `jsonl` (default) and `none`; `https` and typos fall back to JSONL
  with a diagnostic
- Nothing thrown by a sink, resolver or redactor escapes `record()` or `flush()`
- Parity: [`sdk/tests/cross-lang/fixtures/input.json`](../../../../tests/cross-lang/fixtures/input.json)
  with `consent.yaml` and `install_id.bytes`, run by `sdk/tests/cross-lang/runners/php/record.php`

## Neighbours

- `HopTop\Kit\Telemetry\Sink`: where envelopes go once allowed
- `HopTop\Kit\Net`: `--offline` stops requested traffic, not this logging-class egress
- `HopTop\Kit\Api`: the guarded client for the tool's own API calls

## See also

- [Telemetry](../../README.md#telemetry)
- [Adopter guide: telemetry](../../../../../docs/adopters/guides/telemetry.md)
- [Event schema](../../../../docs/telemetry-event-schema.md)
- [Go reference: `go/runtime/telemetry`](../../../../../go/runtime/telemetry/README.md)
