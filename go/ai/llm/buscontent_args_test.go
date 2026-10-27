package llm_test

import (
	"bytes"
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

// Rules that need the key beside the value (gitleaks generic-api-key, a
// keyword-gated custom rule) still mask a value split from its key.
func TestRedactRequest_ArgumentsKeyContext(t *testing.T) {
	r, err := redact.NewEmbedded()
	require.NoError(t, err)
	_, err = r.AddRule("gated", `secret_token"?\s*[:=]\s*"?[A-Za-z0-9]{12}`, "")
	require.NoError(t, err)
	const apiKey = "Zq8Lm3Np7Rt2Vx9Kb4Hc6Jd1Fg5"
	for _, tc := range []struct {
		name, args, secret string
	}{
		{"generic api key", `{"api_key":"` + apiKey + `"}`, apiKey},
		{"nested generic api key", `{"cfg":[{"api_key":"` + apiKey + `"}]}`, apiKey},
		{"keyword-gated custom rule", `{"secret_token":"abcDEF123456"}`, "abcDEF123456"},
		{"card number as a JSON number", `{"n":4111111111111111}`, "4111111111111111"},
		{"card number in an array", `[4111111111111111,true,null]`, "4111111111111111"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			req := llm.Request{Messages: []llm.Message{{Role: "assistant", ToolCalls: []llm.ToolCall{
				{ID: "c", Name: "t", Arguments: json.RawMessage(tc.args)},
			}}}}
			got := llm.RedactRequest(r)(req).Messages[0].ToolCalls[0].Arguments
			require.True(t, json.Valid(got), "%s", got)
			assert.NotContains(t, string(got), tc.secret)
			var v any
			d := json.NewDecoder(bytes.NewReader(got))
			d.UseNumber()
			require.NoError(t, d.Decode(&v))
			assert.NotContains(t, decodedStrings(v)+numbers(v), tc.secret)
		})
	}
}

// Booleans, nulls and harmless numbers pass as they are.
func TestRedactRequest_ArgumentsKeepPlainValues(t *testing.T) {
	r, err := redact.NewEmbedded()
	require.NoError(t, err)
	args := `{"ok":true,"none":null,"n":3,"list":[1,"a",false]}`
	req := llm.Request{Messages: []llm.Message{{Role: "assistant", ToolCalls: []llm.ToolCall{
		{ID: "c", Name: "t", Arguments: json.RawMessage(args)},
	}}}}
	got := llm.RedactRequest(r)(req).Messages[0].ToolCalls[0].Arguments
	assert.JSONEq(t, args, string(got))
}

func numbers(v any) string {
	switch x := v.(type) {
	case json.Number:
		return x.String() + "\x00"
	case []any:
		s := ""
		for _, e := range x {
			s += numbers(e)
		}
		return s
	case map[string]any:
		s := ""
		for _, e := range x {
			s += numbers(e)
		}
		return s
	}
	return ""
}
