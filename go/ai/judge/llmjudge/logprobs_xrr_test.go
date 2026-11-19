package llmjudge_test

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"hop.top/kit/go/ai/judge"
	"hop.top/kit/go/ai/judge/llmjudge"
	"hop.top/kit/go/ai/llm"
	"hop.top/kit/go/ai/llm/llmxrr"
	_ "hop.top/kit/go/ai/llm/ollama"
)

const (
	cassetteDir = "testdata/cassettes"
	// cassetteModel is the model the cassette was recorded with.
	cassetteModel = "qwen2.5vl:3b"
	// cassetteWant is the label that recording chose.
	cassetteWant = "act_now"
)

// TestLogprobsOllamaCassette replays a real Ollama /api/chat exchange:
// structured output plus top_logprobs, read back as a distribution.
// Re-record against a local daemon:
//
//	XRR_MODE=record go test -run TestLogprobsOllamaCassette ./go/ai/judge/llmjudge
func TestLogprobsOllamaCassette(t *testing.T) {
	tr, err := llmxrr.New(cassetteDir)
	require.NoError(t, err)
	p, err := llm.Resolve("ollama://"+cassetteModel+"?base_url=http://localhost:11434", llm.WithTransport(tr))
	require.NoError(t, err)
	defer p.Close()

	c := judge.Case{
		State: map[string]any{"subject": "Invoice 4411 overdue", "body": "Payment is 30 days late; supplier threatens to pause delivery."},
		Questions: []judge.Question{{
			Name:         "route",
			Instructions: "Where does this message go?",
			Kind:         judge.KindChoice,
			Options: []judge.Option{
				{Value: "act_now", Description: "needs action today"},
				{Value: "schedule", Description: "can wait for a planned slot"},
				{Value: "ignore", Description: "no action needed"},
			},
		}},
	}
	temp := 0.0
	v, err := llmjudge.New(p.(llm.Completer),
		llmjudge.Mode(llmjudge.Logprobs), llmjudge.Model(cassetteModel), llmjudge.Temperature(temp),
	).Judge(context.Background(), c)
	require.NoError(t, err)

	a := v.Answers["route"]
	assert.Equal(t, "logprobs", a.Source)
	assert.Len(t, a.Probs, 3)
	assert.Equal(t, cassetteWant, a.Value)
	assert.Greater(t, a.Confidence, 0.5)
	assert.NoError(t, judge.ValidateVerdict(c, v))
}

func TestCassettesClean(t *testing.T) {
	llmxrr.AssertCassettesClean(t, cassetteDir)
}
