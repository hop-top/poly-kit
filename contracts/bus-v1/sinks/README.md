# bus-v1/sinks

## What it answers

What bytes must every kit SDK's bus sinks write? The stdout sink's line, the JSONL sink's line
and the env sink's reporter messages, for a fixed set of events and env values. Wrong place for
topic grammar (`..`) or the event's own JSON form (`../event_json.json`).

## Use it when

- you port the bus sinks to an SDK: its runner must reproduce each golden byte for byte
- you change Go sink output: change Go, regenerate, then make every port pass

## Quick start

```sh
go test ./go/runtime/bus/ -run '^TestSinkConformance$'
UPDATE_SINK_GOLDENS=1 go test ./go/runtime/bus/ -run '^TestSinkConformance$'  # regenerate
```

## Contract

Go is canonical; the goldens are written by the Go runner. Inputs carry `version: "v1"` and a
`description`; runners reject unknown fields.

| File | Pins |
|------|------|
| `events.json` | input events: topic, source, timestamp, payload (integers are int64/uint64, other numbers float64) |
| `stdout.txt` | `[timestamp] topic source: payload` per event, payload as Go's `%v`, cut at 120 runes |
| `jsonl.txt` | one JSON line per event, Go's `encoding/json` bytes, null payload omitted |
| `env_cases.json` | `KIT_BUS_SINK` / `KIT_BUS_SINK_PATH` values that attach no sink |
| `env_reports.txt` | reporter lines those cases produce, in order |

## Runners

| Port | Runner |
|------|--------|
| Go | `go/runtime/bus/sink_conformance_test.go` (`TestSinkConformance`) |
| Rust | `sdk/rs/tests/bus_sinks_conformance.rs` |
