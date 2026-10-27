package google

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"strings"

	"hop.top/kit/go/ai/llm"
	llmerrors "hop.top/kit/go/ai/llm/errors"
)

var _ llm.ToolStreamer = (*Adapter)(nil)

// StreamWithTools streams a tool-calling streamGenerateContent request:
// text parts as tokens, then the calls, finish reason and usage from
// Outcome. Tool names go out as [llm.GeminiToolNames] maps them and
// come back as the caller defined them.
//
// With [llm.Request.StreamEvents], each functionCall part, which Gemini
// sends whole, yields a start, one delta (its args; none when it has
// none) and an end back to back, and every chunk that carries
// usageMetadata yields a usage snapshot after its parts.
//
// The turn ends on the chunk with a finishReason, or a promptFeedback
// blockReason for a blocked prompt; a body that ends before either is
// an [llmerrors.ErrStreamTruncated].
func (a *Adapter) StreamWithTools(
	ctx context.Context, req llm.Request, tools []llm.ToolDef,
) (llm.ToolStream, error) {
	return a.openToolStream(ctx, req, tools)
}

func (a *Adapter) openToolStream(
	ctx context.Context, req llm.Request, tools []llm.ToolDef,
) (*toolStream, error) {
	model := a.effectiveModel(req)
	body, names, err := a.buildBody(ctx, req, tools)
	if err != nil {
		return nil, err
	}
	rc, err := a.openSSE(ctx, model, body)
	if err != nil {
		return nil, err
	}
	return &toolStream{
		scanner: newSSEScanner(rc),
		body:    rc,
		model:   model,
		names:   names,
		events:  req.StreamEvents,
	}, nil
}

// openSSE posts body to the model's streamGenerateContent and returns
// the event stream once the status is a success.
func (a *Adapter) openSSE(
	ctx context.Context, model string, body []byte,
) (io.ReadCloser, error) {
	httpReq, err := a.newRequest(
		ctx, model, "streamGenerateContent?alt=sse", body,
	)
	if err != nil {
		return nil, err
	}
	resp, err := a.client.Do(httpReq)
	if err != nil {
		return nil, llm.RedactURLError(err)
	}
	if err := a.checkStatus(resp, model); err != nil {
		resp.Body.Close()
		return nil, err
	}
	return resp.Body, nil
}

// newSSEScanner reads SSE lines; one line holds at most 1 MiB.
func newSSEScanner(r io.Reader) *bufio.Scanner {
	scanner := bufio.NewScanner(r)
	scanner.Buffer(make([]byte, 0, 64*1024), 1024*1024)
	return scanner
}

// toolStream folds streamGenerateContent chunks into tokens and the
// turn's outcome.
type toolStream struct {
	scanner *bufio.Scanner
	body    io.ReadCloser
	model   string
	// names maps returned tool names back to the caller's.
	names *llm.ToolNames
	// events streams tool-call events and usage snapshots
	// ([llm.Request.StreamEvents]).
	events bool

	// pending holds the tokens of a chunk not yet returned.
	pending []llm.Token
	// done: no more chunks are read (finished or failed).
	done bool

	text    strings.Builder
	calls   []llm.ToolCall
	usage   llm.Usage
	finish  string
	outcome *llm.StreamOutcome
}

// Outcome is the finished turn, once Next returned Done.
func (s *toolStream) Outcome() (llm.StreamOutcome, bool) {
	if s.outcome == nil {
		return llm.StreamOutcome{}, false
	}
	return *s.outcome, true
}

