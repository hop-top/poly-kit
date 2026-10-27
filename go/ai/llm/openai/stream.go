package openai

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"slices"
	"strings"

	oai "github.com/openai/openai-go"
	"github.com/openai/openai-go/option"
	"github.com/openai/openai-go/packages/param"
	"github.com/openai/openai-go/packages/ssestream"

	"hop.top/kit/go/ai/llm"
	llmerrors "hop.top/kit/go/ai/llm/errors"
)

// openStream sends params as a streamed request. The SSE body is read
// here, not by the SDK's stream, which hides data: [DONE] and drops the
// fields of an error chunk.
func (a *Adapter) openStream(
	ctx context.Context, params oai.ChatCompletionNewParams, req llm.Request, names *llm.ToolNames,
) (*streamIter, error) {
	model := a.effectiveModel(req)
	if req.StreamEvents {
		// Usage streams only when asked for, in a chunk after the last
		// choice; a stream without events does not ask.
		params.StreamOptions.IncludeUsage = param.NewOpt(true)
	}
	var res *http.Response
	err := a.client.Post(ctx, "chat/completions", params, &res, option.WithJSONSet("stream", true))
	if err != nil {
		if res != nil && res.Body != nil {
			_ = res.Body.Close()
		}
		return nil, mapError(err, a.scheme, model)
	}
	dec := ssestream.NewDecoder(res)
	if dec == nil {
		return nil, fmt.Errorf("%s: stream response has no body", a.scheme)
	}
	return &streamIter{
		dec: dec, res: res, scheme: a.scheme, model: model, names: names,
		events: req.StreamEvents, byWire: map[int64]int{},
	}, nil
}

// partialCall is a tool call as its fragments arrive.
type partialCall struct {
	wire     int64 // the chunk's tool_calls[].index
	id, name string
	args     strings.Builder
	// pending holds argument fragments that came before the call
	// started (no id and name yet).
	pending string
	started bool
}

type streamIter struct {
	dec    ssestream.Decoder
	res    *http.Response
	scheme string
	model  string
	names  *llm.ToolNames
	// events streams tool-call events and usage snapshots
	// ([llm.Request.StreamEvents]).
	events bool

	content strings.Builder
	finish  string
	usage   llm.Usage
	// calls are the turn's tool calls in order of first fragment;
	// byWire maps a fragment's index to its position.
	calls  []*partialCall
	byWire map[int64]int

	queue   []llm.Token
	done    bool
	outcome *llm.StreamOutcome
}

// Outcome is the finished turn, once Next returned Done.
func (s *streamIter) Outcome() (llm.StreamOutcome, bool) {
	if s.outcome == nil {
		return llm.StreamOutcome{}, false
	}
	return *s.outcome, true
}

func (s *streamIter) Close() error {
	s.done = true
	s.queue = nil
	return s.dec.Close()
}

func (s *streamIter) Next() (llm.Token, error) {
	for {
		if len(s.queue) > 0 {
			tok := s.queue[0]
			s.queue = s.queue[1:]
			return tok, nil
		}
		if s.done {
			return llm.Token{}, io.EOF
		}
		if !s.dec.Next() {
			if err := s.dec.Err(); err != nil {
				return s.fail(mapError(err, s.scheme, s.model))
			}
			// Some compatible servers end without [DONE] once a choice
			// finished; any other end is incomplete.
			if s.finish == "" {
				return s.fail(llmerrors.NewStreamTruncated(s.scheme))
			}
			return s.finished(), nil
		}
		data := bytes.TrimSpace(s.dec.Event().Data)
		if len(data) == 0 {
			continue
		}
		if bytes.Equal(data, []byte("[DONE]")) {
			return s.finished(), nil
		}
		if err := s.chunk(data); err != nil {
			return s.fail(err)
		}
	}
}

// fail ends the stream with err: no outcome, no end event for an open
// call.
func (s *streamIter) fail(err error) (llm.Token, error) {
	s.done = true
	s.queue = nil
	return llm.Token{}, err
}

// finished ends the stream and folds the outcome.
func (s *streamIter) finished() llm.Token {
	s.done = true
	calls := make([]llm.ToolCall, len(s.calls))
	for i, c := range s.calls {
		calls[i] = s.call(c)
	}
	s.outcome = &llm.StreamOutcome{
		Content:      s.content.String(),
		ToolCalls:    calls,
		FinishReason: s.finish,
		Usage:        s.usage,
	}
	return llm.Token{Done: true}
}

// call is c complete: arguments {} when none streamed, the name the
// caller defined.
func (s *streamIter) call(c *partialCall) llm.ToolCall {
	args := c.args.String()
	if args == "" {
		args = "{}"
	}
	return llm.ToolCall{ID: c.id, Name: s.names.Original(c.name), Arguments: json.RawMessage(args)}
}

