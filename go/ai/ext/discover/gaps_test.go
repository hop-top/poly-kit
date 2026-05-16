package discover_test

// Gap tests for `hop.top/kit/go/ai/ext/discover`. Surfaced by the
// observation that the plugin contract is implemented but never
// documented as a public protocol — non-Go sidecar authors have no
// spec to target.

import (
	"os"
	"path/filepath"
	"testing"
)

// Gap: discover plugin contract is not documented as a public
// protocol.
//
// The Go side has discover.go (~4.6K LOC), Capabilities() returning
// CapDiscover, and tests asserting the wire-level Found.Init /
// Interrogate behavior. But there is no `docs/contracts/ext-discover.md`
// (or similar) that a non-Go sidecar author could open and learn:
//
//   - what env vars / args the plugin is invoked with
//   - the JSON payload shape for Init (capabilities advertisement)
//   - the JSON payload shape for Interrogate
//   - exit-code conventions
//   - timeout / cancellation semantics
//
// Without that doc, the only way to write a Python or TS sidecar is
// to read the Go test fixtures. That's the gap.
//
// Desired output:
//
//   - docs/contracts/ext-discover-protocol.md with one section per
//     phase (Init, Interrogate, Shutdown), each with payload schema,
//     example, and a non-Go example invocation.
func TestGap_ExtDiscoverProtocol_Undocumented(t *testing.T) {
	docPath := filepath.Join("..", "..", "..", "..", "docs", "contracts", "ext-discover-protocol.md")
	info, err := os.Stat(docPath)
	if err != nil {
		t.Fatalf("expected protocol doc at %s: %v", docPath, err)
	}
	if info.Size() == 0 {
		t.Fatalf("protocol doc at %s is empty", docPath)
	}
}
