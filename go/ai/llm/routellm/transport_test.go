package routellm_test

import (
	"context"
	"testing"

	"hop.top/kit/go/ai/llm"
	"hop.top/kit/go/ai/llm/internal/transporttest"
	_ "hop.top/kit/go/ai/llm/routellm"
)

func TestTransport_Complete(t *testing.T) {
	transporttest.Check(t, transporttest.Spec{
		URI:         "routellm://m?base_url=http://routellm.transport.test",
		ContentType: "application/json",
		Body: `{"id":"c","object":"chat.completion","created":1,"model":"m",` +
			`"choices":[{"index":0,"message":{"role":"assistant","content":"ok"},"finish_reason":"stop"}],` +
			`"usage":{"prompt_tokens":1,"completion_tokens":1,"total_tokens":2}}`,
		Call: func(p llm.Provider) error {
			_, err := p.(llm.Completer).Complete(context.Background(), llm.Request{
				Messages: []llm.Message{{Role: "user", Content: "Hi"}},
			})
			return err
		},
	})
}
