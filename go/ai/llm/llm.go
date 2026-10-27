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
//
// Provider keys: [Resolve] reads a key only from the URI's api_key
// param, and drops a blank one (empty or only whitespace), which no
// key source counts as a key. [ApplyAPIKey] puts the scheme's key there from llm.yaml, a
// secret store or the environment, under the names [ProviderKeyFor]
// resolves (adapter [Declaration], aim catalog facts, <SCHEME>_API_KEY);
// a required key found nowhere yields a [*MissingKeyError].
//
// Schemes: [Resolve] also accepts a curated aim alias of a registered
// scheme ("fireworks-ai", "togetherai") and a provider from the cached
// aim catalog that no adapter registers, through the adapter claiming
// its protocol. Neither reads the network.
package llm

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
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

// ToolStreamer streams a tool-calling completion: text deltas as tokens,
// then the whole turn (tool calls, stop reason, usage) from
// [ToolStream.Outcome].
type ToolStreamer interface {
	StreamWithTools(ctx context.Context, req Request, tools []ToolDef) (ToolStream, error)
}

// ToolStream is a [TokenIterator] whose finished turn can be read back.
// Outcome reports ok only once the final token (Done) was returned;
// after an error it stays false.
type ToolStream interface {
	TokenIterator
	Outcome() (StreamOutcome, bool)
}

// StreamOutcome is what a finished stream folded into.
type StreamOutcome struct {
	Content string
	// Thinking holds the turn's thinking blocks in order; replay them
	// on the assistant message, unchanged, in a follow-up turn.
	Thinking     []ThinkingBlock
	ToolCalls    []ToolCall
	FinishReason string
	Usage        Usage
}

// ThinkingBlock is one block of a model's extended thinking. Signature
// verifies Thinking; a redacted block carries only Redacted, the
// provider's opaque data. Pass blocks back exactly as received.
type ThinkingBlock struct {
	Thinking  string
	Signature string
	Redacted  string
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

	// Thinking replays an assistant turn's thinking blocks, exactly as
	// the provider returned them (in [Response.Thinking],
	// [ToolResponse.Thinking] or [StreamOutcome.Thinking]). Required
	// with tool use when thinking is enabled. Assistant only.
	Thinking []ThinkingBlock

	// CacheControl marks a prompt-cache breakpoint after this message:
	// on a system message its text, otherwise its last content block.
	CacheControl *CacheControl
}

// CacheControl is a prompt-cache breakpoint. TTL is "" (the provider
// default, five minutes on Anthropic), "5m" or "1h".
type CacheControl struct {
	TTL string
}

// ThinkingConfig enables extended thinking with a token budget, which
// must be below the request's max tokens.
type ThinkingConfig struct {
	BudgetTokens int
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
	// Thinking, when set, enables extended thinking; thinking deltas
	// then stream as [Token.Thinking].
	Thinking *ThinkingConfig
	// StreamEvents, when set, makes a stream also yield incremental
	// tool-call events ([Token.ToolCall]) and usage snapshots
	// ([Token.Usage]). Unset, a stream yields exactly the tokens it
	// always has. Ignored by Complete and CallWithTools.
	StreamEvents bool
	// ToolChoice, when set, constrains tool use on a tool-calling
	// request (CallWithTools, StreamWithTools); nil leaves the provider
	// default. See [ToolChoice].
	ToolChoice *ToolChoice
}

// Response is the result of a completion call.
type Response struct {
	Content      string
	Role         string
	Usage        Usage
	FinishReason string
	// OutputParts holds non-text results from hooks or multimodal responses.
	OutputParts []ContentPart
	// Thinking holds the turn's thinking blocks, to replay unchanged.
	Thinking []ThinkingBlock
}

// Usage reports token consumption.
type Usage struct {
	PromptTokens     int
	CompletionTokens int
	// CacheReadTokens and CacheCreationTokens are input tokens read
	// from or written to the prompt cache, apart from PromptTokens.
	CacheReadTokens     int
	CacheCreationTokens int
	// TotalTokens is every input and output token, cache ones included.
	TotalTokens int
}

// Price is a model's rate per million tokens, by kind.
type Price struct {
	Input, Output, CacheRead, CacheWrite float64
}

