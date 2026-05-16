package ash

import (
	"context"
	"encoding/json"
	"errors"
	"sync"
	"testing"
)

// --- mock provider ---

type mockProvider struct {
	mu        sync.Mutex
	calls     int
	responses []Turn
}

func newMockProvider(responses ...Turn) *mockProvider {
	return &mockProvider{responses: responses}
}

func (m *mockProvider) Complete(
	_ context.Context, _ []Turn,
) (Turn, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	if m.calls >= len(m.responses) {
		return Turn{
			Role:    RoleAssistant,
			Content: "fallback",
		}, nil
	}
	resp := m.responses[m.calls]
	m.calls++
	return resp, nil
}

func (m *mockProvider) Stream(
	_ context.Context, turns []Turn,
) (TurnStream, error) {
	resp, err := m.Complete(context.Background(), turns)
	if err != nil {
		return nil, err
	}
	return &mockStream{tokens: splitTokens(resp.Content)}, nil
}

func (m *mockProvider) CallWithTools(
	ctx context.Context, turns []Turn, _ []ToolDef,
) (Turn, error) {
	return m.Complete(ctx, turns)
}

// --- mock stream ---

type mockStream struct {
	tokens []Token
	idx    int
}

func splitTokens(content string) []Token {
	if content == "" {
		return []Token{{Done: true}}
	}
	var tokens []Token
	for _, ch := range content {
		tokens = append(tokens, Token{Text: string(ch)})
	}
	tokens = append(tokens, Token{Done: true})
	return tokens
}

func (s *mockStream) Next() (Token, error) {
	if s.idx >= len(s.tokens) {
		return Token{Done: true}, nil
	}
	tok := s.tokens[s.idx]
	s.idx++
	return tok, nil
}

func (s *mockStream) Close() error { return nil }

// --- mock publisher ---

type mockPublisher struct {
	mu     sync.Mutex
	events []pubEvent
}

type pubEvent struct {
	Topic   string
	Payload any
}

func (p *mockPublisher) Publish(
	_ context.Context, topic string, payload any,
) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.events = append(p.events, pubEvent{Topic: topic, Payload: payload})
	return nil
}

func (p *mockPublisher) topics() []string {
	p.mu.Lock()
	defer p.mu.Unlock()
	var out []string
	for _, e := range p.events {
		out = append(out, e.Topic)
	}
	return out
}

// --- tests ---

func TestSend_AppendsUserAndAssistantTurns(t *testing.T) {
	sess := &Session{ID: "s-1"}
	prov := newMockProvider(Turn{
		Role:    RoleAssistant,
		Content: "hi there",
	})

	rt := NewRuntime(sess, RuntimeWithProvider(prov))
	ctx := context.Background()

	got, err := rt.Send(ctx, "hello")
	if err != nil {
		t.Fatalf("Send: %v", err)
	}

	if got.Role != RoleAssistant {
		t.Fatalf("expected assistant role, got %s", got.Role)
	}
	if got.Content != "hi there" {
		t.Fatalf("expected 'hi there', got %q", got.Content)
	}

	turns := rt.Session().Turns
	if len(turns) != 2 {
		t.Fatalf("expected 2 turns, got %d", len(turns))
	}
	if turns[0].Role != RoleUser {
		t.Fatalf("first turn should be user, got %s", turns[0].Role)
	}
	if turns[1].Role != RoleAssistant {
		t.Fatalf("second turn should be assistant, got %s", turns[1].Role)
	}
}

func TestSend_NoToolsSingleRoundTrip(t *testing.T) {
	sess := &Session{ID: "s-1"}
	prov := newMockProvider(Turn{
		Role:    RoleAssistant,
		Content: "done",
	})

	rt := NewRuntime(sess, RuntimeWithProvider(prov))
	_, err := rt.Send(context.Background(), "go")
	if err != nil {
		t.Fatalf("Send: %v", err)
	}

	prov.mu.Lock()
	calls := prov.calls
	prov.mu.Unlock()
	if calls != 1 {
		t.Fatalf("expected 1 provider call, got %d", calls)
	}
}

func TestSend_ToolCallInvokesHandler(t *testing.T) {
	// First response: tool call. Second: text.
	prov := newMockProvider(
		Turn{
			Role: RoleAssistant,
			ToolCalls: []ToolCall{{
				ID:    "tc-1",
				Name:  "echo",
				Input: json.RawMessage(`{"msg":"ping"}`),
			}},
		},
		Turn{
			Role:    RoleAssistant,
			Content: "pong received",
		},
	)

	reg := NewToolRegistry()
	handlerCalled := false
	reg.Register("echo", ToolHandlerFunc(
		func(_ context.Context, _ string, input json.RawMessage) (json.RawMessage, error) {
			handlerCalled = true
			return json.RawMessage(`{"reply":"pong"}`), nil
		},
	))

	sess := &Session{ID: "s-1"}
	rt := NewRuntime(sess,
		RuntimeWithProvider(prov),
		RuntimeWithToolRegistry(reg),
	)

	got, err := rt.Send(context.Background(), "do echo")
	if err != nil {
		t.Fatalf("Send: %v", err)
	}

	if !handlerCalled {
		t.Fatal("expected tool handler to be called")
	}
	if got.Content != "pong received" {
		t.Fatalf("expected 'pong received', got %q", got.Content)
	}

	// Should have: user, tool-result, assistant = 3 turns
	// But also the intermediate assistant tool-call is not appended
	// because only the tool turn and the final assistant are.
	turns := rt.Session().Turns
	if len(turns) < 3 {
		t.Fatalf("expected at least 3 turns, got %d", len(turns))
	}

	// Verify tool turn exists.
	found := false
	for _, turn := range turns {
		if turn.Role == RoleTool {
			found = true
			break
		}
	}
	if !found {
		t.Fatal("expected a tool turn in session")
	}
}

