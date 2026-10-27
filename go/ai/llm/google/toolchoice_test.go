package google_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"hop.top/kit/go/ai/llm"
)

func TestToolChoice_WireShape(t *testing.T) {
	tools := []llm.ToolDef{{Name: "get_weather"}, {Name: "9lives"}}
	for _, tc := range []struct {
		name   string
		choice *llm.ToolChoice
		want   string // toolConfig JSON; "" means absent
	}{
		{"unset", nil, ""},
		{"auto", &llm.ToolChoice{Mode: llm.ToolChoiceAuto}, `{"functionCallingConfig":{"mode":"AUTO"}}`},
		{"any", &llm.ToolChoice{Mode: llm.ToolChoiceAny}, `{"functionCallingConfig":{"mode":"ANY"}}`},
		{"none", &llm.ToolChoice{Mode: llm.ToolChoiceNone}, `{"functionCallingConfig":{"mode":"NONE"}}`},
		{"named, by sent name", &llm.ToolChoice{Mode: llm.ToolChoiceTool, Name: "9lives"},
			`{"functionCallingConfig":{"mode":"ANY","allowedFunctionNames":["_9lives"]}}`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var got json.RawMessage
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				var body map[string]json.RawMessage
				_ = json.NewDecoder(r.Body).Decode(&body)
				got = body["toolConfig"]
				writeJSON(w, geminiResponse("done", "STOP", 1, 1))
			}))
			t.Cleanup(srv.Close)
			p := mustNew(t, srv.URL, "gemini-2.0-flash").(llm.ToolCaller)
			_, err := p.CallWithTools(context.Background(), llm.Request{
				Messages:   []llm.Message{{Role: "user", Content: "hi"}},
				ToolChoice: tc.choice,
			}, tools)
			require.NoError(t, err)
			if tc.want == "" {
				assert.Empty(t, got)
				return
			}
			assert.JSONEq(t, tc.want, string(got))
		})
	}
}

func TestToolChoice_Rejected(t *testing.T) {
	var sent atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		sent.Add(1)
		writeJSON(w, geminiResponse("done", "STOP", 1, 1))
	}))
	t.Cleanup(srv.Close)
	p := mustNew(t, srv.URL, "gemini-2.0-flash")
	req := llm.Request{
		Messages:   []llm.Message{{Role: "user", Content: "hi"}},
		ToolChoice: &llm.ToolChoice{Mode: llm.ToolChoiceAuto},
	}
	_, err := p.(llm.Completer).Complete(context.Background(), req)
	assert.EqualError(t, err, `gemini: tool_choice "auto" needs tools`)
	_, err = p.(llm.Streamer).Stream(context.Background(), req)
	assert.EqualError(t, err, `gemini: tool_choice "auto" needs tools`)
	assert.Zero(t, sent.Load())
}
