package openai

import (
	"testing"

	"github.com/stretchr/testify/assert"

	"hop.top/kit/go/ai/llm"
)

// A size-cap param must be a positive byte count; anything else fails
// at construction, naming the scheme and the param.
func TestNew_LimitParamsRefused(t *testing.T) {
	for _, tc := range []struct{ param, value string }{
		{"max_body_bytes", "0"},
		{"max_body_bytes", "-1"},
		{"max_event_bytes", "lots"},
	} {
		_, err := New(llm.ResolvedConfig{
			URI:      llm.URI{Scheme: "groq", Model: "m"},
			Provider: llm.ProviderConfig{Model: "m", Params: map[string]string{tc.param: tc.value}},
		})
		assert.EqualError(t, err, "groq: "+tc.param+" must be a positive byte count", tc.value)
	}
}
