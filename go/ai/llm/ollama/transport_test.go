package ollama_test

import (
	"context"
	"testing"

	"hop.top/kit/go/ai/llm"
	"hop.top/kit/go/ai/llm/internal/transporttest"
)

const chatBody = `{"message":{"role":"assistant","content":"hi"},"done":true}`

func TestTransport_Complete(t *testing.T) {
	transporttest.Check(t, transporttest.Spec{
		URI:         "ollama://llama3?base_url=http://ollama.transport.test",
		ContentType: "application/json",
		Body:        chatBody,
		Call: func(p llm.Provider) error {
			_, err := p.(llm.Completer).Complete(context.Background(), llm.Request{
				Messages: []llm.Message{{Role: "user", Content: "Hi"}},
			})
			return err
		},
	})
}

func TestTransport_GenerateImage(t *testing.T) {
	transporttest.Check(t, transporttest.Spec{
		URI:         "ollama://llava?base_url=http://ollama.transport.test",
		ContentType: "application/json",
		Body:        `{"images":["aGk="],"done":true}`,
		Call: func(p llm.Provider) error {
			_, err := p.(llm.ImageGenerator).GenerateImage(context.Background(), llm.ImageRequest{Prompt: "x"})
			return err
		},
	})
}
