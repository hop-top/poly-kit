package ollama_test

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"sync"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"hop.top/kit/go/ai/llm"
	"hop.top/kit/go/ai/llm/internal/transporttest"
)

func chatReply(content string) string {
	b, _ := json.Marshal(map[string]any{
		"message": map[string]any{"role": "assistant", "content": content},
		"done":    true, "eval_count": 3, "prompt_eval_count": 2,
	})
	return string(b)
}

func structuredReq() llm.Request {
	return llm.Request{Messages: []llm.Message{{Role: "user", Content: "judge"}}}
}

func TestStructured_WireFormat(t *testing.T) {
	fake := transporttest.NewFake(t, "application/json", chatReply(transporttest.VerdictOK))
	p, err := llm.Resolve("ollama://llama3?base_url=" + fake.URL)
	require.NoError(t, err)
	defer p.Close()

	req := structuredReq()
	req.ResponseFormat = &llm.ResponseFormat{Name: "verdict", Schema: json.RawMessage(transporttest.VerdictSchema)}
	_, err = p.(llm.Completer).Complete(context.Background(), req)
	require.NoError(t, err)

	assert.Equal(t, "/api/chat", fake.Path())
	var body map[string]json.RawMessage
	require.NoError(t, json.Unmarshal(fake.Body(), &body))
	assert.JSONEq(t, transporttest.VerdictSchema, string(body["format"]), "format carries the schema")
	assert.NotContains(t, body, "response_format")

	// Without a format the field is absent.
	_, err = p.(llm.Completer).Complete(context.Background(), structuredReq())
	require.NoError(t, err)
	body = nil
	require.NoError(t, json.Unmarshal(fake.Body(), &body))
	assert.NotContains(t, body, "format")
}

func TestStructured_BadFormatNotSent(t *testing.T) {
	fake := transporttest.NewFake(t, "application/json", chatReply("x"))
	p, err := llm.Resolve("ollama://llama3?base_url=" + fake.URL)
	require.NoError(t, err)
	defer p.Close()
	req := structuredReq()
	req.ResponseFormat = &llm.ResponseFormat{Schema: json.RawMessage(`"nope"`)}
	_, err = p.(llm.Completer).Complete(context.Background(), req)
	require.Error(t, err)
	assert.Nil(t, fake.Body(), "nothing went over the wire")
}

// TestStructured_RepairOverTheWire runs the whole loop through the
// adapter: the second request replays the bad reply and the violation.
func TestStructured_RepairOverTheWire(t *testing.T) {
	var mu sync.Mutex
	var bodies []map[string]json.RawMessage
	replies := []string{transporttest.VerdictBad, transporttest.VerdictOK}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		var m map[string]json.RawMessage
		_ = json.Unmarshal(b, &m)
		mu.Lock()
		bodies = append(bodies, m)
		i := len(bodies) - 1
		mu.Unlock()
		_, _ = io.WriteString(w, chatReply(replies[i]))
	}))
	defer srv.Close()
	p, err := llm.Resolve("ollama://llama3?base_url=" + srv.URL)
	require.NoError(t, err)
	defer p.Close()

	res, err := llm.CompleteStructured(context.Background(), p.(llm.Completer), structuredReq(),
		llm.ResponseFormat{Schema: json.RawMessage(transporttest.VerdictSchema)})
	require.NoError(t, err)
	assert.Equal(t, 2, res.Attempts)
	assert.JSONEq(t, transporttest.VerdictOK, string(res.Data))
	assert.Equal(t, 10, res.Usage.TotalTokens, "two attempts of 5")

	require.Len(t, bodies, 2)
	var msgs []llm.Message
	var raw []struct{ Role, Content string }
	require.NoError(t, json.Unmarshal(bodies[1]["messages"], &raw))
	for _, m := range raw {
		msgs = append(msgs, llm.Message{Role: m.Role, Content: m.Content})
	}
	require.Len(t, msgs, 3)
	assert.Equal(t, llm.Message{Role: "assistant", Content: transporttest.VerdictBad}, msgs[1])
	assert.Contains(t, msgs[2].Content, "score")
	assert.JSONEq(t, transporttest.VerdictSchema, string(bodies[1]["format"]), "the repair keeps the format")
}

// TestStructured_XRRReplay replays a cassette recorded against a
// loopback fake. Re-record with OLLAMA_STRUCTURED_XRR_RECORD=1.
func TestStructured_XRRReplay(t *testing.T) {
	var res llm.StructuredResult
	transporttest.Cassette{
		Dir:         filepath.Join("testdata", "xrr-structured"),
		Env:         "OLLAMA_STRUCTURED_XRR_RECORD",
		ContentType: "application/json",
		Reply:       chatReply(transporttest.VerdictOK),
		URI:         func(base string) string { return "ollama://llama3?base_url=" + base },
	}.Run(t, func(p llm.Provider) error {
		var err error
		res, err = llm.CompleteStructured(context.Background(), p.(llm.Completer), structuredReq(),
			llm.ResponseFormat{Name: "verdict", Schema: json.RawMessage(transporttest.VerdictSchema)})
		return err
	})
	assert.JSONEq(t, transporttest.VerdictOK, string(res.Data))
	assert.Equal(t, 5, res.Usage.TotalTokens)
}
