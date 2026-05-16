package ash

import (
	"context"
	"encoding/json"
	"fmt"
	"sync"
)

// Provider is the interface for LLM backends.
type Provider interface {
	// Complete sends turns and returns a single assistant turn.
	Complete(ctx context.Context, turns []Turn) (Turn, error)

	// Stream sends turns and returns a streaming response.
	Stream(ctx context.Context, turns []Turn) (TurnStream, error)

	// CallWithTools sends turns with tool definitions, allowing the
	// provider to request tool invocations in its response.
	CallWithTools(ctx context.Context, turns []Turn, tools []ToolDef) (Turn, error)
}

// TurnStream yields tokens from a streaming provider response.
type TurnStream interface {
	// Next returns the next token. Returns a Token with Done=true
	// at end of stream. Returns error on failure.
	Next() (Token, error)

	// Close releases stream resources.
	Close() error
}

// Token is a single piece of streamed text.
type Token struct {
	Text string `json:"text"`
	Done bool   `json:"done"`
}

// ToolDef describes a tool available to the provider.
type ToolDef struct {
	Name        string          `json:"name"`
	Description string          `json:"description"`
	InputSchema json.RawMessage `json:"input_schema"`
}

// ToolHandler processes a single tool invocation.
type ToolHandler interface {
	Handle(ctx context.Context, name string, input json.RawMessage) (json.RawMessage, error)
}

// ToolHandlerFunc adapts a plain function to the ToolHandler interface.
type ToolHandlerFunc func(ctx context.Context, name string, input json.RawMessage) (json.RawMessage, error)

// Handle implements ToolHandler.
func (f ToolHandlerFunc) Handle(ctx context.Context, name string, input json.RawMessage) (json.RawMessage, error) {
	return f(ctx, name, input)
}

// ErrUnknownTool is returned when a tool name is not registered.
var ErrUnknownTool = fmt.Errorf("ash: unknown tool")

// ToolRegistry dispatches tool calls to registered handlers.
type ToolRegistry struct {
	mu       sync.RWMutex
	handlers map[string]ToolHandler
}

// NewToolRegistry creates an empty ToolRegistry.
func NewToolRegistry() *ToolRegistry {
	return &ToolRegistry{handlers: make(map[string]ToolHandler)}
}

// Register adds a handler for the given tool name.
func (r *ToolRegistry) Register(name string, h ToolHandler) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.handlers[name] = h
}

// Handle dispatches to the registered handler for name.
func (r *ToolRegistry) Handle(ctx context.Context, name string, input json.RawMessage) (json.RawMessage, error) {
	r.mu.RLock()
	h, ok := r.handlers[name]
	r.mu.RUnlock()
	if !ok {
		return nil, fmt.Errorf("%w: %s", ErrUnknownTool, name)
	}
	return h.Handle(ctx, name, input)
}
