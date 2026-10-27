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
	"net/http"
	"os"
	"strings"

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
	defaultBaseURL   = "https://api.anthropic.com"
	// minThinkingBudget is the API's least thinking budget.
	minThinkingBudget = 1024
)

// Adapter wraps an anthropic-sdk-go client.
type Adapter struct {
	client anthropic.Client
	model  string
	// secrets are the credential values sent; scrub removes them from
	// every error message.
	secrets []string
	// toolNames is the tool_names param: how tool names the API
	// rejects are handled.
	toolNames llm.ToolNameMode
}

// compile-time interface checks
var (
	_ llm.Provider     = (*Adapter)(nil)
	_ llm.Completer    = (*Adapter)(nil)
	_ llm.Streamer     = (*Adapter)(nil)
	_ llm.ToolCaller   = (*Adapter)(nil)
	_ llm.ToolStreamer = (*Adapter)(nil)
)

// New creates an Adapter from a resolved config.
func New(cfg llm.ResolvedConfig) (llm.Provider, error) {
	if strings.TrimSpace(cfg.Provider.APIKey) == "" {
		return nil, fmt.Errorf("anthropic: API key required")
	}

	// The key comes from kit's own resolution, so the SDK's credential
	// autoload (auth profiles, env federation) stays off: a host profile
	// would otherwise supply the base URL and workspace header, and log a
	// shadow warning. ANTHROPIC_BASE_URL, ANTHROPIC_AUTH_TOKEN and
	// ANTHROPIC_WORKSPACE_ID keep their fallback role; the workspace ID
	// is what keys not scoped to a workspace must send. A blank key or
	// token is no credential: the key is required, the token not sent.
	lim, err := limitsFrom(cfg.Provider.Params)
	if err != nil {
		return nil, err
	}
	toolNames, err := llm.ToolNameModeOf(cfg.Provider.Params)
	if err != nil {
		return nil, fmt.Errorf("anthropic: %w", err)
	}
	opts := []option.RequestOption{
		option.WithoutEnvironmentDefaults(),
		option.WithMiddleware(boundRetryAfter),
		option.WithMiddleware(lim.middleware),
		// Redirects are not followed: a credential never moves to a
		// host the server names.
		option.WithHTTPClient(&http.Client{CheckRedirect: refuseRedirect}),
	}
	// A hermetic adapter reads no environment variable.
	env := os.LookupEnv
	if cfg.Hermetic {
		env = func(string) (string, bool) { return "", false }
	}
	envBase, hasEnvBase := env("ANTHROPIC_BASE_URL")
	if hasEnvBase {
		if err := checkBaseURL("ANTHROPIC_BASE_URL", envBase); err != nil {
			return nil, err
		}
		opts = append(opts, option.WithBaseURL(envBase))
	}
	if cfg.Provider.BaseURL != "" {
		if err := checkBaseURL("base URL", cfg.Provider.BaseURL); err != nil {
			return nil, err
		}
		opts = append(opts, option.WithBaseURL(cfg.Provider.BaseURL))
	}
	base := cfg.Provider.BaseURL
	if base == "" && hasEnvBase {
		base = envBase
	}
	if base == "" {
		base = defaultBaseURL
	}
	authToken, _ := env("ANTHROPIC_AUTH_TOKEN")
	workspace, _ := env("ANTHROPIC_WORKSPACE_ID")
	creds, err := credentials(cfg.Provider.APIKey, base, envBase, authToken, workspace)
	if err != nil {
		return nil, err
	}
	opts = append(opts, creds...)

	return &Adapter{
		client:    anthropic.NewClient(opts...),
		model:     cfg.Provider.Model,
		secrets:   secretsOf(cfg.Provider.APIKey, authToken),
		toolNames: toolNames,
	}, nil
}