// chunk folds one data payload, queueing the tokens it yields: text,
// then tool-call events, then usage.
func (s *streamIter) chunk(data []byte) error {
	var probe struct {
		Error json.RawMessage `json:"error"`
	}
	if err := json.Unmarshal(data, &probe); err != nil {
		return fmt.Errorf("%s: decode stream chunk: %w", s.scheme, err)
	}
	if len(probe.Error) > 0 && probe.Error[0] == '{' {
		return s.chunkError(probe.Error)
	}
	var c oai.ChatCompletionChunk
	if err := json.Unmarshal(data, &c); err != nil {
		return fmt.Errorf("%s: decode stream chunk: %w", s.scheme, err)
	}
	if len(c.Choices) > 0 {
		ch := c.Choices[0]
		if ch.Delta.Content != "" {
			s.content.WriteString(ch.Delta.Content)
			s.queue = append(s.queue, llm.Token{Content: ch.Delta.Content})
		}
		if s.finish == "" {
			// A finished choice's calls are closed: later fragments are
			// not folded.
			for _, tc := range ch.Delta.ToolCalls {
				s.fragment(tc)
			}
		}
		if ch.FinishReason != "" && s.finish == "" {
			s.finish = ch.FinishReason
			s.endCalls()
		}
	}
	if c.JSON.Usage.Valid() {
		s.usage = llm.Usage{
			PromptTokens:     int(c.Usage.PromptTokens),
			CompletionTokens: int(c.Usage.CompletionTokens),
			TotalTokens:      int(c.Usage.TotalTokens),
		}
		if s.events {
			u := s.usage
			s.queue = append(s.queue, llm.Token{Usage: &u})
		}
	}
	return nil
}

// fragment folds one tool_calls[i] fragment. A call starts on the
// fragment that completes its id and name; argument fragments before
// that go out with the start.
func (s *streamIter) fragment(tc oai.ChatCompletionChunkChoiceDeltaToolCall) {
	wire := tc.Index
	if !tc.JSON.Index.Valid() {
		wire = int64(len(s.calls))
	}
	i, ok := s.byWire[wire]
	if !ok {
		i = len(s.calls)
		s.byWire[wire] = i
		s.calls = append(s.calls, &partialCall{wire: wire})
	}
	c := s.calls[i]
	if c.id == "" {
		c.id = tc.ID
	}
	if c.name == "" {
		c.name = tc.Function.Name
	}
	frag := tc.Function.Arguments
	c.args.WriteString(frag)
	if !s.events {
		return
	}
	if !c.started {
		c.pending += frag
		if c.id != "" && c.name != "" {
			s.start(i, c)
		}
		return
	}
	if frag != "" {
		s.queue = append(s.queue, llm.Token{ToolCall: &llm.ToolCallEvent{
			Kind: llm.ToolCallDelta, Index: i, ArgumentsDelta: frag,
		}})
	}
}

func (s *streamIter) start(i int, c *partialCall) {
	c.started = true
	s.queue = append(s.queue, llm.Token{ToolCall: &llm.ToolCallEvent{
		Kind: llm.ToolCallStart, Index: i, ID: c.id, Name: s.names.Original(c.name),
	}})
	if c.pending != "" {
		s.queue = append(s.queue, llm.Token{ToolCall: &llm.ToolCallEvent{
			Kind: llm.ToolCallDelta, Index: i, ArgumentsDelta: c.pending,
		}})
		c.pending = ""
	}
}

// endCalls closes every open call, in index order, when a choice
// finishes; a call that never got its id or name starts first.
func (s *streamIter) endCalls() {
	order := make([]int, len(s.calls))
	for i := range order {
		order[i] = i
	}
	slices.SortStableFunc(order, func(a, b int) int {
		return int(s.calls[a].wire - s.calls[b].wire)
	})
	for _, i := range order {
		c := s.calls[i]
		if !s.events {
			continue
		}
		if !c.started {
			s.start(i, c)
		}
		call := s.call(c)
		s.queue = append(s.queue, llm.Token{ToolCall: &llm.ToolCallEvent{
			Kind: llm.ToolCallEnd, Index: i, ID: call.ID, Name: call.Name, Call: &call,
		}})
	}
}

// chunkError maps an error chunk ({"error": {...}}) that arrived on a
// 200 response. It comes back as an [oai.Error] with status 0 (the
// HTTP status said nothing about it), classified by mapError.
func (s *streamIter) chunkError(raw json.RawMessage) error {
	apiErr := &oai.Error{Request: s.res.Request, Response: s.res}
	if err := apiErr.UnmarshalJSON(raw); err != nil {
		return fmt.Errorf("%s: stream error: %s", s.scheme, raw)
	}
	return mapError(apiErr, s.scheme, s.model)
}
