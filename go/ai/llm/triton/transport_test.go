package triton_test

import (
	"context"
	"testing"

	"hop.top/kit/go/ai/llm"
	"hop.top/kit/go/ai/llm/internal/transporttest"
	"hop.top/kit/go/ai/llm/triton"
)

func TestTransport_Score(t *testing.T) {
	transporttest.Check(t, transporttest.Spec{
		URI:         "triton://mf?base_url=http://triton.transport.test:8000",
		ContentType: "application/json",
		Body:        `{"outputs":[{"name":"output","shape":[1,1],"datatype":"FP64","data":[0.75]}]}`,
		Call: func(p llm.Provider) error {
			_, err := p.(triton.Scorer).Score(context.Background(), []float32{1})
			return err
		},
	})
}
