package anthropic_test

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"hop.top/kit/go/ai/llm"
)

// A canceled stream ends with an error after the events so far; the
// open call gets no end and there is no outcome.
func TestStreamEventsCanceledMidToolCall(t *testing.T) {
	clearAnthropicEnv(t)
	release := make(chan struct{})
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = io.WriteString(w, "event: message_start\ndata: {\"type\":\"message_start\",\"message\":{\"id\":\"m\",\"type\":\"message\",\"role\":\"assistant\",\"content\":[],\"model\":\"m\",\"usage\":{\"input_tokens\":2,\"output_tokens\":0}}}\n\n"+
			"event: content_block_start\ndata: {\"type\":\"content_block_start\",\"index\":0,\"content_block\":{\"type\":\"tool_use\",\"id\":\"toolu_1\",\"name\":\"get_weather\",\"input\":{}}}\n\n")
		w.(http.Flusher).Flush()
		select {
		case <-r.Context().Done():
		case <-release:
		}
	}))
	t.Cleanup(func() { close(release); srv.Close() })

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	p := newConfAdapter(t, srv.URL, "m", "k")
	st, err := p.(llm.ToolStreamer).StreamWithTools(ctx, llm.Request{
		Messages:     []llm.Message{{Role: "user", Content: "Hi"}},
		StreamEvents: true,
	}, []llm.ToolDef{{Name: "get_weather"}})
	require.NoError(t, err)
	defer st.Close()

	tok, err := st.Next()
	require.NoError(t, err)
	require.NotNil(t, tok.Usage)
	tok, err = st.Next()
	require.NoError(t, err)
	require.NotNil(t, tok.ToolCall)
	assert.Equal(t, llm.ToolCallStart, tok.ToolCall.Kind)

	cancel()
	_, err = st.Next()
	require.Error(t, err)
	assert.True(t, errors.Is(err, context.Canceled), "got %v", err)
	_, ok := st.Outcome()
	assert.False(t, ok, "no outcome after cancellation")
	_, err = st.Next()
	assert.ErrorIs(t, err, io.EOF)
}
