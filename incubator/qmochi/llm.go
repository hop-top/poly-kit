package qmochi

import "context"

// Completer produces a single completion for the given messages.
type Completer interface {
	Complete(ctx context.Context, messages []Message) (string, error)
}

// Message is a single role+content pair.
type Message struct {
	Role    string
	Content string
}
