package ash

import "context"

// Lifecycle event topics.
const (
	TopicSessionCreated = "session.created"
	TopicSessionClosed  = "session.closed"
	TopicTurnStart      = "session.turn.start"
	TopicTurnDone       = "session.turn.done"
	TopicToolCalled     = "session.tool.called"
	TopicToolDone       = "session.tool.done"
	TopicSessionForked  = "session.forked"
)

// Publisher emits lifecycle events. A nil Publisher is safe — callers
// must guard with a nil check before calling Publish.
type Publisher interface {
	Publish(ctx context.Context, topic string, payload any) error
}
