package ash

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
)

func TestToolRegistry_DispatchesToCorrectHandler(t *testing.T) {
	reg := NewToolRegistry()

	called := ""
	reg.Register("read", ToolHandlerFunc(
		func(_ context.Context, name string, input json.RawMessage) (json.RawMessage, error) {
			called = name
			return json.RawMessage(`{"ok":true}`), nil
		},
	))
	reg.Register("write", ToolHandlerFunc(
		func(_ context.Context, name string, _ json.RawMessage) (json.RawMessage, error) {
			called = name
			return json.RawMessage(`{"wrote":true}`), nil
		},
	))

	ctx := context.Background()

	out, err := reg.Handle(ctx, "read", json.RawMessage(`{}`))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if called != "read" {
		t.Fatalf("expected handler 'read' called, got %q", called)
	}
	if string(out) != `{"ok":true}` {
		t.Fatalf("unexpected output: %s", out)
	}

	out, err = reg.Handle(ctx, "write", json.RawMessage(`{}`))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if called != "write" {
		t.Fatalf("expected handler 'write' called, got %q", called)
	}
	if string(out) != `{"wrote":true}` {
		t.Fatalf("unexpected output: %s", out)
	}
}

func TestToolRegistry_UnknownToolError(t *testing.T) {
	reg := NewToolRegistry()
	ctx := context.Background()

	_, err := reg.Handle(ctx, "missing", json.RawMessage(`{}`))
	if err == nil {
		t.Fatal("expected error for unknown tool")
	}
	if !errors.Is(err, ErrUnknownTool) {
		t.Fatalf("expected ErrUnknownTool, got: %v", err)
	}
}

func TestToolHandlerFunc_ImplementsToolHandler(t *testing.T) {
	_ = ToolHandlerFunc(
		func(_ context.Context, _ string, _ json.RawMessage) (json.RawMessage, error) {
			return nil, nil
		},
	)
}
