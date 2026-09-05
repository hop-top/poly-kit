// Package bridge is the kit/bridge protocol library: payload types,
// the manifest loader, and the matcher that routes one to the other.
//
// OS-level shells (macOS Share Extension, Shortcuts, browser extensions)
// deliver payloads to a shared local receiver; the receiver matches
// payloads against installed CLI manifests and dispatches to the
// highest-priority handler. Wire format is JSON — see contracts/bridge.proto
// for the schema source of truth.
//
// This package implements the wire, manifest and routing thirds of that
// picture: Payload (with its Text/URL/File/Blob oneof) in payload.go,
// Manifest plus Load in manifest.go, and Match in match.go, which names
// the manifest and accept rule that should handle a given payload. The
// receiver itself is not implemented here: nothing in this package runs
// a process, opens a socket or speaks HTTP. A host reads the manifests
// via Load, asks Match who should handle each payload, and executes the
// winner itself.
package bridge
