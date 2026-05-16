# Monitoring Probe Example

HTTP endpoint monitor using kit SDK packages without any CLI framework.
Same behavior in Go, TypeScript, and Python.

## Kit packages used

| Package      | Purpose                              |
|-------------|--------------------------------------|
| **config**  | Load targets + thresholds from YAML  |
| **bus**     | Emit kit.probe.check.{executed,alerted,recovered} events|
| **log**     | Structured check result logging      |
| **progress**| Scan progress across targets         |

## How it works

1. Load `probe.yaml` (shared config)
2. Create bus, subscribe to `kit.probe.#` events
3. For each target: HTTP request, measure latency, check status
4. Publish `kit.probe.check.executed` event; on failure:
   `kit.probe.check.alerted`; on recovery:
   `kit.probe.check.recovered`
5. Print summary

## Run

```sh
# Go
cd go && make run

# TypeScript
cd ts && npx tsx probe.ts

# Python
cd sdk/py && python probe.py
```

## Test

```sh
# All languages
make test

# Individual
cd go && make test
cd ts && npx vitest run
cd sdk/py && python -m pytest -v
```

## Sample output

```
INFO checking target=api url=https://httpbin.org/get progress=1/3
INFO event topic=kit.probe.check.executed target=api ok=true
INFO checking target=health url=https://httpbin.org/status/200 progress=2/3
INFO event topic=kit.probe.check.executed target=health ok=true
INFO checking target=slow url=https://httpbin.org/delay/10 progress=3/3
INFO event topic=kit.probe.check.alerted target=slow ok=false

=== Probe Summary ===
  [PASS] api          status=200 latency=245ms
  [PASS] health       status=200 latency=198ms
  [FAIL] slow         error="timeout"

Total: 3 | Passed: 2 | Failed: 1
```
