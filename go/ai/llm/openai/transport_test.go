package openai_test

import (
	"context"
	"testing"

	"hop.top/kit/go/ai/llm"
	"hop.top/kit/go/ai/llm/internal/transporttest"
)

func TestTransport_Complete(t *testing.T) {
	transporttest.Check(t, transporttest.Spec{
		URI:         "openai://gpt-4o?api_key=test-key",
		ContentType: "application/json",
		Body:        okCompletion,
		Call: func(p llm.Provider) error {
			_, err := p.(llm.Completer).Complete(context.Background(), llm.Request{
				Messages: []llm.Message{{Role: "user", Content: "Hi"}},
			})
			return err
		},
	})
}