// credentials returns every credential option for base. The key is
// the caller's; ANTHROPIC_AUTH_TOKEN and the workspace ID come from the
// environment and go only where [llm.CredentialsTrusted] allows. None
// goes over plain http to a remote host or to a malformed endpoint.
// Every credential is added here, behind that one gate.
//
// authToken and workspace are ANTHROPIC_AUTH_TOKEN and
// ANTHROPIC_WORKSPACE_ID as read (empty when hermetic). With neither,
// [llm.CredentialsTrusted], which reads llm.yaml and LLM_BASE_URL, is
// not asked.
func credentials(key, base, envBase, authToken, workspace string) ([]option.RequestOption, error) {
	if err := llm.CheckKeyEndpoint(scheme, key, base); err != nil {
		return nil, err
	}
	opts := []option.RequestOption{option.WithAPIKey(key)}
	var env []option.RequestOption
	if strings.TrimSpace(authToken) != "" {
		if err := llm.CheckKeyEndpoint(scheme, authToken, base); err != nil {
			return nil, err
		}
		env = append(env, option.WithAuthToken(authToken))
	}
	if workspace != "" {
		env = append(env, option.WithHeader("anthropic-workspace-id", workspace))
	}
	if len(env) > 0 && llm.CredentialsTrusted(scheme, base, envBase) {
		opts = append(opts, env...)
	}
	return opts, nil
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
	names, err := a.toolNameMap(nil)
	if err != nil {
		return llm.Response{}, err
	}
	params, err := a.buildParams(ctx, req, names)
	if err != nil {
		return llm.Response{}, err
	}
	if params.ToolChoice, err = toolChoice(req.ToolChoice, nil, names); err != nil {
		return llm.Response{}, err
	}
	msg, err := a.client.Messages.New(ctx, params)
	if err != nil {
		return llm.Response{}, a.mapError(err, a.effectiveModel(req))
	}
	return responseFromMessage(msg), nil
}

// Stream opens a streaming completion and returns a TokenIterator.
func (a *Adapter) Stream(
	ctx context.Context, req llm.Request,
) (llm.TokenIterator, error) {
	names, err := a.toolNameMap(nil)
	if err != nil {
		return nil, err
	}
	params, err := a.buildParams(ctx, req, names)
	if err != nil {
		return nil, err
	}
	if params.ToolChoice, err = toolChoice(req.ToolChoice, nil, names); err != nil {
		return nil, err
	}
	return a.openStream(ctx, params, req, names)
}

func (a *Adapter) openStream(
	ctx context.Context, params anthropic.MessageNewParams, req llm.Request, names *llm.ToolNames,
) (*streamIter, error) {
	stream := a.client.Messages.NewStreaming(ctx, params)
	// The SDK returns a stream immediately; check for init error.
	if err := stream.Err(); err != nil {
		_ = stream.Close()
		return nil, a.mapError(err, a.effectiveModel(req))
	}
	return &streamIter{
		stream: stream, a: a, model: a.effectiveModel(req), thinking: req.Thinking != nil, names: names,
		events: req.StreamEvents, calls: map[int64]int{},
	}, nil
}

// StreamWithTools streams a tool-use request: text deltas as tokens,
// then the calls, stop reason and usage from Outcome.
func (a *Adapter) StreamWithTools(
	ctx context.Context, req llm.Request, tools []llm.ToolDef,
) (llm.ToolStream, error) {
	names, err := a.toolNameMap(tools)
	if err != nil {
		return nil, err
	}
	params, err := a.buildParams(ctx, req, names)
	if err != nil {
		return nil, err
	}
	if params.Tools, err = mapTools(names.Defs(tools)); err != nil {
		return nil, err
	}
	if params.ToolChoice, err = toolChoice(req.ToolChoice, tools, names); err != nil {
		return nil, err
	}
	return a.openStream(ctx, params, req, names)
}

