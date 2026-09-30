// Package llm provides a provider-agnostic LLM abstraction for CLI tools.
//
// Adapters register via [Register] with a URI scheme. The [Client] facade
// wraps a resolved adapter, probes capabilities via type assertion, and
// supports fallback chains and event hooks.
//
// Tool calling round-trips through [Message]: an assistant message
// carries the model's [ToolCall]s in ToolCalls, and each result goes
// back as a role "tool" message whose ToolCallID names the call it
// answers. See [Message] for the per-role contract. Replay the calls
// verbatim: [ToolCall.ProviderData] carries provider-opaque state, such
// as Gemini thought signatures, that the provider requires back.
package llm

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sync"
	"time"

	llmerrors "hop.top/kit/go/ai/llm/errors"
	"hop.top/kit/go/runtime/bus"
)

// ---------------------------------------------------------------------------
// Core interfaces
// ---------------------------------------------------------------------------

// Provider is the base interface all adapters implement.
type Provider interface {
	Close() error
}

// Completer produces a single completion response.
type Completer interface {
	Complete(ctx context.Context, req Request) (Response, error)
}

// Streamer produces a streaming token iterator.
type Streamer interface {
	Stream(ctx context.Context, req Request) (TokenIterator, error)
}

// ToolCaller invokes tool-use / function-calling.
type ToolCaller interface {
	CallWithTools(ctx context.Context, req Request, tools []ToolDef) (ToolResponse, error)
}

// ---------------------------------------------------------------------------
// Data types
// ---------------------------------------------------------------------------

// PartType classifies a content part in a multimodal message.
type PartType string

const (
	PartTypeText  PartType = "text"
	PartTypeImage PartType = "image"
	PartTypeAudio PartType = "audio"
	PartTypeVideo PartType = "video"
)

// ContentPart is a single typed element within a multimodal message.
//
// Rule: if len(Parts) > 0 in a Message, Parts is used; otherwise Content.
type ContentPart struct {
	Type     PartType
	Text     string
	Source   MediaSource
	MimeType string
	Metadata map[string]any
}

// Message is a single role+content pair in a conversation.
//
// Multimodal rule: if len(Parts) > 0, Parts takes precedence over Content.
//
// # Tool-call linkage
//
// An agentic loop replays a tool round as two kinds of message:
//
//   - Role "assistant" with ToolCalls: the calls the model made, copied
//     from [ToolResponse.ToolCalls]. Content carries any text the model
//     produced alongside them and may be empty.
//   - Role "tool" with ToolCallID: one tool's result, in Content.
//     ToolCallID equals the [ToolCall.ID] it answers. Send one message
//     per call; adapters group consecutive results where the provider
//     expects them in a single turn.
//
// ToolCalls is valid only on role "assistant" and ToolCallID only on
// role "tool"; adapters reject either field on any other role, a tool
// result carrying Parts, and linkage the provider cannot express,
// rather than degrading it to plain text. Messages that set neither
// field map exactly as before.
type Message struct {
	Role    string
	Content string
	Parts   []ContentPart

	// ToolCalls lists the tool invocations of an assistant turn.
	ToolCalls []ToolCall
	// ToolCallID links a role "tool" result to the ToolCall it answers.
	ToolCallID string
}

// Request carries all parameters for a completion call.
//
// Temperature is a pointer so an explicit zero is distinguishable from
// unset: nil leaves the provider default; non-nil is sent on the wire,
// including 0 (deterministic sampling). MaxTokens is deliberately
// asymmetric and stays a plain int: zero is not a meaningful request
// value — every provider requires a positive limit — so 0 means unset
// and falls back to the provider default.
type Request struct {
	Messages      []Message
	Model         string
	Temperature   *float64
	MaxTokens     int
	StopSequences []string
	Extensions    map[string]any
}

// Response is the result of a completion call.
type Response struct {
	Content      string
	Role         string
	Usage        Usage
	FinishReason string
	// OutputParts holds non-text results from hooks or multimodal responses.
	OutputParts []ContentPart
}

// Usage reports token consumption.
type Usage struct {
	PromptTokens     int
	CompletionTokens int
	TotalTokens      int
}

// Token is a single streaming chunk.
type Token struct {
	Content string
	Done    bool
}