func TestSend_MaxToolDepthRespected(t *testing.T) {
	// Provider always returns tool calls — should hit max depth.
	prov := &infiniteToolProvider{}

	reg := NewToolRegistry()
	reg.Register("loop", ToolHandlerFunc(
		func(_ context.Context, _ string, _ json.RawMessage) (json.RawMessage, error) {
			return json.RawMessage(`{}`), nil
		},
	))

	sess := &Session{ID: "s-1"}
	rt := NewRuntime(sess,
		RuntimeWithProvider(prov),
		RuntimeWithToolRegistry(reg),
		RuntimeWithMaxToolDepth(3),
	)

	_, err := rt.Send(context.Background(), "go")
	if !errors.Is(err, ErrMaxToolDepth) {
		t.Fatalf("expected ErrMaxToolDepth, got: %v", err)
	}
}

type infiniteToolProvider struct{}

func (p *infiniteToolProvider) Complete(
	_ context.Context, _ []Turn,
) (Turn, error) {
	return Turn{
		Role: RoleAssistant,
		ToolCalls: []ToolCall{{
			ID:    "tc-inf",
			Name:  "loop",
			Input: json.RawMessage(`{}`),
		}},
	}, nil
}

func (p *infiniteToolProvider) Stream(
	_ context.Context, _ []Turn,
) (TurnStream, error) {
	return nil, errors.New("not implemented")
}

func (p *infiniteToolProvider) CallWithTools(
	ctx context.Context, turns []Turn, _ []ToolDef,
) (Turn, error) {
	return p.Complete(ctx, turns)
}

func TestSend_PersistsToStore(t *testing.T) {
	sess := &Session{ID: "s-1"}
	store := NewMemoryStore()
	ctx := context.Background()

	// Pre-create session in store.
	if err := store.Create(ctx, SessionMeta{ID: "s-1"}); err != nil {
		t.Fatalf("Create: %v", err)
	}

	prov := newMockProvider(Turn{
		Role:    RoleAssistant,
		Content: "stored",
	})

	rt := NewRuntime(sess,
		RuntimeWithProvider(prov),
		RuntimeWithStore(store),
	)

	_, err := rt.Send(ctx, "save this")
	if err != nil {
		t.Fatalf("Send: %v", err)
	}

	loaded, err := store.Load(ctx, "s-1")
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if len(loaded.Turns) < 2 {
		t.Fatalf("expected at least 2 persisted turns, got %d", len(loaded.Turns))
	}
}

func TestSend_PublishesEvents(t *testing.T) {
	sess := &Session{ID: "s-1"}
	pub := &mockPublisher{}
	prov := newMockProvider(Turn{
		Role:    RoleAssistant,
		Content: "ok",
	})

	rt := NewRuntime(sess,
		RuntimeWithProvider(prov),
		RuntimeWithPublisher(pub),
	)

	_, err := rt.Send(context.Background(), "hello")
	if err != nil {
		t.Fatalf("Send: %v", err)
	}

	topics := pub.topics()
	if len(topics) < 2 {
		t.Fatalf("expected at least 2 events, got %d: %v", len(topics), topics)
	}

	// Should have turn.start and turn.done at minimum.
	hasTurnStart := false
	hasTurnDone := false
	for _, topic := range topics {
		switch topic {
		case TopicTurnStart:
			hasTurnStart = true
		case TopicTurnDone:
			hasTurnDone = true
		}
	}
	if !hasTurnStart {
		t.Fatal("expected session.turn.start event")
	}
	if !hasTurnDone {
		t.Fatal("expected session.turn.done event")
	}
}

func TestSend_NilPublisherNoPanic(t *testing.T) {
	sess := &Session{ID: "s-1"}
	prov := newMockProvider(Turn{
		Role:    RoleAssistant,
		Content: "ok",
	})

	rt := NewRuntime(sess, RuntimeWithProvider(prov))

	// No publisher set — should not panic.
	_, err := rt.Send(context.Background(), "hello")
	if err != nil {
		t.Fatalf("Send: %v", err)
	}
}

