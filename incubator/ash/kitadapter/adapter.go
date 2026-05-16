// Package kitadapter documents the mapping between hop.top/kit/llm
// types and hop.top/ash types. The real implementation lives in kit
// (which imports ash), since ash cannot import kit.
//
// # Mapping
//
//	AsProvider(llm.Client) ash.Provider
//	  - llm.Client.Chat      → ash.Provider.Complete
//	  - llm.Client.StreamChat → ash.Provider.Stream
//	  - llm.Client.ChatWith   → ash.Provider.CallWithTools
//
//	llm.Message  ↔ ash.Turn
//	  - llm.Message.Role     ↔ ash.Turn.Role
//	  - llm.Message.Content  ↔ ash.Turn.Content
//	  - llm.Message.Parts    ↔ ash.Turn.Parts  (not yet mapped; text-only)
//
//	llm.ToolDef  ↔ ash.ToolDef
//	  - llm.ToolDef.Name        ↔ ash.ToolDef.Name
//	  - llm.ToolDef.Description ↔ ash.ToolDef.Description
//	  - llm.ToolDef.InputSchema ↔ ash.ToolDef.InputSchema
//
//	llm.Response → ash.Turn
//	  - llm.Response.Message → ash.Turn (role=assistant)
//	  - llm.Response.Usage   → ash.Turn.Metadata["usage"]
//
//	llm.TokenIterator → ash.TurnStream
//	  - llm.TokenIterator.Next → ash.TurnStream.Next
//	  - maps llm.Token → ash.Token
package kitadapter

import (
	"context"
	"encoding/json"

	"hop.top/ash"
)

// LLMClient is the subset of llm.Client that the adapter needs.
// This mirrors the kit llm.Client interface for documentation and
// local testing without importing kit.
type LLMClient interface {
	Chat(ctx context.Context, msgs []Message) (Response, error)
	StreamChat(ctx context.Context, msgs []Message) (TokenIterator, error)
	ChatWith(ctx context.Context, msgs []Message, tools []ToolDef) (Response, error)
}

// Message mirrors llm.Message for local testing.
type Message struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

// Response mirrors llm.Response for local testing.
type Response struct {
	Message Message        `json:"message"`
	Usage   map[string]int `json:"usage,omitempty"`
}

// ToolDef mirrors llm.ToolDef for local testing.
type ToolDef struct {
	Name        string          `json:"name"`
	Description string          `json:"description"`
	InputSchema json.RawMessage `json:"input_schema"`
}

// TokenIterator mirrors llm.TokenIterator for local testing.
type TokenIterator interface {
	Next() (string, bool, error)
	Close() error
}

// Adapter wraps an LLMClient to implement ash.Provider.
type Adapter struct {
	client LLMClient
}

// AsProvider wraps an LLMClient as an ash.Provider.
func AsProvider(c LLMClient) ash.Provider {
	return &Adapter{client: c}
}

// Complete implements ash.Provider.
func (a *Adapter) Complete(
	ctx context.Context, turns []ash.Turn,
) (ash.Turn, error) {
	msgs := turnsToMessages(turns)
	resp, err := a.client.Chat(ctx, msgs)
	if err != nil {
		return ash.Turn{}, err
	}
	return responseToTurn(resp), nil
}

// Stream implements ash.Provider.
func (a *Adapter) Stream(
	ctx context.Context, turns []ash.Turn,
) (ash.TurnStream, error) {
	msgs := turnsToMessages(turns)
	iter, err := a.client.StreamChat(ctx, msgs)
	if err != nil {
		return nil, err
	}
	return &streamAdapter{iter: iter}, nil
}

// CallWithTools implements ash.Provider.
func (a *Adapter) CallWithTools(
	ctx context.Context, turns []ash.Turn, tools []ash.ToolDef,
) (ash.Turn, error) {
	msgs := turnsToMessages(turns)
	llmTools := ashToolsToLLM(tools)
	resp, err := a.client.ChatWith(ctx, msgs, llmTools)
	if err != nil {
		return ash.Turn{}, err
	}
	return responseToTurn(resp), nil
}

// --- mapping helpers ---

func turnsToMessages(turns []ash.Turn) []Message {
	msgs := make([]Message, len(turns))
	for i, t := range turns {
		// Parts are not mapped; LLMClient.Message carries text-only content.
		msgs[i] = Message{
			Role:    string(t.Role),
			Content: t.Content,
		}
	}
	return msgs
}

func responseToTurn(resp Response) ash.Turn {
	t := ash.Turn{
		Role:    ash.Role(resp.Message.Role),
		Content: resp.Message.Content,
	}
	if resp.Usage != nil {
		t.Metadata = map[string]any{"usage": resp.Usage}
	}
	return t
}

func ashToolsToLLM(tools []ash.ToolDef) []ToolDef {
	out := make([]ToolDef, len(tools))
	for i, t := range tools {
		out[i] = ToolDef{
			Name:        t.Name,
			Description: t.Description,
			InputSchema: t.InputSchema,
		}
	}
	return out
}

// streamAdapter wraps TokenIterator as ash.TurnStream.
type streamAdapter struct {
	iter TokenIterator
}

func (s *streamAdapter) Next() (ash.Token, error) {
	text, done, err := s.iter.Next()
	if err != nil {
		return ash.Token{}, err
	}
	return ash.Token{Text: text, Done: done}, nil
}

func (s *streamAdapter) Close() error {
	return s.iter.Close()
}
