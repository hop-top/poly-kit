# bus-v1/cloudevents

## What it answers

What every kit SDK must accept, reject and emit when it shapes bus events as CloudEvents 1.0: the structured-mode JSON codec, validation with exact error texts, the `Event` mapping, and network frame detection. Wrong place for topic grammar (the parent `bus-v1` corpus) and for transport behaviour beyond frame shape.

## Use it when

- you port CloudEvents shaping to an SDK: run these vectors before anything else
- you change the Go codec or mapping: add a vector first, then make every port pass it

## Quick start

```sh
go test ./go/runtime/bus/ -run CloudEventsConformance
```

## Contract

Go is canonical; a runner failure means the implementation drifted. Every file carries `version: "v1"` and a `description` stating what it pins.

| File | Pins |
|------|------|
| `decode.json` | raw document text to `CloudEvent`: canonical re-encode, the mapped `Event`, exact error texts, check order, time grammar, base64 strictness, extension rules |
| `encode.json` | `Event` to `CloudEvent`: field mapping, id minting, content-type rules, exact bytes (`text`) where member order matters |
| `frames.json` | network frames: kit vs CloudEvents detection by the exact `specversion` key, origin handling, frame encoding in both formats |

Conventions:

- JSON outputs are compared as values; `text` cases compare bytes.
- Timestamps in `event` objects compare as instants; `time` in CloudEvents output compares as an exact string.
- Extension integers in vectors are JSON integers; ports hold them as 64-bit signed integers.
- Vectors avoid invalid UTF-8, lone surrogates and duplicate keys, where JSON libraries disagree.
- Frame rejections assert the drop only; their error text is not contract.

## Runners

| Port | Runner |
|------|--------|
| Go | `go/runtime/bus/cloudevents_conformance_test.go` (`TestCloudEventsConformance*`) |

## See also

- [`docs/adopters/reference/bus-api.md`](../../../docs/adopters/reference/bus-api.md): CloudEvents section
- [`docs/contracts/event-topics.md`](../../../docs/contracts/event-topics.md): topic as CloudEvents `type`
