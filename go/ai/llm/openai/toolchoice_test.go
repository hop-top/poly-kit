package openai

import (
	"context"
	"encoding/json"
	"net/http"
	"sync/atomic"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"hop.top/kit/go/ai/llm"
)

func TestToolChoice_WireShape(t *testing.T) {
	tools := []llm.ToolDef{{Name: "get_weather"}, {Name: "pane.open"}}
	for _, tc := range []struct {
		name   string
		choice *llm.ToolChoice
		want   string // JSON; "" means absent
	}{
		{"unset", nil, ""},
		{"auto", &llm.ToolChoice{Mode: llm.ToolChoiceAuto}, `"auto"`},
		{"any is required", &llm.ToolChoice{Mode: llm.ToolChoiceAny}, `"required"`},
		{"none", &llm.ToolChoice{Mode: llm.ToolChoiceNone}, `"none"`},
		{"named, by sent name", &llm.ToolChoice{Mode: llm.ToolChoiceTool, Name: "pane.open"},
			`{"type":"function","function":{"name":"pane_open"}}`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var got json.RawMessage
			srv := fakeServer(t, func(w http.ResponseWriter, r *http.Request) {
				var body map[string]json.RawMessage
				_ = json.NewDecoder(r.Body).Decode(&body)
				got = body["tool_choice"]
				w.Header().Set("Content-Type", "application/json")
				_, _ = w.Write([]byte(completionJSON("done", "assistant", "stop")))
			})
			p := newAdapter(t, "openai", srv.URL, "gpt-4o").(llm.ToolCaller)
			_, err := p.CallWithTools(context.Background(), llm.Request{
				Messages:   []llm.Message{{Role: "user", Content: "Hi"}},
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
	srv := fakeServer(t, func(w http.ResponseWriter, _ *http.Request) {
		sent.Add(1)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(completionJSON("done", "assistant", "stop")))
	})
	p := newAdapter(t, "openai", srv.URL, "gpt-4o")
	req := llm.Request{
		Messages:   []llm.Message{{Role: "user", Content: "Hi"}},
		ToolChoice: &llm.ToolChoice{Mode: llm.ToolChoiceAny},
	}
	_, err := p.(llm.Completer).Complete(context.Background(), req)
	assert.EqualError(t, err, `openai: tool_choice "any" needs tools`)
	_, err = p.(llm.Streamer).Stream(context.Background(), req)
	assert.EqualError(t, err, `openai: tool_choice "any" needs tools`)
	req.ToolChoice = &llm.ToolChoice{Mode: llm.ToolChoiceTool, Name: "nope"}
	_, err = p.(llm.ToolCaller).CallWithTools(context.Background(), req, []llm.ToolDef{{Name: "a"}})
	assert.EqualError(t, err, `openai: tool_choice names unknown tool "nope"`)
	assert.Zero(t, sent.Load())
}