// Next returns the next token; see [llm.TokenIterator].
func (s *toolStream) Next() (llm.Token, error) {
	for {
		if len(s.pending) > 0 {
			tok := s.pending[0]
			s.pending = s.pending[1:]
			if tok.Done {
				s.outcome = &llm.StreamOutcome{
					Content:      s.text.String(),
					ToolCalls:    s.calls,
					FinishReason: s.finish,
					Usage:        s.usage,
				}
			}
			return tok, nil
		}
		if s.done {
			return llm.Token{}, io.EOF
		}
		if !s.scanner.Scan() {
			s.done = true
			if err := s.scanner.Err(); err != nil {
				return llm.Token{}, fmt.Errorf("gemini: stream read: %w", err)
			}
			// No finishReason: the response is incomplete.
			return llm.Token{}, llmerrors.NewStreamTruncated("gemini")
		}
		data, ok := strings.CutPrefix(s.scanner.Text(), "data: ")
		if !ok || data == "" {
			continue
		}
		var gr generateResponse
		if err := json.Unmarshal([]byte(data), &gr); err != nil {
			s.done = true
			return llm.Token{}, fmt.Errorf("gemini: decode stream chunk: %w", err)
		}
		if gr.Error != nil {
			s.done = true
			return llm.Token{}, mapStreamError(gr.Error, s.model)
		}
		s.fold(gr)
	}
}

// fold queues one chunk's tokens: its parts in order, its usage, then
// Done when it ends the turn.
func (s *toolStream) fold(gr generateResponse) {
	finish := ""
	if len(gr.Candidates) > 0 {
		c := gr.Candidates[0]
		for _, p := range c.Content.Parts {
			if p.Text != "" {
				s.text.WriteString(p.Text)
				s.pending = append(s.pending, llm.Token{Content: p.Text})
			}
			if p.FunctionCall != nil {
				s.call(p)
			}
		}
		finish = c.FinishReason
	} else if gr.PromptFeedback != nil {
		finish = gr.PromptFeedback.BlockReason
	}
	if gr.UsageMetadata != nil {
		s.addUsage(usageOf(gr.UsageMetadata))
		if s.events {
			u := s.usage
			s.pending = append(s.pending, llm.Token{Usage: &u})
		}
	}
	if finish != "" {
		s.finish = mapFinishReason(finish)
		s.done = true
		s.pending = append(s.pending, llm.Token{Done: true})
	}
}

// call records a functionCall part and, when asked for, queues its
// start, delta and end.
func (s *toolStream) call(p part) {
	id := p.FunctionCall.ID
	if id == "" {
		id = newCallID()
	}
	tc := llm.ToolCall{
		ID:           id,
		Name:         s.names.Original(p.FunctionCall.Name),
		Arguments:    p.FunctionCall.Args,
		ProviderData: signatureData(p.ThoughtSignature),
	}
	i := len(s.calls)
	s.calls = append(s.calls, tc)
	if !s.events {
		return
	}
	s.pending = append(s.pending, llm.Token{ToolCall: &llm.ToolCallEvent{
		Kind: llm.ToolCallStart, Index: i, ID: tc.ID, Name: tc.Name,
	}})
	if len(tc.Arguments) > 0 {
		s.pending = append(s.pending, llm.Token{ToolCall: &llm.ToolCallEvent{
			Kind: llm.ToolCallDelta, Index: i, ArgumentsDelta: string(tc.Arguments),
		}})
	}
	end := tc
	s.pending = append(s.pending, llm.Token{ToolCall: &llm.ToolCallEvent{
		Kind: llm.ToolCallEnd, Index: i, ID: tc.ID, Name: tc.Name, Call: &end,
	}})
}

// addUsage folds a chunk's usage into the turn's: Gemini reports it
// cumulatively, and a count a chunk omits keeps its last value, so no
// count goes down.
func (s *toolStream) addUsage(u llm.Usage) {
	s.usage.PromptTokens = max(s.usage.PromptTokens, u.PromptTokens)
	s.usage.CompletionTokens = max(s.usage.CompletionTokens, u.CompletionTokens)
	s.usage.TotalTokens = max(s.usage.TotalTokens, u.TotalTokens)
}

// Close releases the underlying response body.
func (s *toolStream) Close() error {
	s.done = true
	s.pending = nil
	return s.body.Close()
}