// TokenIterator yields streaming tokens.
//
// Contract: the final token has Done=true and a nil error. Every
// subsequent call to Next returns (Token{}, io.EOF). Adapters MUST
// follow this two-phase termination so consumers can rely on either
// signal.
type TokenIterator interface {
	Next() (Token, error)
	Close() error
}

// ToolDef describes a tool the model may call.
type ToolDef struct {
	Name        string
	Description string
	Parameters  json.RawMessage
}

// ToolCall is a single tool invocation returned by the model.
//
// ID is the handle a tool result quotes back in [Message.ToolCallID].
// Adapters whose provider does not return one synthesize a unique ID so
// the linkage holds across providers.
//
// ProviderData is state the provider attached to the call that must go
// back with it, unchanged, when the call is replayed (see
// [ProviderData]). Callers copy ToolCalls from [ToolResponse.ToolCalls]
// verbatim and never need to read it.
type ToolCall struct {
	ID           string
	Name         string
	Arguments    json.RawMessage
	ProviderData ProviderData
}

// ProviderData holds provider-opaque state, keyed by adapter namespace
// (for example "google"). Each value is JSON owned by that namespace's
// adapter: an adapter writes only its own key when it parses a response,
// reads only its own key when it replays history, and ignores every
// other key, so history can move through fallback chains and between
// providers without leaking one provider's state into another's request.
//
// Callers treat the map as immutable and pass it through as is; nil
// means the provider attached nothing. Keys and value shapes are
// documented by each adapter package.
type ProviderData map[string]json.RawMessage

// ToolResponse is the result of a tool-calling completion.
type ToolResponse struct {
	Content   string
	ToolCalls []ToolCall
}

// ---------------------------------------------------------------------------
// Registry
// ---------------------------------------------------------------------------

// Factory creates a Provider from a resolved configuration.
type Factory func(cfg ResolvedConfig) (Provider, error)

// Registry maps URI schemes to adapter factories.
type Registry struct {
	mu        sync.RWMutex
	factories map[string]Factory
}

// DefaultRegistry is the process-wide registry used by adapter init
// functions and the package-level [Register] / [Resolve] helpers.
var DefaultRegistry = NewRegistry()

// Register adds a factory for the given scheme to [DefaultRegistry].
func Register(scheme string, f Factory) { DefaultRegistry.Register(scheme, f) }

// Resolve looks up and creates a provider via [DefaultRegistry].
func Resolve(uri string) (Provider, error) { return DefaultRegistry.Resolve(uri) }

// Schemes returns the list of registered schemes in [DefaultRegistry].
func Schemes() []string { return DefaultRegistry.Schemes() }

// NewRegistry creates an empty adapter registry.
func NewRegistry() *Registry {
	return &Registry{factories: make(map[string]Factory)}
}

// Register adds a factory for the given scheme. Panics on duplicate.
func (r *Registry) Register(scheme string, f Factory) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if _, ok := r.factories[scheme]; ok {
		panic(fmt.Sprintf(
			"llm: adapter already registered for scheme %q", scheme,
		))
	}
	r.factories[scheme] = f
}

// Schemes returns a sorted list of registered URI schemes.
func (r *Registry) Schemes() []string {
	r.mu.RLock()
	defer r.mu.RUnlock()
	schemes := make([]string, 0, len(r.factories))
	for s := range r.factories {
		schemes = append(schemes, s)
	}
	return schemes
}

// Resolve parses a provider URI, looks up the factory by scheme,
// and creates a Provider. It uses [ParseURI] to build a minimal
// [ResolvedConfig] directly from the URI components and parameters.
func (r *Registry) Resolve(uri string) (Provider, error) {
	parsed, err := ParseURI(uri)
	if err != nil {
		return nil, fmt.Errorf("llm: invalid URI %q: %w", uri, err)
	}

	r.mu.RLock()
	f, ok := r.factories[parsed.Scheme]
	r.mu.RUnlock()

	if !ok {
		return nil, llmerrors.NewProviderNotFound(parsed.Scheme)
	}

	cfg := ResolvedConfig{
		URI: parsed,
		Provider: ProviderConfig{
			Model: parsed.Model,
		},
	}
	if parsed.Host != "" {
		cfg.Provider.BaseURL = "http://" + parsed.Host
	}
	if parsed.Params != nil {
		cfg.Provider.Params = parsed.Params
		if v, ok := parsed.Params["api_key"]; ok {
			cfg.Provider.APIKey = v
		}
		if v, ok := parsed.Params["base_url"]; ok {
			cfg.Provider.BaseURL = v
		}
	}

	return f(cfg)
}

