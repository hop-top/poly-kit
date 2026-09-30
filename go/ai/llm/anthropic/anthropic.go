// Package anthropic adapts the Anthropic Messages API for the llm
// package. It registers the "anthropic" URI scheme and implements
// [llm.Completer], [llm.Streamer], and [llm.ToolCaller].
package anthropic

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"

	"github.com/anthropics/anthropic-sdk-go"
	"github.com/anthropics/anthropic-sdk-go/option"
	"github.com/anthropics/anthropic-sdk-go/packages/param"
	"github.com/anthropics/anthropic-sdk-go/packages/ssestream"

	"hop.top/kit/go/ai/llm"
	llmerrors "hop.top/kit/go/ai/llm/errors"
)

const (
	defaultMaxTokens = 1024
	scheme           = "anthropic"
)

// Adapter wraps an anthropic-sdk-go client.
type Adapter struct {
	client anthropic.Client
	model  string
}

// compile-time interface checks
var (
	_ llm.Provider   = (*Adapter)(nil)
	_ llm.Completer  = (*Adapter)(nil)
	_ llm.Streamer   = (*Adapter)(nil)
	_ llm.ToolCaller = (*Adapter)(nil)
)

// New creates an Adapter from a resolved config.
func New(cfg llm.ResolvedConfig) (llm.Provider, error) {
	if cfg.Provider.APIKey == "" {
		return nil, fmt.Errorf("anthropic: API key required")
	}

	// The key comes from kit's own resolution, so the SDK's credential
	// autoload (auth profiles, env federation) stays off: a host profile
	// would otherwise supply the base URL and workspace header, and log a
	// shadow warning. ANTHROPIC_BASE_URL, ANTHROPIC_AUTH_TOKEN and
	// ANTHROPIC_WORKSPACE_ID keep their fallback role; the workspace ID
	// is what keys not scoped to a workspace must send.
	opts := []option.RequestOption{option.WithoutEnvironmentDefaults()}
	if v, ok := os.LookupEnv("ANTHROPIC_BASE_URL"); ok {
		opts = append(opts, option.WithBaseURL(v))
	}
	if v, ok := os.LookupEnv("ANTHROPIC_AUTH_TOKEN"); ok {
		opts = append(opts, option.WithAuthToken(v))
	}
	if v := os.Getenv("ANTHROPIC_WORKSPACE_ID"); v != "" {
		opts = append(opts, option.WithHeader("anthropic-workspace-id", v))
	}
	opts = append(opts, option.WithAPIKey(cfg.Provider.APIKey))
	if cfg.Provider.BaseURL != "" {
		opts = append(opts, option.WithBaseURL(cfg.Provider.BaseURL))
	}

	return &Adapter{
		client: anthropic.NewClient(opts...),
		model:  cfg.Provider.Model,
	}, nil
}

// Close is a no-op; the HTTP client does not need explicit teardown.
func (a *Adapter) Close() error { return nil }

func (a *Adapter) effectiveModel(req llm.Request) string {
	if req.Model != "" {
		return req.Model
	}
	return a.model
}

// Complete sends a single completion request and returns the
// aggregated response.
func (a *Adapter) Complete(
	ctx context.Context, req llm.Request,
) (llm.Response, error) {
	params, err := a.buildParams(ctx, req)
	if err != nil {
		return llm.Response{}, err
	}
	msg, err := a.client.Messages.New(ctx, params)
	if err != nil {
		return llm.Response{}, mapError(err, a.effectiveModel(req))
	}
	return responseFromMessage(msg), nil
}

// Stream opens a streaming completion and returns a TokenIterator.
func (a *Adapter) Stream(
	ctx context.Context, req llm.Request,
) (llm.TokenIterator, error) {
	params, err := a.buildParams(ctx, req)
	if err != nil {
		return nil, err
	}
	stream := a.client.Messages.NewStreaming(ctx, params)
	// The SDK returns a stream immediately; check for init error.
	if err := stream.Err(); err != nil {
		_ = stream.Close()
		return nil, mapError(err, a.effectiveModel(req))
	}
	return &streamIter{stream: stream}, nil
}

