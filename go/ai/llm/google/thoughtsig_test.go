package google_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"hop.top/kit/go/ai/llm"
)

const skipSig = "skip_thought_signature_validator"

// sigData is the ProviderData the adapter attaches to a signed call.
func sigData(sig string) llm.ProviderData {
	return llm.ProviderData{"google": json.RawMessage(`{"thought_signature":"` + sig + `"}`)}
}

// fcResponse is a model turn of functionCall parts; sigs[i] is the
// thoughtSignature of part i ("" leaves it off).
func fcResponse(names []string, sigs []string) map[string]any {
	ps := make([]map[string]any, len(names))
	for i, n := range names {
		ps[i] = map[string]any{"functionCall": map[string]any{
			"name": n, "args": map[string]any{"n": i},
		}}
		if sigs[i] != "" {
			ps[i]["thoughtSignature"] = sigs[i]
		}
	}
	return map[string]any{"candidates": []map[string]any{{
		"content":      map[string]any{"role": "model", "parts": ps},
		"finishReason": "STOP",
	}}}
}

func callTools(t *testing.T, url string, msgs []llm.Message) (llm.ToolResponse, error) {
	t.Helper()
	tc := mustNew(t, url, "gemini-3.5-flash-lite").(llm.ToolCaller)
	return tc.CallWithTools(context.Background(),
		llm.Request{Messages: msgs},
		[]llm.ToolDef{{Name: "echo"}, {Name: "get_weather"}},
	)
}

func TestThoughtSignature_ParsedIntoProviderData(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(
		func(w http.ResponseWriter, _ *http.Request) {
			writeJSON(w, fcResponse([]string{"get_weather", "get_weather"}, []string{"SIG_A", ""}))
		},
	))
	t.Cleanup(srv.Close)

	resp, err := callTools(t, srv.URL, []llm.Message{{Role: "user", Content: "Paris and London?"}})
	require.NoError(t, err)
	require.Len(t, resp.ToolCalls, 2)
	require.Contains(t, resp.ToolCalls[0].ProviderData, "google")
	assert.JSONEq(t, `{"thought_signature":"SIG_A"}`,
		string(resp.ToolCalls[0].ProviderData["google"]))
	assert.Nil(t, resp.ToolCalls[1].ProviderData,
		"a parallel call without a signature carries no provider data")
}

// TestThoughtSignature_ToolLoop reproduces the live failure: a Gemini 3
// model rejects the second request of a tool loop with 400 when the
// functionCall part of the current turn lacks its thoughtSignature.
func TestThoughtSignature_ToolLoop(t *testing.T) {
	round := 0
	srv := httptest.NewServer(http.HandlerFunc(
		func(w http.ResponseWriter, r *http.Request) {
			round++
			if round == 1 {
				writeJSON(w, fcResponse([]string{"echo"}, []string{"SIG_A"}))
				return
			}
			var body struct {
				Contents []struct {
					Role  string           `json:"role"`
					Parts []map[string]any `json:"parts"`
				} `json:"contents"`
			}
			if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
				http.Error(w, err.Error(), http.StatusInternalServerError)
				return
			}
			for _, c := range body.Contents {
				for _, p := range c.Parts {
					if _, ok := p["functionCall"]; ok && p["thoughtSignature"] != "SIG_A" {
						w.WriteHeader(http.StatusBadRequest)
						writeJSON(w, map[string]any{"error": map[string]any{
							"code": 400, "status": "INVALID_ARGUMENT",
							"message": "Function call is missing a thought_signature in functionCall parts.",
						}})
						return
					}
				}
			}
			writeJSON(w, geminiResponse("done", "STOP", 1, 1))
		},
	))
	t.Cleanup(srv.Close)

	msgs := []llm.Message{{Role: "user", Content: "echo hi"}}
	resp, err := callTools(t, srv.URL, msgs)
	require.NoError(t, err)
	require.Len(t, resp.ToolCalls, 1)

	msgs = append(msgs, llm.Message{Role: "assistant", Content: resp.Content, ToolCalls: resp.ToolCalls})
	msgs = append(msgs, llm.Message{Role: "tool", ToolCallID: resp.ToolCalls[0].ID, Content: "hi"})
	resp, err = callTools(t, srv.URL, msgs)
	require.NoError(t, err, "second round must echo the thought signature")
	assert.Equal(t, "done", resp.Content)
}

