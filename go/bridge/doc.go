// Package bridge is the kit/bridge protocol library: payload types,
// manifest loader, dispatch, and embeddable receiver.
//
// OS-level shells (macOS Share Extension, Shortcuts, browser extensions)
// deliver payloads to a shared local receiver; the receiver matches
// payloads against installed CLI manifests and dispatches to the
// highest-priority handler. Wire format is JSON — see contracts/bridge.proto
// for the schema source of truth.
//
// This file (doc.go) only carries the package comment. Public types
// live in payload.go (this commit), manifest.go, dispatch.go, etc.
package bridge
