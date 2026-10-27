package llm_test

import (
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"hop.top/kit/go/ai/llm"
	"hop.top/kit/go/core/redact"
)

// A secret whose JSON form needs escapes (quote, backslash, a \u
// escape, a newline) is matched on its decoded text, not the wire form.
func TestRedactRequest_ArgumentsMatchDecodedStrings(t *testing.T) {
	r := redact.New()
	_, err := r.AddRule("pw", `p"w\\d\n<é>`, "")
	require.NoError(t, err)
	_, err = r.AddRule("key", `secret-key`, "")
	require.NoError(t, err)

	for _, tc := range []struct {
		name string
		args string
	}{
		{"escaped value", `{"pw":"p\"w\\d\n<é>"}`},
		{"escaped key", `{"p\"w\\d\n<é>":1}`},
		{"nested", `{"a":[{"b":"x secret-key y"}]}`},
		{"not JSON", `{"pw": secret-key`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			req := llm.Request{Messages: []llm.Message{{Role: "assistant", ToolCalls: []llm.ToolCall{
				{ID: "c", Name: "t", Arguments: json.RawMessage(tc.args)},
			}}}}
			out := llm.RedactRequest(r)(req)
			got := out.Messages[0].ToolCalls[0].Arguments
			require.True(t, json.Valid(got), "%s", got)
			var v any
			require.NoError(t, json.Unmarshal(got, &v))
			flat, _ := json.Marshal(v)
			assert.NotContains(t, decodedStrings(v), "secret-key")
			assert.NotContains(t, decodedStrings(v), "p\"w\\d\n<é>", "got %s", flat)
			assert.Contains(t, string(flat), "REDACTED")
		})
	}
}

// decodedStrings joins every string (keys included) in v.
func decodedStrings(v any) string {
	switch x := v.(type) {
	case string:
		return x + "\x00"
	case []any:
		s := ""
		for _, e := range x {
			s += decodedStrings(e)
		}
		return s
	case map[string]any:
		s := ""
		for k, e := range x {
			s += k + "\x00" + decodedStrings(e)
		}
		return s
	}
	return ""
}
