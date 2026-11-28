# Events, jobs and runtime

Open when a tool publishes in-process events or emits usage telemetry.
Reference: [telemetry guide](https://github.com/hop-top/poly-kit/blob/next/docs/adopters/guides/telemetry.md). Both modules
carry cell `unverified` in [coverage](coverage.md): test them, and do not
promise wire or topic parity with Go.

## Decisions

- In-process events: `hop_top_kit.bus`. `create_bus()` gives a `Bus` over a
  memory adapter; `subscribe(pattern, handler)` returns an unsubscribe
  callable. Topics are dot-separated; `*` matches one segment, `#` matches
  trailing segments. The `Migrator` publishes lifecycle events to a bus you
  pass it. No SQLite adapter, remote streams or CloudEvents here.
- Usage telemetry: `hop_top_kit.telemetry`. `Client(sink="jsonl", ...)`,
  `record(event, attrs)`, `shutdown()`. Nothing is emitted until consent is
  granted and `KIT_TELEMETRY_MODE` is not `off`. The `https` sink needs the
  `telemetry-https` extra. Redaction and consent here cover telemetry only.

## Not here

Job queue, notifications, domain building blocks, entity sync and peer mesh
have no module in this port: [other ports](other-ports.md).
