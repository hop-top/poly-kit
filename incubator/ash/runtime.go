package ash

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"time"
)

// ErrMaxToolDepth is returned when tool-call recursion exceeds the
// configured limit.
var ErrMaxToolDepth = errors.New("ash: max tool depth exceeded")

// ErrNoProvider is returned when Send/StreamSend is called without a
// configured Provider.
var ErrNoProvider = errors.New("ash: no provider configured")

// Runtime orchestrates conversation turns through a Provider.
// It wraps a Session (data) and adds tool dispatch, persistence, and
// event publication.
type Runtime struct {
	mu           sync.Mutex
	session      *Session
	provider     Provider
	store        Store
	publisher    Publisher
	toolRegistry *ToolRegistry
	maxToolDepth int
}

// RuntimeOption configures a Runtime.
type RuntimeOption func(*Runtime)

// NewRuntime creates a Runtime wrapping the given Session.
func NewRuntime(session *Session, opts ...RuntimeOption) *Runtime {
	r := &Runtime{
		session:      session,
		maxToolDepth: 10,
	}
	for _, o := range opts {
		o(r)
	}
	return r
}

// RuntimeWithProvider sets the LLM provider on a Runtime.
func RuntimeWithProvider(p Provider) RuntimeOption {
	return func(r *Runtime) { r.provider = p }
}

// RuntimeWithStore sets the persistence backend on a Runtime.
func RuntimeWithStore(s Store) RuntimeOption {
	return func(r *Runtime) { r.store = s }
}

// RuntimeWithPublisher sets the event publisher on a Runtime.
func RuntimeWithPublisher(p Publisher) RuntimeOption {
	return func(r *Runtime) { r.publisher = p }
}

// RuntimeWithToolRegistry sets the tool registry for dispatch.
func RuntimeWithToolRegistry(reg *ToolRegistry) RuntimeOption {
	return func(r *Runtime) { r.toolRegistry = reg }
}

// RuntimeWithMaxToolDepth caps recursive tool-call loops (default 10).
func RuntimeWithMaxToolDepth(n int) RuntimeOption {
	return func(r *Runtime) {
		if n > 0 {
			r.maxToolDepth = n
		}
	}
}

// Session returns the underlying data session.
func (rt *Runtime) Session() *Session { return rt.session }

// Send appends a user message, calls Provider.Complete, handles any
// tool calls, and returns the final assistant Turn.
func (rt *Runtime) Send(ctx context.Context, content string) (Turn, error) {
	if rt.provider == nil {
		return Turn{}, ErrNoProvider
	}

	rt.mu.Lock()
	defer rt.mu.Unlock()

	// 1. Append user turn.
	userTurn := Turn{
		ID:        fmt.Sprintf("u-%d", len(rt.session.Turns)),
		Role:      RoleUser,
		Content:   content,
		Timestamp: time.Now().UTC(),
	}
	rt.appendTurn(userTurn)

	rt.emitEvent(ctx, TopicTurnStart, userTurn)

	// 2. Call provider in a tool-call loop.
	assistantTurn, err := rt.completeWithToolLoop(ctx)
	if err != nil {
		return Turn{}, err
	}

	// 3. Append final assistant turn.
	assistantTurn.ID = fmt.Sprintf("a-%d", len(rt.session.Turns))
	assistantTurn.Timestamp = time.Now().UTC()
	rt.appendTurn(assistantTurn)

	rt.emitEvent(ctx, TopicTurnDone, assistantTurn)

	// 4. Persist last turns.
	if err := rt.persistTurn(ctx, userTurn); err != nil {
		return Turn{}, fmt.Errorf("ash: persist user turn: %w", err)
	}
	if err := rt.persistTurn(ctx, assistantTurn); err != nil {
		return Turn{}, fmt.Errorf("ash: persist assistant turn: %w", err)
	}

	return assistantTurn, nil
}

