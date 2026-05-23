package buswf_test

import (
	"testing"

	"hop.top/kit/cmd/kit/init/buswf"
	"hop.top/kit/go/runtime/bus"
)

// TestTopicsPassValidateTopic is the load-bearing test for T-0776: a
// previous dispatch was STOPped specifically because two of the four
// pinned topics were 3-segment and failed bus.ValidateTopic. Asserting
// all four pass here means a future spec edit that breaks the shape
// fails CI immediately.
func TestTopicsPassValidateTopic(t *testing.T) {
	t.Parallel()
	for _, topic := range buswf.Topics() {
		topic := topic
		t.Run(string(topic), func(t *testing.T) {
			t.Parallel()
			if err := bus.ValidateTopic(topic); err != nil {
				t.Fatalf("ValidateTopic(%q): %v", topic, err)
			}
		})
	}
}

// TestTopicsAreCanonical pins the exact four strings (spec §2) so a
// drift in either direction (spec or code) is caught at PR time.
func TestTopicsAreCanonical(t *testing.T) {
	t.Parallel()
	want := []bus.Topic{
		"github.pr.run.completed",
		"github.pr.comment.created",
		"github.pr.pull.merged",
		"github.pr.pull.closed",
	}
	got := buswf.Topics()
	if len(got) != len(want) {
		t.Fatalf("Topics(): got %d topics, want %d", len(got), len(want))
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("Topics()[%d]: got %q, want %q", i, got[i], want[i])
		}
	}
}
