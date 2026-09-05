// Package bridge is the kit/bridge protocol library: payload types and
// the manifest loader.
//
// OS-level shells (macOS Share Extension, Shortcuts, browser extensions)
// deliver payloads to a shared local receiver; the receiver matches
// payloads against installed CLI manifests and dispatches to the
// highest-priority handler. Wire format is JSON — see contracts/bridge.proto
// for the schema source of truth.
//
// This package implements only the wire and manifest halves of that
// picture: Payload (with its Text/URL/File/Blob oneof) in payload.go,
// and Manifest plus Load in manifest.go. The receiver and the dispatch
// logic are not implemented here; a host that wants them reads the
// manifests via Load and routes payloads itself.
package bridge