// CallWithTools sends a tool-use request and maps the response back
// to [llm.ToolResponse].
func (a *Adapter) CallWithTools(
	ctx context.Context, req llm.Request, tools []llm.ToolDef,
) (llm.ToolResponse, error) {
	params, err := a.buildParams(ctx, req)
	if err != nil {
		return llm.ToolResponse{}, err
	}
	params.Tools = mapTools(tools)
	msg, err := a.client.Messages.New(ctx, params)
	if err != nil {
		return llm.ToolResponse{}, mapError(err, a.effectiveModel(req))
	}
	return toolResponseFromMessage(msg), nil
}

// ---------------------------------------------------------------------------
// Request building
// ---------------------------------------------------------------------------

func (a *Adapter) buildParams(
	ctx context.Context, req llm.Request,
) (anthropic.MessageNewParams, error) {
	model := a.model
	if req.Model != "" {
		model = req.Model
	}

	maxTokens := int64(req.MaxTokens)
	if maxTokens <= 0 {
		maxTokens = defaultMaxTokens
	}

	system, msgs, err := extractSystem(ctx, req.Messages)
	if err != nil {
		return anthropic.MessageNewParams{}, err
	}

	p := anthropic.MessageNewParams{
		Model:     anthropic.Model(model),
		MaxTokens: maxTokens,
		Messages:  msgs,
	}

	if system != "" {
		p.System = []anthropic.TextBlockParam{
			{Text: system},
		}
	}

	if req.Temperature != nil {
		p.Temperature = param.NewOpt(*req.Temperature)
	}

	if len(req.StopSequences) > 0 {
		p.StopSequences = req.StopSequences
	}

	return p, nil
}

// extractSystem separates any leading system message from the
// conversation. Anthropic requires system as a top-level field.
func extractSystem(
	ctx context.Context, msgs []llm.Message,
) (string, []anthropic.MessageParam, error) {
	var system string
	out := make([]anthropic.MessageParam, 0, len(msgs))

	for i, m := range msgs {
		if m.Role == "system" {
			system = m.Content
			continue
		}

		if err := checkLinkageRole(m); err != nil {
			return "", nil, err
		}

		if m.Role == "tool" {
			block, err := toolResultBlock(m)
			if err != nil {
				return "", nil, err
			}
			// Consecutive results answer one assistant turn and belong
			// in a single user message.
			if i > 0 && msgs[i-1].Role == "tool" {
				last := &out[len(out)-1]
				last.Content = append(last.Content, block)
				continue
			}
			out = append(out, anthropic.MessageParam{
				Role:    anthropic.MessageParamRoleUser,
				Content: []anthropic.ContentBlockParamUnion{block},
			})
			continue
		}

		if len(m.ToolCalls) > 0 {
			var blocks []anthropic.ContentBlockParamUnion
			var err error
			if len(m.Parts) > 0 {
				blocks, err = mapAnthropicParts(ctx, m.Parts)
				if err != nil {
					return "", nil, err
				}
			} else if m.Content != "" {
				blocks = append(blocks, anthropic.NewTextBlock(m.Content))
			}
			uses, err := toolUseBlocks(m.ToolCalls)
			if err != nil {
				return "", nil, err
			}
			out = append(out, anthropic.MessageParam{
				Role:    anthropic.MessageParamRoleAssistant,
				Content: append(blocks, uses...),
			})
			continue
		}

		if len(m.Parts) == 0 {
			out = append(out, anthropic.MessageParam{
				Role:    anthropic.MessageParamRole(m.Role),
				Content: []anthropic.ContentBlockParamUnion{anthropic.NewTextBlock(m.Content)},
			})
			continue
		}

		// Multimodal path.
		blocks, err := mapAnthropicParts(ctx, m.Parts)
		if err != nil {
			return "", nil, err
		}
		out = append(out, anthropic.MessageParam{
			Role:    anthropic.MessageParamRole(m.Role),
			Content: blocks,
		})
	}

	return system, out, nil
}

