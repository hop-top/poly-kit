# bridge

kit/bridge protocol library: payload types, the manifest loader, and
the matcher that routes one to the other.

OS-level shells (macOS Share Extension, Shortcuts, browser extensions)
deliver payloads to a shared local receiver, which matches them against
installed CLI manifests and dispatches to the highest-priority handler.
Wire format is JSON; [`contracts/bridge.proto`](../../contracts/bridge.proto)
is the schema source of truth and names `payload.go` as its Go-side
mirror.

This package implements the wire, manifest and routing thirds of that
picture:

- `payload.go` — `Payload` and its `Text`/`URL`/`File`/`Blob` oneof.
- `manifest.go` — `Manifest`, `Validate` and `Load`.
- `match.go` — `Match`, selecting the manifest and accept rule that
  should handle a payload, and `reason.go`'s `NoMatchReason` for when
  none does.

The receiver itself is not implemented here: nothing in this package
runs a process, opens a socket or speaks HTTP. A host reads the
manifests via `Load`, asks `Match` who should handle each payload, and
executes the winner itself.

Single flat package, no sub-packages.