// ---------------------------------------------------------------------------
// Client options
// ---------------------------------------------------------------------------

// Option configures a Client.
type Option func(*clientConfig)

type clientConfig struct {
	bus       bus.Bus
	fallbacks []Provider
	topics    Topics
}

// ensureBus lazily initializes an internal bus when an On* option is
// applied without a prior [WithBus]. Safe to call from multiple
// sequential option applications.
func (c *clientConfig) ensureBus() {
	if c.bus == nil {
		c.bus = bus.New()
	}
}

// resolveTopics returns the configured topics, falling back to
// [DefaultTopics] for any zero-valued field. Used by On* options that
// run during option application — before NewClient finalizes config.
func (c *clientConfig) resolveTopics() Topics {
	t := c.topics
	if t.RequestStart == "" {
		t.RequestStart = DefaultTopics.RequestStart
	}
	if t.RequestEnd == "" {
		t.RequestEnd = DefaultTopics.RequestEnd
	}
	if t.RequestError == "" {
		t.RequestError = DefaultTopics.RequestError
	}
	if t.Fallback == "" {
		t.Fallback = DefaultTopics.Fallback
	}
	if t.Route == "" {
		t.Route = DefaultTopics.Route
	}
	if t.EvaResult == "" {
		t.EvaResult = DefaultTopics.EvaResult
	}
	return t
}

// WithBus attaches an external [bus.Bus] to the client. All lifecycle
// events are published to this bus. On* helpers applied alongside
// WithBus subscribe to the same bus, so both paths receive events.
func WithBus(b bus.Bus) Option {
	return func(c *clientConfig) { c.bus = b }
}

// WithFallback appends a fallback provider to the chain.
func WithFallback(p Provider) Option {
	return func(c *clientConfig) {
		c.fallbacks = append(c.fallbacks, p)
	}
}

// OnRequest subscribes fn to the configured RequestStart topic.
// Multiple OnRequest calls stack (bus supports multiple subscribers).
// Apply this AFTER [WithTopicPrefix] / [WithTopics] when overriding
// topics so the subscription targets the renamed topic.
func OnRequest(fn func(Request)) Option {
	return func(c *clientConfig) {
		c.ensureBus()
		c.bus.Subscribe(string(c.resolveTopics().RequestStart),
			func(_ context.Context, e bus.Event) error {
				if p, ok := e.Payload.(RequestStartPayload); ok {
					fn(p.Request)
				}
				return nil
			})
	}
}

// OnResponse subscribes fn to the configured RequestEnd topic.
func OnResponse(fn func(Response, time.Duration)) Option {
	return func(c *clientConfig) {
		c.ensureBus()
		c.bus.Subscribe(string(c.resolveTopics().RequestEnd),
			func(_ context.Context, e bus.Event) error {
				if p, ok := e.Payload.(RequestEndPayload); ok {
					fn(p.Response, p.Duration)
				}
				return nil
			})
	}
}

// OnError subscribes fn to the configured RequestError topic.
func OnError(fn func(error)) Option {
	return func(c *clientConfig) {
		c.ensureBus()
		c.bus.Subscribe(string(c.resolveTopics().RequestError),
			func(_ context.Context, e bus.Event) error {
				if p, ok := e.Payload.(RequestErrorPayload); ok {
					fn(p.Err)
				}
				return nil
			})
	}
}

// OnFallback subscribes fn to the configured Fallback topic.
// from/to are zero-based indices (0 = primary).
func OnFallback(fn func(from, to int, err error)) Option {
	return func(c *clientConfig) {
		c.ensureBus()
		c.bus.Subscribe(string(c.resolveTopics().Fallback),
			func(_ context.Context, e bus.Event) error {
				if p, ok := e.Payload.(FallbackPayload); ok {
					fn(p.From, p.To, p.Err)
				}
				return nil
			})
	}
}

// OnRoute subscribes fn to the configured Route topic.
func OnRoute(fn func(router string, score float64, model string)) Option {
	return func(c *clientConfig) {
		c.ensureBus()
		c.bus.Subscribe(string(c.resolveTopics().Route),
			func(_ context.Context, e bus.Event) error {
				if p, ok := e.Payload.(RoutePayload); ok {
					fn(p.Router, p.Score, p.Model)
				}
				return nil
			})
	}
}