func TestSend_NoProviderError(t *testing.T) {
	sess := &Session{ID: "s-1"}
	rt := NewRuntime(sess) // no provider

	_, err := rt.Send(context.Background(), "hello")
	if !errors.Is(err, ErrNoProvider) {
		t.Fatalf("expected ErrNoProvider, got: %v", err)
	}
}

func TestSend_ToolCallPublishesToolEvents(t *testing.T) {
	pub := &mockPublisher{}
	prov := newMockProvider(
		Turn{
			Role: RoleAssistant,
			ToolCalls: []ToolCall{{
				ID:    "tc-1",
				Name:  "ping",
				Input: json.RawMessage(`{}`),
			}},
		},
		Turn{Role: RoleAssistant, Content: "done"},
	)

	reg := NewToolRegistry()
	reg.Register("ping", ToolHandlerFunc(
		func(_ context.Context, _ string, _ json.RawMessage) (json.RawMessage, error) {
			return json.RawMessage(`{}`), nil
		},
	))

	sess := &Session{ID: "s-1"}
	rt := NewRuntime(sess,
		RuntimeWithProvider(prov),
		RuntimeWithPublisher(pub),
		RuntimeWithToolRegistry(reg),
	)

	_, err := rt.Send(context.Background(), "go")
	if err != nil {
		t.Fatalf("Send: %v", err)
	}

	topics := pub.topics()
	hasToolCalled := false
	hasToolDone := false
	for _, topic := range topics {
		switch topic {
		case TopicToolCalled:
			hasToolCalled = true
		case TopicToolDone:
			hasToolDone = true
		}
	}
	if !hasToolCalled {
		t.Fatal("expected session.tool.called event")
	}
	if !hasToolDone {
		t.Fatal("expected session.tool.done event")
	}
}

// --- StreamSend tests ---

func TestStreamSend_ReturnsTokens(t *testing.T) {
	sess := &Session{ID: "s-1"}
	prov := newMockProvider(Turn{
		Role:    RoleAssistant,
		Content: "abc",
	})

	rt := NewRuntime(sess, RuntimeWithProvider(prov))

	stream, err := rt.StreamSend(context.Background(), "go")
	if err != nil {
		t.Fatalf("StreamSend: %v", err)
	}

	var text string
	for {
		tok, err := stream.Next()
		if err != nil {
			t.Fatalf("Next: %v", err)
		}
		text += tok.Text
		if tok.Done {
			break
		}
	}

	if err := stream.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}

	if text != "abc" {
		t.Fatalf("expected 'abc', got %q", text)
	}
}

func TestStreamSend_FinalizesAssistantTurn(t *testing.T) {
	sess := &Session{ID: "s-1"}
	prov := newMockProvider(Turn{
		Role:    RoleAssistant,
		Content: "hello",
	})

	rt := NewRuntime(sess, RuntimeWithProvider(prov))

	stream, err := rt.StreamSend(context.Background(), "go")
	if err != nil {
		t.Fatalf("StreamSend: %v", err)
	}

	// Drain.
	for {
		tok, err := stream.Next()
		if err != nil {
			t.Fatalf("Next: %v", err)
		}
		if tok.Done {
			break
		}
	}
	_ = stream.Close()

	turns := rt.Session().Turns
	if len(turns) < 2 {
		t.Fatalf("expected at least 2 turns, got %d", len(turns))
	}

	last := turns[len(turns)-1]
	if last.Role != RoleAssistant {
		t.Fatalf("expected last turn to be assistant, got %s", last.Role)
	}
	if last.Content != "hello" {
		t.Fatalf("expected 'hello', got %q", last.Content)
	}
}

func TestStreamSend_NoProviderError(t *testing.T) {
	sess := &Session{ID: "s-1"}
	rt := NewRuntime(sess)

	_, err := rt.StreamSend(context.Background(), "hello")
	if !errors.Is(err, ErrNoProvider) {
		t.Fatalf("expected ErrNoProvider, got: %v", err)
	}
}

func TestStreamSend_PublishesEvents(t *testing.T) {
	pub := &mockPublisher{}
	sess := &Session{ID: "s-1"}
	prov := newMockProvider(Turn{
		Role:    RoleAssistant,
		Content: "ok",
	})

	rt := NewRuntime(sess,
		RuntimeWithProvider(prov),
		RuntimeWithPublisher(pub),
	)

	stream, err := rt.StreamSend(context.Background(), "go")
	if err != nil {
		t.Fatalf("StreamSend: %v", err)
	}

	for {
		tok, err := stream.Next()
		if err != nil {
			t.Fatalf("Next: %v", err)
		}
		if tok.Done {
			break
		}
	}
	_ = stream.Close()

	topics := pub.topics()
	hasTurnStart := false
	hasTurnDone := false
	for _, topic := range topics {
		switch topic {
		case TopicTurnStart:
			hasTurnStart = true
		case TopicTurnDone:
			hasTurnDone = true
		}
	}
	if !hasTurnStart {
		t.Fatal("expected session.turn.start event")
	}
	if !hasTurnDone {
		t.Fatal("expected session.turn.done event")
	}
}
