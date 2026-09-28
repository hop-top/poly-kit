package cmdsurface

import (
	"testing"
	"time"

	"github.com/spf13/cobra"

	"hop.top/kit/go/console/cli/idemstore"
)

// The deprecated MountMCP surface takes a call's idempotency key from
// the same sources the SDK surface does — params._meta, else the
// Idempotency-Key header — so a retry replays and marks the result.
func TestIdempotency_MCPLegacyCarriesTheKey(t *testing.T) {
	run := &idemRunner{}
	srv := newMCPHarness(t, func(root *cobra.Command) (*Bridge, error) {
		return New(root, WithRunner(run),
			WithIdempotency(NewIdempotencyLedger(idemstore.Memory()), time.Hour)), nil
	})
	call := func(meta map[string]any) map[string]any {
		params := map[string]any{"name": "ping"}
		if meta != nil {
			params["_meta"] = meta
		}
		return map[string]any{"jsonrpc": "2.0", "id": 1, "method": "tools/call", "params": params}
	}
	replayed := func(res map[string]any) bool {
		m, _ := res["_meta"].(map[string]any)
		return m[MCPMetaIdempotentReplayed] == true
	}
	for _, c := range []struct {
		name string
		hdr  map[string]string
		meta map[string]any
	}{
		{"header", map[string]string{"Idempotency-Key": "k1"}, nil},
		{"_meta", nil, map[string]any{MCPMetaIdempotencyKey: "k2"}},
	} {
		before := run.calls.Load()
		_, first := postRPC(t, srv, c.hdr, call(c.meta))
		_, second := postRPC(t, srv, c.hdr, call(c.meta))
		if first.Error != nil || second.Error != nil {
			t.Fatalf("%s: %+v %+v", c.name, first.Error, second.Error)
		}
		if replayed(resultAsMap(t, first)) || !replayed(resultAsMap(t, second)) {
			t.Fatalf("%s: replayed = %v, %v; want false, true", c.name,
				replayed(resultAsMap(t, first)), replayed(resultAsMap(t, second)))
		}
		if got := run.calls.Load() - before; got != 1 {
			t.Fatalf("%s: runner ran %d times, want 1", c.name, got)
		}
	}
}
