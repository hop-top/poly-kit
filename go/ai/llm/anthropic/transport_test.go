package anthropic_test

import (
	"context"
	"testing"

	"hop.top/kit/go/ai/llm"
	"hop.top/kit/go/ai/llm/internal/transporttest"
)

func TestTransport_Complete(t *testing.T) {
	transporttest.Check(t, transporttest.Spec{
		URI:         "anthropic://claude-sonnet-4-5?api_key=test-key",
		ContentType: "application/json",
		Body:        string(anthropicResponse("hi", "end_turn", 1, 1)),
		Call: func(p llm.Provider) error {
			_, err := p.(llm.Completer).Complete(context.Background(), llm.Request{
				Messages: []llm.Message{{Role: "user", Content: "Hi"}},
			})
			return err
		},
	})
}
