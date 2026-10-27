package llm

import (
	"encoding/json"
	"sort"
	"time"
)

// Bus topic constants for llm lifecycle events.
//
// All topics use the kit 4-segment convention
// [Source].[Category].[Object].[Action]. These vars read from
// [DefaultTopics] so there is a single source of truth; adopters that
// need to override should use [WithTopics] or [WithTopicPrefix].
var (
	TopicRequestStart = DefaultTopics.RequestStart
	TopicRequestEnd   = DefaultTopics.RequestEnd
	TopicRequestError = DefaultTopics.RequestError
	TopicFallback     = DefaultTopics.Fallback
	TopicRoute        = DefaultTopics.Route
	TopicEvaResult    = DefaultTopics.EvaResult
)

// RequestStartPayload is published before each LLM call. It describes
// the request without its content: no message text, tool names or
// arguments, extension values or media, so a bus that forwards events
// (to a network peer, a log file, a database) carries no prompt.
//
// Request holds the content only when the client was built with
// [WithRequestContent], as that option's transform returned it.
// [OnRequest] hooks run in process and always get the full request.
type RequestStartPayload struct {
	// Op is the call: "complete", "stream", "tool_call",
	// "stream_tools", or a media call ("image_gen", "transcribe",
	// "synthesize", "video_analyze", "video_gen").
	Op string `json:"op"`
	// Model is [Request.Model]; empty means the adapter's own.
	Model string `json:"model,omitempty"`
	// Messages counts the messages, Roles them by role and Parts the
	// content parts of multimodal messages by type.
	Messages int            `json:"messages"`
	Roles    map[string]int `json:"roles,omitempty"`
	Parts    map[string]int `json:"parts,omitempty"`
	// ContentBytes is the UTF-8 length of the messages' text: Content,
	// text parts, tool call arguments and thinking text. Media is not
	// counted.
	ContentBytes int `json:"content_bytes"`
	// Tools counts the tools offered (tool_call, stream_tools).
	Tools          int  `json:"tools"`
	MaxTokens      int  `json:"max_tokens"`
	HasTemperature bool `json:"has_temperature"`
	Thinking       bool `json:"thinking"`
	StopSequences  int  `json:"stop_sequences"`
	// ExtensionKeys lists [Request.Extensions] keys, sorted; never
	// their values.
	ExtensionKeys []string `json:"extension_keys,omitempty"`

	// Request is the content, set only by [WithRequestContent].
	Request *Request `json:"request,omitempty"`
}

// RequestEndPayload is published after a successful call other than a
// stream; a stream publishes none. Like [RequestStartPayload]
// it carries no content: Response is set only by [WithResponseContent].
// [OnResponse] hooks run in process and always get the full response.
type RequestEndPayload struct {
	// Op is the call, as in [RequestStartPayload].
	Op       string        `json:"op"`
	Duration time.Duration `json:"-"`
	// FinishReason is the provider's stop reason.
	FinishReason string `json:"finish_reason,omitempty"`
	Usage        Usage  `json:"-"`
	// ContentBytes is the UTF-8 length of the response's text: Content,
	// text output parts, tool call arguments and thinking text.
	ContentBytes int `json:"content_bytes"`
	// ToolCalls counts the calls the model made (tool_call).
	ToolCalls int `json:"tool_calls"`

	// Response is the content, set only by [WithResponseContent].
	Response *Response `json:"response,omitempty"`
}

// MarshalJSON adds duration_ms and spells usage as the bus contract
// does (prompt_tokens, completion_tokens, cache_read_tokens,
// cache_creation_tokens, total_tokens).
func (p RequestEndPayload) MarshalJSON() ([]byte, error) {
	type plain RequestEndPayload
	return json.Marshal(struct {
		plain
		DurationMS int64     `json:"duration_ms"`
		Usage      usageJSON `json:"usage"`
	}{plain(p), p.Duration.Milliseconds(), usageJSON{
		PromptTokens:        p.Usage.PromptTokens,
		CompletionTokens:    p.Usage.CompletionTokens,
		CacheReadTokens:     p.Usage.CacheReadTokens,
		CacheCreationTokens: p.Usage.CacheCreationTokens,
		TotalTokens:         p.Usage.TotalTokens,
	}})
}

type usageJSON struct {
	PromptTokens        int `json:"prompt_tokens"`
	CompletionTokens    int `json:"completion_tokens"`
	CacheReadTokens     int `json:"cache_read_tokens"`
	CacheCreationTokens int `json:"cache_creation_tokens"`
	TotalTokens         int `json:"total_tokens"`
}

// requestStart describes req for the bus; tools counts the tools
// offered.
func requestStart(op string, req Request, tools int) RequestStartPayload {
	p := RequestStartPayload{
		Op:             op,
		Model:          req.Model,
		Messages:       len(req.Messages),
		Tools:          tools,
		MaxTokens:      req.MaxTokens,
		HasTemperature: req.Temperature != nil,
		Thinking:       req.Thinking != nil,
		StopSequences:  len(req.StopSequences),
	}
	for _, m := range req.Messages {
		if p.Roles == nil {
			p.Roles = map[string]int{}
		}
		p.Roles[m.Role]++
		p.ContentBytes += len(m.Content) + toolCallBytes(m.ToolCalls) + thinkingBytes(m.Thinking)
		for _, part := range m.Parts {
			if p.Parts == nil {
				p.Parts = map[string]int{}
			}
			p.Parts[string(part.Type)]++
			if part.Type == PartTypeText {
				p.ContentBytes += len(part.Text)
			}
		}
	}
	for k := range req.Extensions {
		p.ExtensionKeys = append(p.ExtensionKeys, k)
	}
	sort.Strings(p.ExtensionKeys)
	return p
}

// requestEnd describes resp for the bus.
func requestEnd(op string, resp Response, calls []ToolCall, d time.Duration) RequestEndPayload {
	p := RequestEndPayload{
		Op:           op,
		Duration:     d,
		FinishReason: resp.FinishReason,
		Usage:        resp.Usage,
		ContentBytes: len(resp.Content) + toolCallBytes(calls) + thinkingBytes(resp.Thinking),
		ToolCalls:    len(calls),
	}
	for _, part := range resp.OutputParts {
		if part.Type == PartTypeText {
			p.ContentBytes += len(part.Text)
		}
	}
	return p
}

func toolCallBytes(calls []ToolCall) int {
	n := 0
	for _, c := range calls {
		n += len(c.Arguments)
	}
	return n
}

func thinkingBytes(tbs []ThinkingBlock) int {
	n := 0
	for _, tb := range tbs {
		n += len(tb.Thinking)
	}
	return n
}

// RequestErrorPayload is published when an LLM call fails terminally.
type RequestErrorPayload struct {
	Err        error  `json:"-"`
	ErrMessage string `json:"error"`
}

// FallbackPayload is published when falling back to the next provider.
type FallbackPayload struct {
	From       int
	To         int
	Err        error  `json:"-"`
	ErrMessage string `json:"error"`
}

// RoutePayload is published after a routing decision.
type RoutePayload struct {
	Router string
	Score  float64
	Model  string
}

// EvaResultPayload is published after an eva contract evaluation.
type EvaResultPayload struct {
	Contract   string
	Passed     bool
	Violations []string
}
