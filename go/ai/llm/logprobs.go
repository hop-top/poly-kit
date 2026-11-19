package llm

import (
	"fmt"
	"slices"

	llmerrors "hop.top/kit/go/ai/llm/errors"
)

// MaxTopLogprobs is the most alternatives per token a request may ask
// for; OpenAI's limit, and the one kit holds every adapter to.
const MaxTopLogprobs = 20

// LogprobsConfig asks a completion for the log-probability of each
// token it generated. TopK also asks for the TopK most likely
// alternatives at each position; zero asks for the sampled token's
// alone.
type LogprobsConfig struct {
	TopK int
}

// TokenLogprob is one generated token and its log-probability. Top
// lists the most likely tokens at that position, best first, when the
// request set [LogprobsConfig.TopK]; the sampled token is usually, not
// always, among them.
type TokenLogprob struct {
	Token   string
	Logprob float64
	Top     []TopLogprob
}

// TopLogprob is one alternative at a token position.
type TopLogprob struct {
	Token   string
	Logprob float64
}

// LogprobCompleter is a [Completer] that can return log-probabilities
// ([Response.Logprobs]) for a request that sets [Request.Logprobs].
// SupportsLogprobs says whether this adapter sends the request; a
// server behind it may still not return them, which a call reports as
// [llmerrors.ErrCapabilityNotSupported]. Only Complete returns them: a
// stream or a tool-calling call with Logprobs set is refused.
type LogprobCompleter interface {
	Completer
	SupportsLogprobs() bool
}

// CheckLogprobs reports why c cannot go on a request: a TopK below 0
// or above [MaxTopLogprobs]. A nil c is valid.
func CheckLogprobs(c *LogprobsConfig) error {
	if c == nil {
		return nil
	}
	if c.TopK < 0 || c.TopK > MaxTopLogprobs {
		return fmt.Errorf("logprobs top_k must be 0-%d, got %d", MaxTopLogprobs, c.TopK)
	}
	return nil
}

// NoLogprobs is the error for an adapter, or a call kind, that does
// not return log-probabilities; not fallbackable.
func NoLogprobs(provider string) error {
	return llmerrors.NewCapabilityNotSupported("logprobs", provider)
}

func cloneLogprobs(lp []TokenLogprob) []TokenLogprob {
	out := slices.Clone(lp)
	for i := range out {
		out[i].Top = slices.Clone(out[i].Top)
	}
	return out
}