// Cost is u's cost at p, cache reads and writes at their own rates.
func (u Usage) Cost(p Price) float64 {
	return (float64(u.PromptTokens)*p.Input +
		float64(u.CompletionTokens)*p.Output +
		float64(u.CacheReadTokens)*p.CacheRead +
		float64(u.CacheCreationTokens)*p.CacheWrite) / 1e6
}

// Token is a single streaming chunk. A token carries one thing: a
// text delta (Content), a thinking delta (Thinking), a tool-call event
// (ToolCall), a usage snapshot (Usage), or the end (Done).
type Token struct {
	Content string
	// Thinking is a thinking delta, sent only when the request enabled
	// [Request.Thinking]; Content is then empty.
	Thinking string
	// ToolCall is an incremental tool-call event, sent only when the
	// request set [Request.StreamEvents]. See [ToolCallEvent].
	ToolCall *ToolCallEvent
	// Usage is the turn's usage so far, sent only when the request set
	// [Request.StreamEvents], each time the provider reports it. It is a
	// cumulative snapshot, never a delta: counts never go down, and the
	// last one equals the finished turn's usage ([StreamOutcome.Usage]).
	Usage *Usage
	Done  bool
}

// ToolCallEventKind is the step of a streamed tool call.
type ToolCallEventKind string

const (
	// ToolCallStart opens a call: Index, ID and Name are set.
	ToolCallStart ToolCallEventKind = "start"
	// ToolCallDelta carries a fragment of the call's JSON arguments.
	ToolCallDelta ToolCallEventKind = "delta"
	// ToolCallEnd closes a call: Call holds it complete.
	ToolCallEnd ToolCallEventKind = "end"
)