// CallWithTools sends a tool-use request and maps the response back
// to [llm.ToolResponse].
func (a *Adapter) CallWithTools(
	ctx context.Context, req llm.Request, tools []llm.ToolDef,
) (llm.ToolResponse, error) {
	names, err := a.toolNameMap(tools)
	if err != nil {
		return llm.ToolResponse{}, err
	}
	params, err := a.buildParams(ctx, req, names)
	if err != nil {
		return llm.ToolResponse{}, err
	}
	if params.Tools, err = mapTools(names.Defs(tools)); err != nil {
		return llm.ToolResponse{}, err
	}
	if params.ToolChoice, err = toolChoice(req.ToolChoice, tools, names); err != nil {
		return llm.ToolResponse{}, err
	}
	msg, err := a.client.Messages.New(ctx, params)
	if err != nil {
		return llm.ToolResponse{}, a.mapError(err, a.effectiveModel(req))
	}
	return toolResponseFromMessage(msg, names), nil
}

// toolNameMap checks tools against the API's name rule and maps them,
// per the tool_names param; see [llm.NewToolNames].
func (a *Adapter) toolNameMap(tools []llm.ToolDef) (*llm.ToolNames, error) {
	return llm.NewToolNames(llm.AnthropicToolNames, a.toolNames, tools)
}

// ---------------------------------------------------------------------------
// Request building
// ---------------------------------------------------------------------------

func (a *Adapter) buildParams(
	ctx context.Context, req llm.Request, names *llm.ToolNames,
) (anthropic.MessageNewParams, error) {
	model := a.model
	if req.Model != "" {
		model = req.Model
	}

	maxTokens := int64(req.MaxTokens)
	if maxTokens <= 0 {
		maxTokens = defaultMaxTokens
	}

	system, msgs, err := extractSystem(ctx, req.Messages, names)
	if err != nil {
		return anthropic.MessageNewParams{}, err
	}

	p := anthropic.MessageNewParams{
		Model:     anthropic.Model(model),
		MaxTokens: maxTokens,
		Messages:  msgs,
		System:    system,
	}

	if t := req.Thinking; t != nil {
		if t.BudgetTokens < minThinkingBudget {
			return anthropic.MessageNewParams{}, fmt.Errorf(
				"anthropic: thinking budget_tokens must be at least %d, got %d", minThinkingBudget, t.BudgetTokens)
		}
		if int64(t.BudgetTokens) >= maxTokens {
			return anthropic.MessageNewParams{}, fmt.Errorf(
				"anthropic: thinking budget_tokens (%d) must be less than max_tokens (%d)", t.BudgetTokens, maxTokens)
		}
		p.Thinking = anthropic.ThinkingConfigParamOfEnabled(int64(t.BudgetTokens))
	}

	if req.Temperature != nil {
		p.Temperature = param.NewOpt(*req.Temperature)
	}

	if len(req.StopSequences) > 0 {
		p.StopSequences = req.StopSequences
	}

	return p, nil
}

// systemSeparator joins consecutive system messages into one text.
const systemSeparator = "\n\n"

