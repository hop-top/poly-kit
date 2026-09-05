# telemetry

## What it answers

What a Python adopter needs before emitting usage telemetry: the mode gate (`off`, `anon`, `full`), the anonymous rotatable install id, the consent reader, the redactor, and a fire-and-forget `Client` with jsonl and https sinks. Default-denied: a fresh install never emits. Wrong module for Factor 11 provenance metadata on payloads (`hop_top_kit.provenance`) and for the compliance checker (`hop_top_kit.compliance`).

## Use it when

- record an event: `Client(...)`, `client.record(event, attrs)`, `client.shutdown()`
- know which tier applies: `resolve_mode()`
- read or rotate the install id: `get_install_id()`, `rotate()`
- check the operator's decision: `load_consent()`
- scrub before your own sink: `redact_string(s)`, or `redact` from `hop_top_kit.telemetry.redact`

## Quick start

```python
from hop_top_kit.telemetry import Client

client = Client(sink="jsonl", sink_file="/tmp/kit-events.jsonl", sdk_version="0.0.0")
client.record("cmd.invoked", {"command": "launch", "exit_code": 0})
client.shutdown()
```

`record` is a no-op until consent is granted and `KIT_TELEMETRY_MODE` is `anon` or `full`.

## Contract

- Envelope shape is [`sdk/docs/telemetry-event-schema.md`](../../../docs/telemetry-event-schema.md); keys are wire snake_case.
- `record` is sync-callable from any context and returns in about a millisecond; the queue is bounded and drops count.
- Consent and mode are consulted on each `record`, so a live consent flip takes effect without restart.
- The `https` sink needs the `telemetry-https` extra (`httpx`); the `jsonl` sink has no extra dependency.
- Redactor placeholders are `<redacted:email>`, `<redacted:ipv4>`, `<redacted:ipv6>`, `<redacted:token>`, and `$HOME` for home-path prefixes.
- The envelope carries a free-form `event` plus `attrs` where Go carries `CommandPath`; this is a documented divergence.
- Parity: [`sdk/tests/cross-lang/expected/envelope.json`](../../../tests/cross-lang/expected/envelope.json), driven by `sdk/tests/cross-lang/run.sh` from [`fixtures/consent.yaml`](../../../tests/cross-lang/fixtures/consent.yaml) and [`fixtures/install_id.bytes`](../../../tests/cross-lang/fixtures/install_id.bytes).

## Neighbours

- `hop_top_kit.provenance`: `_meta` on output payloads
- `hop_top_kit.compliance`: 12-factor checks, including the telemetry consent factor
- `hop_top_kit.xdg`: state and config paths the install id and consent file live under
- Go reference: [`go/runtime/telemetry`](../../../../go/runtime/telemetry/README.md); TypeScript port: [`sdk/ts/src/telemetry`](../../../ts/src/telemetry/README.md)

## See also

- [`sdk/py/README.md`](../../README.md), section "Telemetry": what is collected, opt in, opt out
- [`docs/adopters/guides/telemetry.md`](../../../../docs/adopters/guides/telemetry.md)
- [`docs/adopters/reference/telemetry-compliance.md`](../../../../docs/adopters/reference/telemetry-compliance.md)
- [`sdk/tests/cross-lang/README.md`](../../../tests/cross-lang/README.md)
