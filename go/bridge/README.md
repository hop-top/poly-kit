# bridge

kit/bridge protocol library: payload types and the manifest loader.

OS-level shells (macOS Share Extension, Shortcuts, browser extensions)
deliver payloads to a shared local receiver, which matches them against
installed CLI manifests and dispatches to the highest-priority handler.
Wire format is JSON; [`contracts/bridge.proto`](../../contracts/bridge.proto)
is the schema source of truth and names `payload.go` as its Go-side
mirror.

This package implements only the wire and manifest halves of that
picture:

- `payload.go` — `Payload` and its `Text`/`URL`/`File`/`Blob` oneof.
- `manifest.go` — `Manifest`, `Validate` and `Load`.

The receiver and the dispatch logic are not implemented here. A host
that wants them reads the manifests via `Load` and routes payloads
itself.

Single flat package, no sub-packages.
