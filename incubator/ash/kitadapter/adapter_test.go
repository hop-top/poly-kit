package kitadapter_test

import (
	"context"
	"encoding/json"
	"testing"

	"hop.top/ash"
	"hop.top/ash/kitadapter"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestAsProvider_Complete(t *testing.T) {
	mock := &mockClient{
		chatResp: kitadapter.Response{
			Message: kitadapter.Message{
				Role:    "assistant",
				Content: "hello back",
			},
			Usage: map[string]int{"input_tokens": 10, "output_tokens": 5},
		},
	}

	prov := kitadapter.AsProvider(mock)

	turn, err := prov.Complete(context.Background(), []ash.Turn{
		{Role: ash.RoleUser, Content: "hello"},
	})
	require.NoError(t, err)

	assert.Equal(t, ash.RoleAssistant, turn.Role)
	assert.Equal(t, "hello back", turn.Content)
	assert.NotNil(t, turn.Metadata["usage"])

	// Verify messages were mapped correctly.
	require.Len(t, mock.lastMsgs, 1)
	assert.Equal(t, "user", mock.lastMsgs[0].Role)
	assert.Equal(t, "hello", mock.lastMsgs[0].Content)
}

func TestAsProvider_Stream(t *testing.T) {
	mock := &mockClient{
		streamTokens: []string{"hello", " ", "world"},
	}

	prov := kitadapter.AsProvider(mock)

	stream, err := prov.Stream(context.Background(), []ash.Turn{
		{Role: ash.RoleUser, Content: "hi"},
	})
	require.NoError(t, err)

	var collected string
	for {
		tok, err := stream.Next()
		require.NoError(t, err)
		if tok.Done {
			break
		}
		collected += tok.Text
	}

	assert.Equal(t, "hello world", collected)
	require.NoError(t, stream.Close())
}

func TestAsProvider_CallWithTools(t *testing.T) {
	mock := &mockClient{
		chatResp: kitadapter.Response{
			Message: kitadapter.Message{
				Role:    "assistant",
				Content: "used tools",
			},
		},
	}

	prov := kitadapter.AsProvider(mock)

	tools := []ash.ToolDef{
		{
			Name:        "read_file",
			Description: "reads a file",
			InputSchema: json.RawMessage(`{"type":"object"}`),
		},
	}

	turn, err := prov.CallWithTools(
		context.Background(),
		[]ash.Turn{{Role: ash.RoleUser, Content: "read foo"}},
		tools,
	)
	require.NoError(t, err)

	assert.Equal(t, "used tools", turn.Content)
	require.Len(t, mock.lastTools, 1)
	assert.Equal(t, "read_file", mock.lastTools[0].Name)
}

// --- mock types ---

type mockClient struct {
	chatResp     kitadapter.Response
	streamTokens []string
	lastMsgs     []kitadapter.Message
	lastTools    []kitadapter.ToolDef
}

func (m *mockClient) Chat(
	_ context.Context, msgs []kitadapter.Message,
) (kitadapter.Response, error) {
	m.lastMsgs = msgs
	return m.chatResp, nil
}

func (m *mockClient) StreamChat(
	_ context.Context, msgs []kitadapter.Message,
) (kitadapter.TokenIterator, error) {
	m.lastMsgs = msgs
	return &mockIterator{tokens: m.streamTokens}, nil
}

func (m *mockClient) ChatWith(
	_ context.Context, msgs []kitadapter.Message, tools []kitadapter.ToolDef,
) (kitadapter.Response, error) {
	m.lastMsgs = msgs
	m.lastTools = tools
	return m.chatResp, nil
}

type mockIterator struct {
	tokens []string
	idx    int
}

func (it *mockIterator) Next() (string, bool, error) {
	if it.idx >= len(it.tokens) {
		return "", true, nil
	}
	tok := it.tokens[it.idx]
	it.idx++
	return tok, false, nil
}

func (it *mockIterator) Close() error {
	return nil
}

// Ensure Adapter satisfies ash.Provider at compile time.
var _ ash.Provider = kitadapter.AsProvider(&mockClient{})
