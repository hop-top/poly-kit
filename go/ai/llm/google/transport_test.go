package google_test

import (
	"context"
	"testing"

	"hop.top/kit/go/ai/llm"
	"hop.top/kit/go/ai/llm/internal/transporttest"
)

func TestTransport_Complete(t *testing.T) {
	transporttest.Check(t, transporttest.Spec{
		URI:         "google://gemini-2.0-flash?api_key=test-key",
		ContentType: "application/json",
		Body:        `{"candidates":[{"content":{"role":"model","parts":[{"text":"hi"}]},"finishReason":"STOP"}]}`,
		Call: func(p llm.Provider) error {
			_, err := p.(llm.Completer).Complete(context.Background(), llm.Request{
				Messages: []llm.Message{{Role: "user", Content: "Hi"}},
			})
			return err
		},
	})
}
