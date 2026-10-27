package consent

import (
	"context"
	"time"
)

// Prompt is what a Prompter is asked to decide.
type Prompt struct {
	Capability    Capability
	Subject       string
	PromptVersion int
	// Reason is unset or expired: why there is no standing decision.
	Reason Reason
}

// Answer is the user's decision. ExpiresAt is set for Until only.
type Answer struct {
	Decision  Effect
	Duration  Duration
	ExpiresAt time.Time
}

// Prompter is the caller's UI seam: a TTY prompt, a dialog, a shell
// sheet. It returns an error when it cannot ask (no TTY, headless,
// dismissed); the request then fails closed. No prompter at all is a
// refusal too: Request never grants without one.
type Prompter interface {
	Ask(ctx context.Context, p Prompt) (Answer, error)
}

// PrompterFunc adapts a function to Prompter.
type PrompterFunc func(ctx context.Context, p Prompt) (Answer, error)

func (f PrompterFunc) Ask(ctx context.Context, p Prompt) (Answer, error) { return f(ctx, p) }
