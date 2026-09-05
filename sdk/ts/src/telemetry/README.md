# telemetry

## What it answers

What a TS adopter needs before emitting usage telemetry: the mode gate (`off`, `anon`, `full`), the anonymous rotatable install id, the consent reader, the redactor, and a non-blocking `Client` with jsonl and https sinks. Default-denied: a fresh install never emits. Wrong module for Factor 11 provenance metadata on payloads (`@hop-top/kit/provenance`) and for the compliance checker (`@hop-top/kit/compliance`).

## Use it when

- record an event: `new Client(opts)`, `client.record(event, attrs)`, `await client.shutdown(ms)`
- know which tier applies: `resolveMode(process.env)`
- read or rotate the install id: `getInstallId()`, `rotate()`
- check the operator's decision: `loadConsent()`
- scrub before your own sink: `redact(value)`, `redactString(s)`

## Quick start

```ts
import { Client } from '@hop-top/kit/telemetry';

const client = new Client({ sink: 'jsonl', sinkFile: '/tmp/kit-events.jsonl', sdkVersion: '0.0.0' });
client.record('cmd.invoked', { command: 'launch', exit_code: 0 });
await client.shutdown(5_000);
```

`record` is a no-op until consent is granted and `KIT_TELEMETRY_MODE` is `anon` or `full`.

## Contract

- Envelope shape is [`sdk/docs/telemetry-event-schema.md`](../../../docs/telemetry-event-schema.md); keys are wire snake_case.
- `record` returns synchronously and never throws; the queue is bounded and drops count.
- Consent and mode are consulted on each `record`, so a live consent flip takes effect without restart.
- Redactor placeholders are `<redacted:email>`, `<redacted:ipv4>`, `<redacted:ipv6>`, `<redacted:token>`, and `$HOME` for home-path prefixes.
- The envelope carries a free-form `event` plus `attrs` where Go carries `CommandPath`; this is a documented divergence.
- Parity: [`sdk/tests/cross-lang/expected/envelope.json`](../../../tests/cross-lang/expected/envelope.json), driven by `sdk/tests/cross-lang/run.sh` from [`fixtures/consent.yaml`](../../../tests/cross-lang/fixtures/consent.yaml) and [`fixtures/install_id.bytes`](../../../tests/cross-lang/fixtures/install_id.bytes).

## Neighbours

- `@hop-top/kit/provenance`: `_meta` on output payloads
- `@hop-top/kit/compliance`: 12-factor checks, including the telemetry consent factor
- `@hop-top/kit/xdg`: state and config paths the install id and consent file live under
- Go reference: [`go/runtime/telemetry`](../../../../go/runtime/telemetry/README.md); Python port: [`hop_top_kit/telemetry`](../../../py/hop_top_kit/telemetry/README.md)

## See also

- [`sdk/ts/README.md`](../../README.md), section "Telemetry": env vars, redactor, envelope
- [`docs/adopters/guides/telemetry.md`](../../../../docs/adopters/guides/telemetry.md)
- [`docs/adopters/reference/telemetry-compliance.md`](../../../../docs/adopters/reference/telemetry-compliance.md)
- [`sdk/tests/cross-lang/README.md`](../../../tests/cross-lang/README.md)