// OnEvaResult subscribes fn to the configured EvaResult topic.
func OnEvaResult(fn func(contract string, passed bool, violations []string)) Option {
	return func(c *clientConfig) {
		c.ensureBus()
		c.bus.Subscribe(string(c.resolveTopics().EvaResult),
			func(_ context.Context, e bus.Event) error {
				if p, ok := e.Payload.(EvaResultPayload); ok {
					fn(p.Contract, p.Passed, p.Violations)
				}
				return nil
			})
	}
}

// ---------------------------------------------------------------------------
// Client
// ---------------------------------------------------------------------------

// Client is a facade that wraps a resolved adapter and provides
// capability probing, fallback chains, and event hooks.
type Client struct {
	primary Provider
	cfg     clientConfig
}

// NewClient creates a Client wrapping the given primary adapter.
func NewClient(primary Provider, opts ...Option) *Client {
	var cfg clientConfig
	for _, o := range opts {
		o(&cfg)
	}
	cfg.topics = cfg.resolveTopics()
	return &Client{primary: primary, cfg: cfg}
}

// Provider returns the underlying primary adapter for direct access
// or type assertion.
func (c *Client) Provider() Provider { return c.primary }

// Topics returns the bus topics this client publishes on. Subscribers
// that need to wire up handlers dynamically (after [NewClient]) read
// from this method instead of the package-level constants.
func (c *Client) Topics() Topics { return c.cfg.topics }

// publish sends an event on the bus if one is configured; nil bus is
// a no-op. Errors are intentionally ignored — lifecycle events are
// best-effort and must not affect LLM operations. A sync subscriber
// returning an error will NOT veto the LLM call.
func (c *Client) publish(ctx context.Context, topic bus.Topic, payload any) {
	if c.cfg.bus != nil {
		_ = c.cfg.bus.Publish(ctx, bus.NewEvent(topic, "kit.ai.client", payload))
	}
}

// Capabilities returns the list of capabilities the primary adapter
// supports, probed via type assertion.
func (c *Client) Capabilities() []string {
	var caps []string
	if _, ok := c.primary.(Completer); ok {
		caps = append(caps, "complete")
	}
	if _, ok := c.primary.(Streamer); ok {
		caps = append(caps, "stream")
	}
	if _, ok := c.primary.(ToolCaller); ok {
		caps = append(caps, "tool_call")
	}
	if _, ok := c.primary.(ImageGenerator); ok {
		caps = append(caps, "image_gen")
	}
	if _, ok := c.primary.(Transcriber); ok {
		caps = append(caps, "transcribe")
	}
	if _, ok := c.primary.(SpeechSynthesizer); ok {
		caps = append(caps, "synthesize")
	}
	if _, ok := c.primary.(VideoAnalyzer); ok {
		caps = append(caps, "video_analyze")
	}
	if _, ok := c.primary.(VideoGenerator); ok {
		caps = append(caps, "video_gen")
	}
	return caps
}

// Complete delegates to the adapter's Completer. Supports fallback
// and event hooks.
func (c *Client) Complete(ctx context.Context, req Request) (Response, error) {
	c.publish(ctx, c.cfg.topics.RequestStart, RequestStartPayload{Request: req})

	chain := append([]Provider{c.primary}, c.cfg.fallbacks...)
	var errs []error

	for i, p := range chain {
		comp, ok := p.(Completer)
		if !ok {
			err := llmerrors.NewCapabilityNotSupported(
				"complete", fmt.Sprintf("provider[%d]", i),
			)
			errs = append(errs, err)
			continue
		}

		start := time.Now()
		resp, err := comp.Complete(ctx, req)
		dur := time.Since(start)

		if err == nil {
			c.publish(ctx, c.cfg.topics.RequestEnd, RequestEndPayload{
				Response: resp,
				Duration: dur,
			})
			return resp, nil
		}

		errs = append(errs, err)

		// Non-fallbackable errors return immediately.
		if !llmerrors.IsFallbackable(err) {
			c.publish(ctx, c.cfg.topics.RequestError, RequestErrorPayload{Err: err, ErrMessage: err.Error()})
			return Response{}, err
		}

		// Fire fallback hook before trying next.
		if i < len(chain)-1 {
			c.publish(ctx, c.cfg.topics.Fallback, FallbackPayload{
				From: i, To: i + 1, Err: err, ErrMessage: err.Error(),
			})
		}
	}

	// If every provider lacked the capability, return that directly.
	if allCapabilityErrors(errs) {
		return Response{}, errs[len(errs)-1]
	}

	exhausted := llmerrors.NewFallbackExhausted(errs)
	c.publish(ctx, c.cfg.topics.RequestError, RequestErrorPayload{Err: exhausted, ErrMessage: exhausted.Error()})
	return Response{}, exhausted
}