// completeWithToolLoop calls Provider.Complete and dispatches tool
// calls until the provider returns a text response or max depth.
func (rt *Runtime) completeWithToolLoop(ctx context.Context) (Turn, error) {
	for range rt.maxToolDepth {
		resp, err := rt.provider.Complete(ctx, rt.session.Turns)
		if err != nil {
			return Turn{}, fmt.Errorf("ash: provider.Complete: %w", err)
		}

		// No tool calls or no registry — return as-is.
		if len(resp.ToolCalls) == 0 || rt.toolRegistry == nil {
			return resp, nil
		}

		// Dispatch tool calls.
		for _, tc := range resp.ToolCalls {
			rt.emitEvent(ctx, TopicToolCalled, tc)

			output, handleErr := rt.toolRegistry.Handle(
				ctx, tc.Name, tc.Input,
			)

			toolTurn := Turn{
				ID:        fmt.Sprintf("tool-%d", len(rt.session.Turns)),
				Role:      RoleTool,
				Timestamp: time.Now().UTC(),
				ToolCalls: []ToolCall{{
					ID:     tc.ID,
					Name:   tc.Name,
					Input:  tc.Input,
					Output: output,
					Status: toolStatus(handleErr),
				}},
			}
			if handleErr != nil {
				toolTurn.Content = handleErr.Error()
			}
			rt.appendTurn(toolTurn)
			if err := rt.persistTurn(ctx, toolTurn); err != nil {
				return Turn{}, fmt.Errorf("ash: persist tool turn: %w", err)
			}

			rt.emitEvent(ctx, TopicToolDone, toolTurn)
		}
	}

	return Turn{}, ErrMaxToolDepth
}

// StreamSend appends a user message and returns a TurnStream that
// yields tokens. On stream completion the finalized assistant Turn
// is appended and persisted.
//
// The mutex is NOT held across the stream lifetime. It is acquired
// only for state mutations (appending turns, persisting).
func (rt *Runtime) StreamSend(
	ctx context.Context, content string,
) (TurnStream, error) {
	if rt.provider == nil {
		return nil, ErrNoProvider
	}

	rt.mu.Lock()

	// 1. Append user turn.
	userTurn := Turn{
		ID:        fmt.Sprintf("u-%d", len(rt.session.Turns)),
		Role:      RoleUser,
		Content:   content,
		Timestamp: time.Now().UTC(),
	}
	rt.appendTurn(userTurn)
	_ = rt.persistTurn(ctx, userTurn)

	rt.emitEvent(ctx, TopicTurnStart, userTurn)

	// 2. Call provider.Stream.
	stream, err := rt.provider.Stream(ctx, rt.session.Turns)
	rt.mu.Unlock() // Release before returning stream to caller.
	if err != nil {
		return nil, fmt.Errorf("ash: provider.Stream: %w", err)
	}

	// 3. Return a wrapping stream that finalizes on done.
	return &runtimeStream{
		rt:     rt,
		ctx:    ctx,
		inner:  stream,
		tokens: make([]Token, 0, 32),
	}, nil
}

// runtimeStream wraps a provider TurnStream, accumulates tokens, and
// finalizes the assistant turn on completion or close.
type runtimeStream struct {
	rt        *Runtime
	ctx       context.Context
	inner     TurnStream
	tokens    []Token
	finalized bool
}

func (s *runtimeStream) Next() (Token, error) {
	tok, err := s.inner.Next()
	if err != nil {
		return tok, err
	}

	s.tokens = append(s.tokens, tok)

	if tok.Done {
		s.finalize()
	}

	return tok, nil
}

func (s *runtimeStream) Close() error {
	if !s.finalized {
		s.finalize()
	}
	return s.inner.Close()
}

func (s *runtimeStream) finalize() {
	if s.finalized {
		return
	}
	s.finalized = true

	// Build content from accumulated tokens.
	var content string
	for _, t := range s.tokens {
		content += t.Text
	}

	s.rt.mu.Lock()
	assistantTurn := Turn{
		ID:        fmt.Sprintf("a-%d", len(s.rt.session.Turns)),
		Role:      RoleAssistant,
		Content:   content,
		Timestamp: time.Now().UTC(),
	}
	s.rt.appendTurn(assistantTurn)
	s.rt.session.UpdatedAt = time.Now().UTC()
	s.rt.mu.Unlock()

	s.rt.emitEvent(s.ctx, TopicTurnDone, assistantTurn)
	_ = s.rt.persistTurn(s.ctx, assistantTurn)
}

// appendTurn adds a turn to the session (caller holds mu).
func (rt *Runtime) appendTurn(t Turn) {
	rt.session.Turns = append(rt.session.Turns, t)
	rt.session.UpdatedAt = time.Now().UTC()
}

// emitEvent fires an event if a publisher is configured.
func (rt *Runtime) emitEvent(
	ctx context.Context, topic string, payload any,
) {
	if rt.publisher != nil {
		_ = rt.publisher.Publish(ctx, topic, payload)
	}
}

// persistTurn appends a turn to the store if configured.
func (rt *Runtime) persistTurn(ctx context.Context, t Turn) error {
	if rt.store == nil {
		return nil
	}
	return rt.store.AppendTurn(ctx, rt.session.ID, t)
}

func toolStatus(err error) string {
	if err != nil {
		return "error"
	}
	return "success"
}