// checkLinkageRole rejects tool-call linkage on a role that cannot carry
// it: ToolCalls belong to assistant turns, ToolCallID to tool results.
func checkLinkageRole(m llm.Message) error {
	if len(m.ToolCalls) > 0 && m.Role != "assistant" {
		return fmt.Errorf("anthropic: ToolCalls are valid only on role assistant, got %q", m.Role)
	}
	if m.ToolCallID != "" && m.Role != "tool" {
		return fmt.Errorf("anthropic: ToolCallID is valid only on role tool, got %q", m.Role)
	}
	return nil
}

// toolResultBlock maps a role "tool" message to a tool_result block
// linked to its tool_use by tool_use_id.
func toolResultBlock(m llm.Message) (anthropic.ContentBlockParamUnion, error) {
	if m.ToolCallID == "" {
		return anthropic.ContentBlockParamUnion{}, fmt.Errorf("anthropic: tool result needs ToolCallID")
	}
	if len(m.Parts) > 0 {
		return anthropic.ContentBlockParamUnion{}, fmt.Errorf(
			"anthropic: tool result %q carries only Content", m.ToolCallID,
		)
	}
	res := anthropic.ToolResultBlockParam{ToolUseID: m.ToolCallID}
	// The API rejects empty text blocks, so an empty result sends none.
	if m.Content != "" {
		res.Content = []anthropic.ToolResultBlockParamContentUnion{
			{OfText: &anthropic.TextBlockParam{Text: m.Content}},
		}
	}
	return anthropic.ContentBlockParamUnion{OfToolResult: &res}, nil
}

// toolUseBlocks maps an assistant turn's calls to tool_use blocks.
func toolUseBlocks(calls []llm.ToolCall) ([]anthropic.ContentBlockParamUnion, error) {
	out := make([]anthropic.ContentBlockParamUnion, 0, len(calls))
	for _, tc := range calls {
		if tc.ID == "" {
			return nil, fmt.Errorf("anthropic: tool call %q needs an ID", tc.Name)
		}
		input := json.RawMessage(`{}`)
		if len(tc.Arguments) > 0 {
			if !json.Valid(tc.Arguments) {
				return nil, fmt.Errorf("anthropic: tool call %q arguments are not valid JSON", tc.ID)
			}
			input = tc.Arguments
		}
		out = append(out, anthropic.NewToolUseBlock(tc.ID, input, tc.Name))
	}
	return out, nil
}

// mapAnthropicParts converts llm.ContentPart slice to Anthropic content blocks.
func mapAnthropicParts(
	ctx context.Context,
	parts []llm.ContentPart,
) ([]anthropic.ContentBlockParamUnion, error) {
	out := make([]anthropic.ContentBlockParamUnion, 0, len(parts))
	for _, p := range parts {
		switch p.Type {
		case llm.PartTypeText:
			out = append(out, anthropic.NewTextBlock(p.Text))

		case llm.PartTypeImage:
			mime := p.MimeType
			if mime == "" && p.Source != nil {
				mime = p.Source.MimeType()
			}
			if mime == "" {
				return nil, llmerrors.NewInvalidFormat("", "anthropic", nil)
			}

			data, err := anthropicReadBase64(ctx, p.Source)
			if err != nil {
				return nil, fmt.Errorf("anthropic: read media: %w", err)
			}

			if mime == "application/pdf" {
				out = append(out, anthropic.NewDocumentBlock(
					anthropic.Base64PDFSourceParam{Data: data},
				))
			} else {
				out = append(out, anthropic.NewImageBlockBase64(mime, data))
			}

		default:
			return nil, llmerrors.NewUnsupportedModality(string(p.Type), "anthropic", nil)
		}
	}
	return out, nil
}