// extractSystem separates the system messages from the conversation:
// Anthropic takes system as a top-level field. See [systemBlocks].
func extractSystem(
	ctx context.Context, msgs []llm.Message, names *llm.ToolNames,
) ([]anthropic.TextBlockParam, []anthropic.MessageParam, error) {
	system, err := systemBlocks(msgs)
	if err != nil {
		return nil, nil, err
	}
	out := make([]anthropic.MessageParam, 0, len(msgs))

	for i, m := range msgs {
		if m.Role == "system" {
			continue
		}

		if err := checkLinkageRole(m); err != nil {
			return nil, nil, err
		}

		if m.Role == "tool" {
			block, err := toolResultBlock(m)
			if err != nil {
				return nil, nil, err
			}
			if err := setCache(&block, m.CacheControl); err != nil {
				return nil, nil, err
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

		var blocks []anthropic.ContentBlockParamUnion
		var err error
		switch {
		case len(m.Parts) > 0:
			blocks, err = mapAnthropicParts(ctx, m.Parts)
			if err != nil {
				return nil, nil, err
			}
		case len(m.ToolCalls) == 0 || m.Content != "":
			blocks = append(blocks, anthropic.NewTextBlock(m.Content))
		}
		uses, err := toolUseBlocks(m.ToolCalls, names)
		if err != nil {
			return nil, nil, err
		}
		blocks = append(blocks, uses...)
		if len(blocks) > 0 {
			if err := setCache(&blocks[len(blocks)-1], m.CacheControl); err != nil {
				return nil, nil, err
			}
		}
		// Thinking blocks lead the turn, unchanged and in order.
		blocks = append(replayThinking(m.Thinking), blocks...)
		out = append(out, anthropic.MessageParam{
			Role:    anthropic.MessageParamRole(m.Role),
			Content: blocks,
		})
	}

	return system, out, nil
}

// systemBlocks maps every system message, wherever it sits, to the
// system field: their Content joined in order with a blank line
// ([systemSeparator]), empty ones skipped. A CacheControl on a system
// message ends a text block there and carries that breakpoint, so each
// breakpoint caches exactly the text up to it; the next system
// message starts a new block. No system text yields nil.
func systemBlocks(msgs []llm.Message) ([]anthropic.TextBlockParam, error) {
	var out []anthropic.TextBlockParam
	var text strings.Builder
	for _, m := range msgs {
		if m.Role != "system" {
			continue
		}
		if m.Content != "" {
			if text.Len() > 0 {
				text.WriteString(systemSeparator)
			}
			text.WriteString(m.Content)
		}
		if m.CacheControl == nil || text.Len() == 0 {
			continue
		}
		cc, _, err := cacheParam(m.CacheControl)
		if err != nil {
			return nil, err
		}
		out = append(out, anthropic.TextBlockParam{Text: text.String(), CacheControl: cc})
		text.Reset()
	}
	if text.Len() > 0 {
		out = append(out, anthropic.TextBlockParam{Text: text.String()})
	}
	return out, nil
}

// replayThinking maps thinking blocks back to request blocks.
func replayThinking(tbs []llm.ThinkingBlock) []anthropic.ContentBlockParamUnion {
	out := make([]anthropic.ContentBlockParamUnion, 0, len(tbs))
	for _, tb := range tbs {
		if tb.Redacted != "" {
			out = append(out, anthropic.NewRedactedThinkingBlock(tb.Redacted))
			continue
		}
		out = append(out, anthropic.NewThinkingBlock(tb.Signature, tb.Thinking))
	}
	return out
}

// cacheParam maps a breakpoint; ok is false for none.
func cacheParam(cc *llm.CacheControl) (anthropic.CacheControlEphemeralParam, bool, error) {
	if cc == nil {
		return anthropic.CacheControlEphemeralParam{}, false, nil
	}
	p := anthropic.NewCacheControlEphemeralParam()
	switch cc.TTL {
	case "":
	case "5m", "1h":
		p.TTL = anthropic.CacheControlEphemeralTTL(cc.TTL)
	default:
		return p, false, fmt.Errorf("anthropic: cache_control ttl must be \"5m\" or \"1h\", got %q", cc.TTL)
	}
	return p, true, nil
}

// setCache puts cc on block, when block can carry one.
func setCache(block *anthropic.ContentBlockParamUnion, cc *llm.CacheControl) error {
	p, ok, err := cacheParam(cc)
	if err != nil || !ok {
		return err
	}
	if dst := block.GetCacheControl(); dst != nil {
		*dst = p
	}
	return nil
}

// checkLinkageRole rejects tool-call linkage on a role that cannot carry
// it: ToolCalls belong to assistant turns, ToolCallID to tool results.
func checkLinkageRole(m llm.Message) error {
	if len(m.ToolCalls) > 0 && m.Role != "assistant" {
		return fmt.Errorf("anthropic: ToolCalls are valid only on role assistant, got %q", m.Role)
	}
	if len(m.Thinking) > 0 && m.Role != "assistant" {
		return fmt.Errorf("anthropic: Thinking is valid only on role assistant, got %q", m.Role)
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

// toolUseBlocks maps an assistant turn's calls to tool_use blocks,
// each under the name the request sends for it.
func toolUseBlocks(calls []llm.ToolCall, names *llm.ToolNames) ([]anthropic.ContentBlockParamUnion, error) {
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
		out = append(out, anthropic.NewToolUseBlock(tc.ID, input, names.Wire(tc.Name)))
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

			// The API takes a URL source for images and PDFs: send the
			// URL and let the provider fetch it, so no download (and no
			// request to a caller-chosen host) happens here.
			if p.Source != nil && p.Source.URL() != "" {
				u := p.Source.URL()
				if mime == "application/pdf" {
					out = append(out, anthropic.NewDocumentBlock(anthropic.URLPDFSourceParam{URL: u}))
				} else {
					out = append(out, anthropic.NewImageBlock(anthropic.URLImageSourceParam{URL: u}))
				}
				continue
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

// toolChoice maps c, checked against tools, to the request's
// tool_choice; a named tool goes by the name the request sends for it.
// A nil c sends none.
func toolChoice(
	c *llm.ToolChoice, tools []llm.ToolDef, names *llm.ToolNames,
) (anthropic.ToolChoiceUnionParam, error) {
	var out anthropic.ToolChoiceUnionParam
	if err := llm.CheckToolChoice(c, tools); err != nil {
		return out, fmt.Errorf("anthropic: %w", err)
	}
	if c == nil {
		return out, nil
	}
	switch c.Mode {
	case llm.ToolChoiceAuto:
		out.OfAuto = &anthropic.ToolChoiceAutoParam{}
	case llm.ToolChoiceAny:
		out.OfAny = &anthropic.ToolChoiceAnyParam{}
	case llm.ToolChoiceNone:
		out.OfNone = &anthropic.ToolChoiceNoneParam{}
	case llm.ToolChoiceTool:
		out.OfTool = &anthropic.ToolChoiceToolParam{Name: names.Wire(c.Name)}
	}
	return out, nil
}

// mapTools converts llm.ToolDef slice to Anthropic tool params.
func mapTools(defs []llm.ToolDef) ([]anthropic.ToolUnionParam, error) {
	out := make([]anthropic.ToolUnionParam, 0, len(defs))
	for _, d := range defs {
		// The API requires input_schema: a tool without parameters, or
		// with ones that do not decode, takes an empty object schema.
		var schema anthropic.ToolInputSchemaParam
		if len(d.Parameters) == 0 || json.Unmarshal(d.Parameters, &schema) != nil {
			schema = anthropic.ToolInputSchemaParam{Properties: map[string]any{}}
		}
		tp := &anthropic.ToolParam{
			Name:        d.Name,
			Description: param.NewOpt(d.Description),
			InputSchema: schema,
		}
		cc, ok, err := cacheParam(d.CacheControl)
		if err != nil {
			return nil, err
		}
		if ok {
			tp.CacheControl = cc
		}
		out = append(out, anthropic.ToolUnionParam{OfTool: tp})
	}
	return out, nil
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
		Usage:        usageOf(msg.Usage),
		Thinking:     thinkingBlocks(msg),
	}
}

func usageOf(u anthropic.Usage) llm.Usage {
	out := llm.Usage{
		PromptTokens:        int(u.InputTokens),
		CompletionTokens:    int(u.OutputTokens),
		CacheReadTokens:     int(u.CacheReadInputTokens),
		CacheCreationTokens: int(u.CacheCreationInputTokens),
	}
	out.TotalTokens = out.PromptTokens + out.CompletionTokens + out.CacheReadTokens + out.CacheCreationTokens
	return out
}

// toolCallOf maps a tool_use block; the call carries the name the
// caller defined, through names.
func toolCallOf(block anthropic.ContentBlockUnion, names *llm.ToolNames) llm.ToolCall {
	return llm.ToolCall{ID: block.ID, Name: names.Original(block.Name), Arguments: block.Input}
}

// toolResponseFromMessage maps msg; each call carries the name the
// caller defined, through names.
func toolResponseFromMessage(msg *anthropic.Message, names *llm.ToolNames) llm.ToolResponse {
	var text string
	var calls []llm.ToolCall

	for _, block := range msg.Content {
		switch block.Type {
		case "text":
			text += block.Text
		case "tool_use":
			calls = append(calls, toolCallOf(block, names))
		}
	}

	return llm.ToolResponse{
		Content:      text,
		ToolCalls:    calls,
		Thinking:     thinkingBlocks(msg),
		FinishReason: string(msg.StopReason),
		Usage:        usageOf(msg.Usage),
	}
}

// ---------------------------------------------------------------------------
// Streaming iterator
// ---------------------------------------------------------------------------

type streamIter struct {
	stream *ssestream.Stream[anthropic.MessageStreamEventUnion]
	a      *Adapter
	model  string
	done   bool
	// thinking streams thinking deltas as tokens.
	thinking bool
	// msg folds every event, as the SDK's Message.Accumulate does.
	msg     anthropic.Message
	outcome *llm.StreamOutcome
	// names maps returned tool names back to the caller's.
	names *llm.ToolNames
	// events streams tool-call events and usage snapshots
	// ([llm.Request.StreamEvents]).
	events bool
	// calls maps a tool_use block's content index to its call index.
	calls map[int64]int
}

// Outcome is the finished turn, once Next returned Done.
func (s *streamIter) Outcome() (llm.StreamOutcome, bool) {
	if s.outcome == nil {
		return llm.StreamOutcome{}, false
	}
	return *s.outcome, true
}

func (s *streamIter) Next() (llm.Token, error) {
	for {
		if s.done {
			return llm.Token{}, io.EOF
		}
		if !s.stream.Next() {
			s.done = true
			if err := s.stream.Err(); err != nil {
				return llm.Token{}, s.a.mapError(err, s.model)
			}
			// No message_stop: the response is incomplete.
			return llm.Token{}, llmerrors.NewStreamTruncated(scheme)
		}
		evt := s.stream.Current()
		if err := s.msg.Accumulate(evt); err != nil {
			s.done = true
			return llm.Token{}, fmt.Errorf("anthropic: %w", err)
		}
		switch evt.Type {
		case "content_block_delta":
			switch {
			case evt.Delta.Type == "text_delta":
				return llm.Token{Content: evt.Delta.Text}, nil
			case evt.Delta.Type == "thinking_delta" && s.thinking:
				return llm.Token{Thinking: evt.Delta.Thinking}, nil
			}
			if tok, ok := s.event(evt); ok {
				return tok, nil
			}
		case "message_start", "message_delta", "content_block_start", "content_block_stop":
			if tok, ok := s.event(evt); ok {
				return tok, nil
			}
		case "message_stop":
			s.done = true
			out := outcomeFromMessage(&s.msg, s.names)
			s.outcome = &out
			return llm.Token{Done: true}, nil
		}
		// skip other event types
	}
}

func (s *streamIter) Close() error {
	return s.stream.Close()
}

// event maps an accumulated SSE event to a tool-call or usage token,
// when the request asked for them ([llm.Request.StreamEvents]).
func (s *streamIter) event(evt anthropic.MessageStreamEventUnion) (llm.Token, bool) {
	if !s.events {
		return llm.Token{}, false
	}
	switch evt.Type {
	case "message_start", "message_delta":
		u := usageOf(s.msg.Usage)
		return llm.Token{Usage: &u}, true
	case "content_block_start":
		block := s.msg.Content[evt.Index]
		if block.Type != "tool_use" {
			return llm.Token{}, false
		}
		i := len(s.calls)
		s.calls[evt.Index] = i
		return llm.Token{ToolCall: &llm.ToolCallEvent{
			Kind: llm.ToolCallStart, Index: i, ID: block.ID, Name: s.names.Original(block.Name),
		}}, true
	case "content_block_delta":
		i, ok := s.calls[evt.Index]
		if !ok || evt.Delta.Type != "input_json_delta" || evt.Delta.PartialJSON == "" {
			return llm.Token{}, false
		}
		return llm.Token{ToolCall: &llm.ToolCallEvent{
			Kind: llm.ToolCallDelta, Index: i, ArgumentsDelta: evt.Delta.PartialJSON,
		}}, true
	case "content_block_stop":
		i, ok := s.calls[evt.Index]
		if !ok {
			return llm.Token{}, false
		}
		call := toolCallOf(s.msg.Content[evt.Index], s.names)
		return llm.Token{ToolCall: &llm.ToolCallEvent{
			Kind: llm.ToolCallEnd, Index: i, ID: call.ID, Name: call.Name, Call: &call,
		}}, true
	}
	return llm.Token{}, false
}

// ---------------------------------------------------------------------------
// Error mapping
// ---------------------------------------------------------------------------

func mapError(err error, model string) error {
	// Errors quote the request URL; a base URL may carry a credential.
	err = llm.RedactURLError(err)
	var apiErr *anthropic.Error
	if !errors.As(err, &apiErr) {
		return err
	}
	redactRequestURL(apiErr.Request)

	// An SSE error event arrives on a 200 response: classify it by the
	// status its error type stands for (overloaded_error is a 529).
	if apiErr.StatusCode < 400 {
		if st, ok := eventStatus[string(apiErr.Type())]; ok {
			apiErr.StatusCode = st
		}
	}

	switch apiErr.StatusCode {
	case 401:
		return llmerrors.NewAuth(scheme, err)
	case 403:
		return llmerrors.NewPermissionDenied(scheme, err)
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
	llm.Register(scheme, New, llm.Declaration{Endpoints: []string{defaultBaseURL}})
}

// eventStatus maps an API error type to the status it stands for.
var eventStatus = map[string]int{
	"invalid_request_error": 400,
	"authentication_error":  401,
	"billing_error":         402,
	"permission_error":      403,
	"not_found_error":       404,
	"request_too_large":     413,
	"rate_limit_error":      429,
	"api_error":             500,
	"timeout_error":         504,
	"overloaded_error":      529,
}

// mapError is [mapError] with every credential value scrubbed from the
// message: a response body echoing the key never surfaces it.
func (a *Adapter) mapError(err error, model string) error {
	return scrub(mapError(err, model), a.secrets)
}

// refuseRedirect stops the client at a redirect: the 3xx comes back as
// an [llmerrors.HTTPStatusError], not followed.
func refuseRedirect(req *http.Request, _ []*http.Request) error {
	code := 0
	if req.Response != nil {
		code = req.Response.StatusCode
	}
	return llmerrors.NewHTTPStatusError(code, "anthropic: redirect not followed")
}

// thinkingBlocks returns msg's thinking and redacted thinking blocks.
func thinkingBlocks(msg *anthropic.Message) []llm.ThinkingBlock {
	var out []llm.ThinkingBlock
	for _, b := range msg.Content {
		switch b.Type {
		case "thinking":
			out = append(out, llm.ThinkingBlock{Thinking: b.Thinking, Signature: b.Signature})
		case "redacted_thinking":
			out = append(out, llm.ThinkingBlock{Redacted: b.Data})
		}
	}
	return out
}

func outcomeFromMessage(msg *anthropic.Message, names *llm.ToolNames) llm.StreamOutcome {
	tr := toolResponseFromMessage(msg, names)
	return llm.StreamOutcome{
		Content:      tr.Content,
		Thinking:     thinkingBlocks(msg),
		ToolCalls:    tr.ToolCalls,
		FinishReason: string(msg.StopReason),
		Usage:        usageOf(msg.Usage),
	}
}