// ToolCallEvent is one step of a tool call as it streams.
//
// For each call: one start, then any deltas, then one end, in that
// order; events of parallel calls may interleave and are told apart by
// Index, the call's position in [StreamOutcome.ToolCalls]. A call that
// started but never ended (the stream failed, was canceled or closed)
// did not complete: no end event is made up for it.
type ToolCallEvent struct {
	Kind  ToolCallEventKind
	Index int
	// ID and Name are set on start and end. Name is the tool's name as
	// the caller defined it, whatever name went over the wire.
	ID   string
	Name string
	// ArgumentsDelta is a raw fragment of the arguments' JSON, on delta
	// only; fragments concatenate to the complete arguments and are not
	// valid JSON alone.
	ArgumentsDelta string
	// Call is the complete call, on end only: exactly the entry at Index
	// in [StreamOutcome.ToolCalls].
	Call *ToolCall
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
	// CacheControl marks a prompt-cache breakpoint after this tool.
	CacheControl *CacheControl
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
	// Thinking holds the turn's thinking blocks, to replay unchanged.
	Thinking     []ThinkingBlock
	FinishReason string
	Usage        Usage
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

	// onRequest and onResponse are the in-process hooks.
	onRequest  []func(Request)
	onResponse []func(Response, time.Duration)
	// requestContent and responseContent, when set, put content on the
	// bus payloads ([WithRequestContent], [WithResponseContent]).
	requestContent  func(Request) Request
	responseContent func(Response) Response
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

// OnRequest calls fn with the full request before each call, in
// process, synchronously; it never goes through the bus, whose
// RequestStart event carries no content (see [RequestStartPayload]).
// Multiple OnRequest calls stack, in order.
func OnRequest(fn func(Request)) Option {
	return func(c *clientConfig) { c.onRequest = append(c.onRequest, fn) }
}

// OnResponse calls fn with the full response and the call's duration
// after each successful call other than a stream, in process,
// synchronously; it never goes through the bus, whose RequestEnd event
// carries no content (see [RequestEndPayload]). For CallWithTools the
// response holds the turn's text, thinking, stop reason and usage.
// Multiple OnResponse calls stack, in order.
func OnResponse(fn func(Response, time.Duration)) Option {
	return func(c *clientConfig) { c.onResponse = append(c.onResponse, fn) }
}

// WithRequestContent puts request content on the bus: the RequestStart
// payload's Request is fn applied to a copy of the request (fn may
// rewrite it freely; the caller's request is not touched). A nil fn
// publishes the content as is. Without this option the payload is
// metadata only. [RedactRequest] builds an fn that masks secrets with
// a [redact.Redactor].
//
// The content then reaches every subscriber, including sinks that
// forward or store events; publish it only to a bus you control.
func WithRequestContent(fn func(Request) Request) Option {
	if fn == nil {
		fn = func(r Request) Request { return r }
	}
	return func(c *clientConfig) { c.requestContent = fn }
}

// WithResponseContent is [WithRequestContent] for the RequestEnd
// payload's Response. [RedactResponse] builds a masking fn.
func WithResponseContent(fn func(Response) Response) Option {
	if fn == nil {
		fn = func(r Response) Response { return r }
	}
	return func(c *clientConfig) { c.responseContent = fn }
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

// started runs the OnRequest hooks and publishes RequestStart for a
// call of kind op offering tools tools.
func (c *Client) started(ctx context.Context, op string, req Request, tools int) {
	for _, fn := range c.cfg.onRequest {
		fn(req)
	}
	if c.cfg.bus == nil {
		return
	}
	p := requestStart(op, req, tools)
	if c.cfg.requestContent != nil {
		r := c.cfg.requestContent(cloneRequest(req))
		p.Request = &r
	}
	c.publish(ctx, c.cfg.topics.RequestStart, p)
}

// ended runs the OnResponse hooks and publishes RequestEnd.
func (c *Client) ended(ctx context.Context, op string, resp Response, calls []ToolCall, d time.Duration) {
	for _, fn := range c.cfg.onResponse {
		fn(resp, d)
	}
	if c.cfg.bus == nil {
		return
	}
	p := requestEnd(op, resp, calls, d)
	if c.cfg.responseContent != nil {
		r := c.cfg.responseContent(cloneResponse(resp))
		p.Response = &r
	}
	c.publish(ctx, c.cfg.topics.RequestEnd, p)
}

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
	if _, ok := c.primary.(ToolStreamer); ok {
		caps = append(caps, "stream_tools")
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
	c.started(ctx, "complete", req, 0)

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
			c.ended(ctx, "complete", resp, nil, dur)
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
	c.started(ctx, "stream", req, 0)

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
	c.started(ctx, "tool_call", req, len(tools))

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
			c.ended(ctx, "tool_call", Response{
				Content:      resp.Content,
				Role:         "assistant",
				Usage:        resp.Usage,
				FinishReason: resp.FinishReason,
				Thinking:     resp.Thinking,
			}, resp.ToolCalls, dur)
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

// StreamWithTools delegates to the adapter's [ToolStreamer]. Fallback
// applies to opening the stream only, as for [Client.Stream].
func (c *Client) StreamWithTools(ctx context.Context, req Request, tools []ToolDef) (ToolStream, error) {
	c.started(ctx, "stream_tools", req, len(tools))

	chain := append([]Provider{c.primary}, c.cfg.fallbacks...)
	var errs []error
	for i, p := range chain {
		ts, ok := p.(ToolStreamer)
		if !ok {
			errs = append(errs, llmerrors.NewCapabilityNotSupported("stream_tools", fmt.Sprintf("provider[%d]", i)))
			continue
		}
		st, err := ts.StreamWithTools(ctx, req, tools)
		if err == nil {
			return st, nil
		}
		errs = append(errs, err)
		if !llmerrors.IsFallbackable(err) {
			c.publish(ctx, c.cfg.topics.RequestError, RequestErrorPayload{Err: err, ErrMessage: err.Error()})
			return nil, err
		}
		if i < len(chain)-1 {
			c.publish(ctx, c.cfg.topics.Fallback, FallbackPayload{From: i, To: i + 1, Err: err, ErrMessage: err.Error()})
		}
	}
	if allCapabilityErrors(errs) {
		return nil, errs[len(errs)-1]
	}
	exhausted := llmerrors.NewFallbackExhausted(errs)
	c.publish(ctx, c.cfg.topics.RequestError, RequestErrorPayload{Err: exhausted, ErrMessage: exhausted.Error()})
	return nil, exhausted
}