func allCapabilityErrors(errs []error) bool {
	for _, e := range errs {
		var capErr *llmerrors.ErrCapabilityNotSupported
		if !errors.As(e, &capErr) {
			return false
		}
	}
	return len(errs) > 0
}

// Stream delegates to the adapter's Streamer interface. Supports fallback
// and event hooks.
func (c *Client) Stream(ctx context.Context, req Request) (TokenIterator, error) {
	c.publish(ctx, c.cfg.topics.RequestStart, RequestStartPayload{Request: req})

	chain := append([]Provider{c.primary}, c.cfg.fallbacks...)
	var errs []error

	for i, p := range chain {
		s, ok := p.(Streamer)
		if !ok {
			err := llmerrors.NewCapabilityNotSupported(
				"stream", fmt.Sprintf("provider[%d]", i),
			)
			errs = append(errs, err)
			continue
		}

		iter, err := s.Stream(ctx, req)
		if err == nil {
			return iter, nil
		}

		errs = append(errs, err)

		if !llmerrors.IsFallbackable(err) {
			c.publish(ctx, c.cfg.topics.RequestError, RequestErrorPayload{Err: err, ErrMessage: err.Error()})
			return nil, err
		}

		if i < len(chain)-1 {
			c.publish(ctx, c.cfg.topics.Fallback, FallbackPayload{
				From: i, To: i + 1, Err: err, ErrMessage: err.Error(),
			})
		}
	}

	if allCapabilityErrors(errs) {
		return nil, errs[len(errs)-1]
	}

	exhausted := llmerrors.NewFallbackExhausted(errs)
	c.publish(ctx, c.cfg.topics.RequestError, RequestErrorPayload{Err: exhausted, ErrMessage: exhausted.Error()})
	return nil, exhausted
}

// CallWithTools delegates to the adapter's ToolCaller interface. Supports
// fallback and event hooks.
func (c *Client) CallWithTools(
	ctx context.Context, req Request, tools []ToolDef,
) (ToolResponse, error) {
	c.publish(ctx, c.cfg.topics.RequestStart, RequestStartPayload{Request: req})

	chain := append([]Provider{c.primary}, c.cfg.fallbacks...)
	var errs []error

	for i, p := range chain {
		tc, ok := p.(ToolCaller)
		if !ok {
			err := llmerrors.NewCapabilityNotSupported(
				"tool_call", fmt.Sprintf("provider[%d]", i),
			)
			errs = append(errs, err)
			continue
		}

		start := time.Now()
		resp, err := tc.CallWithTools(ctx, req, tools)
		dur := time.Since(start)

		if err == nil {
			c.publish(ctx, c.cfg.topics.RequestEnd, RequestEndPayload{
				Response: Response{Content: resp.Content},
				Duration: dur,
			})
			return resp, nil
		}

		errs = append(errs, err)

		if !llmerrors.IsFallbackable(err) {
			c.publish(ctx, c.cfg.topics.RequestError, RequestErrorPayload{Err: err, ErrMessage: err.Error()})
			return ToolResponse{}, err
		}

		if i < len(chain)-1 {
			c.publish(ctx, c.cfg.topics.Fallback, FallbackPayload{
				From: i, To: i + 1, Err: err, ErrMessage: err.Error(),
			})
		}
	}

	if allCapabilityErrors(errs) {
		return ToolResponse{}, errs[len(errs)-1]
	}

	exhausted := llmerrors.NewFallbackExhausted(errs)
	c.publish(ctx, c.cfg.topics.RequestError, RequestErrorPayload{Err: exhausted, ErrMessage: exhausted.Error()})
	return ToolResponse{}, exhausted
}
