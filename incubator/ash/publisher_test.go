package ash

import (
	"context"
	"testing"
)

func TestTopicConstants(t *testing.T) {
	expected := map[string]string{
		"session.created":     TopicSessionCreated,
		"session.closed":      TopicSessionClosed,
		"session.turn.start":  TopicTurnStart,
		"session.turn.done":   TopicTurnDone,
		"session.tool.called": TopicToolCalled,
		"session.tool.done":   TopicToolDone,
		"session.forked":      TopicSessionForked,
	}

	for want, got := range expected {
		if got != want {
			t.Errorf("topic %q: expected %q, got %q", want, want, got)
		}
	}
}

func TestNilPublisherSafe(t *testing.T) {
	// Verify that a nil Publisher causes no runtime panic when
	// used via Runtime.emitEvent.
	rt := &Runtime{
		session:   &Session{ID: "s-1"},
		publisher: nil,
	}

	// Should not panic.
	rt.emitEvent(context.TODO(), TopicTurnStart, nil)
}
