package openai

import (
	oai "github.com/openai/openai-go"
	"github.com/openai/openai-go/packages/param"

	"hop.top/kit/go/ai/llm"
)

// SupportsLogprobs implements [llm.LogprobCompleter]: Chat Completions
// takes logprobs and top_logprobs. A compatible server without them
// ignores the fields; Complete then fails with a capability error.
func (a *Adapter) SupportsLogprobs() bool { return true }

var _ llm.LogprobCompleter = (*Adapter)(nil)

// applyLogprobs maps c onto p: logprobs true, and top_logprobs when
// c asks for alternatives.
func applyLogprobs(p *oai.ChatCompletionNewParams, c *llm.LogprobsConfig) {
	if c == nil {
		return
	}
	p.Logprobs = param.NewOpt(true)
	if c.TopK > 0 {
		p.TopLogprobs = param.NewOpt(int64(c.TopK))
	}
}

// noStreamLogprobs refuses a request that asks for logprobs on a call
// that cannot return them: streams and tool-calling turns.
func (a *Adapter) noStreamLogprobs(req llm.Request) error {
	if req.Logprobs != nil {
		return llm.NoLogprobs(a.scheme)
	}
	return nil
}

// mapLogprobs reads the choice's logprobs when the request asked.
// Missing from a reply that generated tokens means the server ignored
// the request: not supported, not "no tokens".
func (a *Adapter) mapLogprobs(want *llm.LogprobsConfig, c *oai.ChatCompletion) ([]llm.TokenLogprob, error) {
	if want == nil {
		return nil, nil
	}
	var content []oai.ChatCompletionTokenLogprob
	if len(c.Choices) > 0 {
		content = c.Choices[0].Logprobs.Content
	}
	if len(content) == 0 {
		if c.Usage.CompletionTokens == 0 {
			return nil, nil
		}
		return nil, llm.NoLogprobs(a.scheme)
	}
	out := make([]llm.TokenLogprob, len(content))
	for i, t := range content {
		out[i] = llm.TokenLogprob{Token: t.Token, Logprob: t.Logprob}
		for _, alt := range t.TopLogprobs {
			out[i].Top = append(out[i].Top, llm.TopLogprob{Token: alt.Token, Logprob: alt.Logprob})
		}
	}
	return out, nil
}