func TestThoughtSignature_ParallelEchoOnFirstPartOnly(t *testing.T) {
	got, err := captureContents(t, []llm.Message{
		{Role: "user", Content: "Paris and London?"},
		{Role: "assistant", ToolCalls: []llm.ToolCall{
			{ID: "c1", Name: "get_weather", Arguments: json.RawMessage(`{"city":"Paris"}`), ProviderData: sigData("SIG_A")},
			{ID: "c2", Name: "get_weather", Arguments: json.RawMessage(`{"city":"London"}`)},
		}},
		{Role: "tool", ToolCallID: "c1", Content: "15C"},
		{Role: "tool", ToolCallID: "c2", Content: "12C"},
	})
	require.NoError(t, err)
	_, model := parts(t, got[1])
	require.Len(t, model, 2)
	assert.Equal(t, "SIG_A", model[0]["thoughtSignature"], "signature echoed on its own functionCall part")
	assert.NotContains(t, model[1], "thoughtSignature", "the unsigned parallel call stays unsigned")
}

func TestThoughtSignature_SequentialSteps(t *testing.T) {
	got, err := captureContents(t, []llm.Message{
		{Role: "user", Content: "Check AA100, book a taxi if delayed."},
		{Role: "assistant", ToolCalls: []llm.ToolCall{
			{ID: "c1", Name: "get_weather", ProviderData: sigData("SIG_A")},
		}},
		{Role: "tool", ToolCallID: "c1", Content: "delayed"},
		{Role: "assistant", Content: "Booking.", ToolCalls: []llm.ToolCall{
			{ID: "c2", Name: "get_weather", ProviderData: sigData("SIG_B")},
		}},
		{Role: "tool", ToolCallID: "c2", Content: "booked"},
	})
	require.NoError(t, err)
	require.Len(t, got, 5)
	_, step1 := parts(t, got[1])
	require.Len(t, step1, 1)
	assert.Equal(t, "SIG_A", step1[0]["thoughtSignature"])
	_, step2 := parts(t, got[3])
	require.Len(t, step2, 2)
	assert.NotContains(t, step2[0], "thoughtSignature", "text part stays separate and unsigned")
	assert.Equal(t, "SIG_B", step2[1]["thoughtSignature"])
}

// Hand-built or other-provider history has no signature. In the current
// turn Gemini 3 validates the first functionCall of every step, so the
// adapter sends the documented skip value there; earlier turns are not
// validated and go out exactly as given.
func TestThoughtSignature_HistoryWithoutSignatures(t *testing.T) {
	got, err := captureContents(t, []llm.Message{
		{Role: "user", Content: "old question"},
		{Role: "assistant", ToolCalls: []llm.ToolCall{{ID: "o1", Name: "get_weather"}}},
		{Role: "tool", ToolCallID: "o1", Content: "old"},
		{Role: "assistant", Content: "old answer"},
		{Role: "user", Content: "Paris and London?"},
		{Role: "assistant", ToolCalls: []llm.ToolCall{
			{ID: "c1", Name: "get_weather"},
			{ID: "c2", Name: "get_weather", ProviderData: llm.ProviderData{
				"anthropic": json.RawMessage(`{"thinking":"not for gemini"}`),
			}},
		}},
		{Role: "tool", ToolCallID: "c1", Content: "15C"},
		{Role: "tool", ToolCallID: "c2", Content: "12C"},
	})
	require.NoError(t, err)
	require.Len(t, got, 7)

	_, old := parts(t, got[1])
	require.Len(t, old, 1)
	assert.NotContains(t, old[0], "thoughtSignature", "earlier turns are sent as given")

	_, cur := parts(t, got[5])
	require.Len(t, cur, 2)
	assert.Equal(t, skipSig, cur[0]["thoughtSignature"],
		"first unsigned call of a current-turn step gets the skip value")
	assert.NotContains(t, cur[1], "thoughtSignature")
	assert.NotContains(t, cur[1]["functionCall"], "thinking", "another namespace never reaches Gemini")
}

func TestThoughtSignature_MalformedProviderData(t *testing.T) {
	_, err := captureContents(t, []llm.Message{
		{Role: "user", Content: "go"},
		{Role: "assistant", ToolCalls: []llm.ToolCall{
			{ID: "c1", Name: "get_weather", ProviderData: llm.ProviderData{"google": json.RawMessage(`"SIG_A"`)}},
		}},
		{Role: "tool", ToolCallID: "c1", Content: "x"},
	})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "gemini:")
}