// anthropicReadBase64 reads all bytes from src and returns base64-encoded string.
func anthropicReadBase64(ctx context.Context, src llm.MediaSource) (string, error) {
	if src == nil {
		return "", fmt.Errorf("anthropic: nil media source")
	}
	rc, err := src.Reader(ctx)
	if err != nil {
		return "", err
	}
	defer rc.Close()
	raw, err := io.ReadAll(rc)
	if err != nil {
		return "", err
	}
	return base64.StdEncoding.EncodeToString(raw), nil
}

// mapTools converts llm.ToolDef slice to Anthropic tool params.
func mapTools(defs []llm.ToolDef) []anthropic.ToolUnionParam {
	out := make([]anthropic.ToolUnionParam, 0, len(defs))
	for _, d := range defs {
		var schema anthropic.ToolInputSchemaParam
		if len(d.Parameters) > 0 {
			_ = json.Unmarshal(d.Parameters, &schema)
		}
		out = append(out, anthropic.ToolUnionParam{
			OfTool: &anthropic.ToolParam{
				Name:        d.Name,
				Description: param.NewOpt(d.Description),
				InputSchema: schema,
			},
		})
	}
	return out
}

// ---------------------------------------------------------------------------
// Response mapping
// ---------------------------------------------------------------------------

func responseFromMessage(msg *anthropic.Message) llm.Response {
	var text string
	for _, block := range msg.Content {
		if block.Type == "text" {
			text += block.Text
		}
	}
	return llm.Response{
		Content:      text,
		Role:         "assistant",
		FinishReason: string(msg.StopReason),
		Usage: llm.Usage{
			PromptTokens:     int(msg.Usage.InputTokens),
			CompletionTokens: int(msg.Usage.OutputTokens),
			TotalTokens:      int(msg.Usage.InputTokens + msg.Usage.OutputTokens),
		},
	}
}

func toolResponseFromMessage(msg *anthropic.Message) llm.ToolResponse {
	var text string
	var calls []llm.ToolCall

	for _, block := range msg.Content {
		switch block.Type {
		case "text":
			text += block.Text
		case "tool_use":
			calls = append(calls, llm.ToolCall{
				ID:        block.ID,
				Name:      block.Name,
				Arguments: block.Input,
			})
		}
	}

	return llm.ToolResponse{
		Content:   text,
		ToolCalls: calls,
	}
}

// ---------------------------------------------------------------------------
// Streaming iterator
// ---------------------------------------------------------------------------

type streamIter struct {
	stream *ssestream.Stream[anthropic.MessageStreamEventUnion]
	done   bool
}

func (s *streamIter) Next() (llm.Token, error) {
	for {
		if s.done {
			return llm.Token{}, io.EOF
		}
		if !s.stream.Next() {
			s.done = true
			if err := s.stream.Err(); err != nil {
				return llm.Token{}, mapError(err, "")
			}
			return llm.Token{Done: true}, nil
		}
		evt := s.stream.Current()
		switch evt.Type {
		case "content_block_delta":
			if evt.Delta.Type == "text_delta" {
				return llm.Token{Content: evt.Delta.Text}, nil
			}
		case "message_stop":
			s.done = true
			return llm.Token{Done: true}, nil
		}
		// skip other event types
	}
}

func (s *streamIter) Close() error {
	return s.stream.Close()
}

// ---------------------------------------------------------------------------
// Error mapping
// ---------------------------------------------------------------------------

func mapError(err error, model string) error {
	var apiErr *anthropic.Error
	if !errors.As(err, &apiErr) {
		return err
	}

	switch apiErr.StatusCode {
	case 401, 403:
		return llmerrors.NewAuth(scheme, err)
	case 429:
		return llmerrors.NewRateLimit(scheme, 0)
	case 404:
		return llmerrors.NewModel(model, scheme)
	default:
		if apiErr.StatusCode >= 500 {
			return llmerrors.NewHTTPStatusError(
				apiErr.StatusCode, fmt.Sprintf("anthropic: %s", err),
			)
		}
	}
	return err
}

// ---------------------------------------------------------------------------
// Registration
// ---------------------------------------------------------------------------

func init() {
	llm.Register(scheme, New)
}
