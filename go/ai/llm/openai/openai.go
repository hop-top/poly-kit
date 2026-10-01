// Package openai adapts openai/openai-go to the llm provider interfaces.
//
// Registers schemes: openai, openrouter, xai, lmstudio, groq, together,
// fireworks, deepseek, mistral. lmstudio defaults to LM Studio's local
// server, http://localhost:1234/v1, and appends /v1 to any base URL
// that lacks it. An openai host-form URI (openai://host:port/model)
// names a self-hosted OpenAI-compatible server and reaches it at
// http://host:port/v1; an openai base_url is used as given.
//
// It also claims the OpenAI wire protocols (see protocols): a
// provider in the aim catalog that no adapter registers by name, and
// whose protocol is one of them ("openai-compatible" for most), resolves
// here with the catalog's base URL.
// Implements [llm.Completer], [llm.Streamer], [llm.ToolCaller].
package openai

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strings"

	oai "github.com/openai/openai-go"
	"github.com/openai/openai-go/option"
	"github.com/openai/openai-go/packages/param"
	"github.com/openai/openai-go/packages/ssestream"
	"github.com/openai/openai-go/shared"

	"hop.top/kit/go/ai/llm"
	llmerrors "hop.top/kit/go/ai/llm/errors"
)

// schemes registered by this adapter.
var schemes = []string{
	"openai", "openrouter", "xai", "lmstudio",
	"groq", "together", "fireworks", "deepseek", "mistral",
}

// defaultBaseURLs maps scheme to its default API base URL, declared at
// registration so an alias of the scheme ("fireworks-ai") gets it too.
// Kit keeps its own even where the aim catalog has one: a scheme must
// reach its host with no catalog cached (offline, first run), and a
// declaration outranks the catalog. Schemes not listed here fall back
// to OpenAI's URL.
var defaultBaseURLs = map[string]string{
	"openrouter": "https://openrouter.ai/api/v1",
	"xai":        "https://api.x.ai/v1",
	"groq":       "https://api.groq.com/openai/v1",
	"together":   "https://api.together.xyz/v1",
	"fireworks":  "https://api.fireworks.ai/inference/v1",
	"deepseek":   "https://api.deepseek.com",
	"mistral":    "https://api.mistral.ai/v1",
	// LM Studio's local server; name another host or port with a
	// host-form URI or ?base_url=.
	"lmstudio": "http://localhost:1234/v1",
}

// protocols are the aim catalog protocols (derived from each provider's
// AI SDK package) this adapter speaks: the OpenAI Chat Completions wire.
// Only catalog providers that no adapter registers by name route by
// protocol, so these reach catalog-only providers such as DigitalOcean
// (openai-compatible) or Meta (openai).
var protocols = []string{
	"openai-compatible", "openai", "openrouter",
	"groq", "xai", "togetherai", "mistral",
}

// declaration is what this adapter states about scheme s.
func declaration(s string) llm.Declaration {
	d := llm.Declaration{BaseURL: defaultBaseURLs[s]}
	switch s {
	case "openai":
		d.Protocols = protocols
	case "lmstudio":
		// LM Studio's local server takes no key: none is sent even when
		// LMSTUDIO_API_KEY, which the aim catalog lists, is set.
		d.Key = &llm.ProviderKey{Optional: true}
	}
	return d
}

func init() {
	for _, s := range schemes {
		llm.Register(s, New, declaration(s))
	}
}

// Adapter wraps an openai-go client and model name.
type Adapter struct {
	client oai.Client
	model  string
	scheme string
}

// compile-time interface checks.
var (
	_ llm.Provider   = (*Adapter)(nil)
	_ llm.Completer  = (*Adapter)(nil)
	_ llm.Streamer   = (*Adapter)(nil)
	_ llm.ToolCaller = (*Adapter)(nil)
)

// New creates an Adapter from the resolved config.
func New(cfg llm.ResolvedConfig) (llm.Provider, error) {
	opts := []option.RequestOption{
		option.WithAPIKey(cfg.Provider.APIKey),
	}

	base := cfg.Provider.BaseURL
	if base == "" {
		if u, ok := defaultBaseURLs[cfg.URI.Scheme]; ok {
			base = u
		} else {
			base = "https://api.openai.com/v1"
		}
	}
	if cfg.URI.Scheme == "lmstudio" || cfg.URI.Scheme == "openai" && isHostForm(cfg) {
		base = apiRootV1(base)
	}
	opts = append(opts, option.WithBaseURL(base))

	model := cfg.Provider.Model
	if model == "" {
		model = cfg.URI.Model
	}

	return &Adapter{
		client: oai.NewClient(opts...),
		model:  model,
		scheme: cfg.URI.Scheme,
	}, nil
}

// isHostForm reports whether cfg's base URL is the one a host-form URI
// (scheme://host:port/model) derives, http://host:port, rather than one
// named outright by a base_url param, a config file or LLM_BASE_URL,
// each of which outranks the host.
func isHostForm(cfg llm.ResolvedConfig) bool {
	if _, ok := cfg.URI.Params["base_url"]; ok {
		return false
	}
	return cfg.Provider.BaseURL == "http://"+cfg.URI.Host
}

// apiRootV1 returns base ending in /v1; one already ending in /v1, with
// or without a trailing slash, is kept.
//
// lmstudio applies it to every base URL: LM Studio serves its
// OpenAI-compatible API under /v1 only, so a base URL naming just the
// server (a host-form URI, base_url, LLM_BASE_URL) gets /v1 appended.
//
// openai applies it to the host form only. A host form cannot carry a
// path, and the servers it names (vLLM, llama.cpp, TGI, Ollama, LocalAI,
// LM Studio) serve the API under /v1, as api.openai.com does. A base_url
// is the API root as given: gateways and proxies mount it under other
// prefixes, so appending /v1 there would break them.
func apiRootV1(base string) string {
	trimmed := strings.TrimRight(base, "/")
	if strings.HasSuffix(trimmed, "/v1") {
		return base
	}
	return trimmed + "/v1"
}

// Close is a no-op; the HTTP client has no persistent connections to tear down.
func (a *Adapter) Close() error { return nil }

func (a *Adapter) effectiveModel(req llm.Request) string {
	if req.Model != "" {
		return req.Model
	}
	return a.model
}

// Complete maps an llm.Request to an OpenAI ChatCompletion and back.
func (a *Adapter) Complete(
	ctx context.Context, req llm.Request,
) (llm.Response, error) {
	model := a.effectiveModel(req)
	params, err := a.buildParams(ctx, req)
	if err != nil {
		return llm.Response{}, err
	}
	comp, err := a.client.Chat.Completions.New(ctx, params)
	if err != nil {
		return llm.Response{}, mapError(err, a.scheme, model)
	}
	return mapCompletion(comp), nil
}

// Stream returns a TokenIterator wrapping an OpenAI streaming response.
func (a *Adapter) Stream(
	ctx context.Context, req llm.Request,
) (llm.TokenIterator, error) {
	model := a.effectiveModel(req)
	params, err := a.buildParams(ctx, req)
	if err != nil {
		return nil, err
	}
	stream := a.client.Chat.Completions.NewStreaming(ctx, params)
	// Check for immediate errors (e.g. connection refused).
	if err := stream.Err(); err != nil {
		_ = stream.Close()
		return nil, mapError(err, a.scheme, model)
	}
	return &streamIter{stream: stream, scheme: a.scheme, model: model}, nil
}

// CallWithTools maps ToolDefs to OpenAI function tools and returns tool calls.
func (a *Adapter) CallWithTools(
	ctx context.Context, req llm.Request, tools []llm.ToolDef,
) (llm.ToolResponse, error) {
	params, err := a.buildParams(ctx, req)
	if err != nil {
		return llm.ToolResponse{}, err
	}
	params.Tools = mapTools(tools)

	comp, err := a.client.Chat.Completions.New(ctx, params)
	if err != nil {
		return llm.ToolResponse{}, mapError(err, a.scheme, a.effectiveModel(req))
	}

	return mapToolResponse(comp), nil
}

// ---------- param building ----------

func (a *Adapter) buildParams(
	ctx context.Context, req llm.Request,
) (oai.ChatCompletionNewParams, error) {
	model := a.model
	if req.Model != "" {
		model = req.Model
	}

	msgs, err := mapMessages(ctx, req.Messages)
	if err != nil {
		return oai.ChatCompletionNewParams{}, err
	}

	p := oai.ChatCompletionNewParams{
		Model:    model,
		Messages: msgs,
	}
	if req.Temperature != nil {
		p.Temperature = param.NewOpt(*req.Temperature)
	}
	if req.MaxTokens > 0 {
		p.MaxTokens = param.NewOpt(int64(req.MaxTokens))
	}
	if len(req.StopSequences) > 0 {
		p.Stop = oai.ChatCompletionNewParamsStopUnion{
			OfStringArray: req.StopSequences,
		}
	}
	return p, nil
}

func mapMessages(
	ctx context.Context,
	msgs []llm.Message,
) ([]oai.ChatCompletionMessageParamUnion, error) {
	out := make([]oai.ChatCompletionMessageParamUnion, 0, len(msgs))
	for _, m := range msgs {
		linked, err := mapToolLinkage(m)
		if err != nil {
			return nil, err
		}
		if linked != nil {
			out = append(out, *linked)
			continue
		}

		if len(m.Parts) == 0 {
			// Text-only path (unchanged).
			switch m.Role {
			case "system":
				out = append(out, oai.SystemMessage(m.Content))
			case "assistant":
				out = append(out, oai.AssistantMessage(m.Content))
			default:
				out = append(out, oai.UserMessage(m.Content))
			}
			continue
		}

		// Multimodal path — only user messages support content parts.
		if m.Role != "" && m.Role != "user" {
			return nil, fmt.Errorf("openai: message role %q does not support content parts", m.Role)
		}
		parts, err := mapContentParts(ctx, m.Parts)
		if err != nil {
			return nil, err
		}
		out = append(out, oai.UserMessage(parts))
	}
	return out, nil
}

// mapToolLinkage maps the tool-call half of a conversation: an assistant
// turn carrying ToolCalls becomes a message with tool_calls, and a role
// "tool" result becomes a tool message with tool_call_id. It returns nil
// and no error for any other message, which then maps as before.
func mapToolLinkage(m llm.Message) (*oai.ChatCompletionMessageParamUnion, error) {
	switch {
	case m.Role == "tool":
		if m.ToolCallID == "" {
			return nil, fmt.Errorf("openai: tool result needs ToolCallID")
		}
		if len(m.ToolCalls) > 0 || len(m.Parts) > 0 {
			return nil, fmt.Errorf("openai: tool result %q carries only Content", m.ToolCallID)
		}
		msg := oai.ToolMessage(m.Content, m.ToolCallID)
		return &msg, nil

	case m.ToolCallID != "":
		return nil, fmt.Errorf("openai: ToolCallID is valid only on role tool, got %q", m.Role)

	case len(m.ToolCalls) == 0:
		return nil, nil

	case m.Role != "assistant":
		return nil, fmt.Errorf("openai: ToolCalls are valid only on role assistant, got %q", m.Role)

	case len(m.Parts) > 0:
		return nil, fmt.Errorf("openai: assistant tool calls do not support content parts")
	}

	var asst oai.ChatCompletionAssistantMessageParam
	if m.Content != "" {
		asst.Content.OfString = param.NewOpt(m.Content)
	}
	for _, tc := range m.ToolCalls {
		if tc.ID == "" {
			return nil, fmt.Errorf("openai: tool call %q needs an ID", tc.Name)
		}
		args := "{}"
		if len(tc.Arguments) > 0 {
			if !json.Valid(tc.Arguments) {
				return nil, fmt.Errorf("openai: tool call %q arguments are not valid JSON", tc.ID)
			}
			args = string(tc.Arguments)
		}
		asst.ToolCalls = append(asst.ToolCalls, oai.ChatCompletionMessageToolCallParam{
			ID: tc.ID,
			Function: oai.ChatCompletionMessageToolCallFunctionParam{
				Name:      tc.Name,
				Arguments: args,
			},
		})
	}
	return &oai.ChatCompletionMessageParamUnion{OfAssistant: &asst}, nil
}

// mapContentParts converts llm.ContentPart slice to OpenAI content part params.
func mapContentParts(
	ctx context.Context,
	parts []llm.ContentPart,
) ([]oai.ChatCompletionContentPartUnionParam, error) {
	out := make([]oai.ChatCompletionContentPartUnionParam, 0, len(parts))
	for _, p := range parts {
		switch p.Type {
		case llm.PartTypeText:
			out = append(out, oai.TextContentPart(p.Text))

		case llm.PartTypeImage:
			mime := p.MimeType
			if mime == "" && p.Source != nil {
				mime = p.Source.MimeType()
			}

			// PDF → file content part.
			if mime == "application/pdf" {
				data, err := readBase64(ctx, p.Source)
				if err != nil {
					return nil, fmt.Errorf("openai: read PDF: %w", err)
				}
				out = append(out, oai.FileContentPart(
					oai.ChatCompletionContentPartFileFileParam{
						FileData: param.NewOpt(data),
					},
				))
				continue
			}

			// Image: prefer URL passthrough when available.
			if p.Source != nil && p.Source.URL() != "" {
				out = append(out, oai.ImageContentPart(
					oai.ChatCompletionContentPartImageImageURLParam{
						URL: p.Source.URL(),
					},
				))
				continue
			}

			// Fall back to base64 data URI.
			if mime == "" {
				return nil, llmerrors.NewInvalidFormat("", "openai", nil)
			}
			data, err := readBase64(ctx, p.Source)
			if err != nil {
				return nil, fmt.Errorf("openai: read image: %w", err)
			}
			url := "data:" + mime + ";base64," + data
			out = append(out, oai.ImageContentPart(
				oai.ChatCompletionContentPartImageImageURLParam{URL: url},
			))

		default:
			return nil, llmerrors.NewUnsupportedModality(string(p.Type), "openai", nil)
		}
	}
	return out, nil
}

// readBase64 reads all bytes from src and returns base64-encoded string.
func readBase64(ctx context.Context, src llm.MediaSource) (string, error) {
	if src == nil {
		return "", fmt.Errorf("openai: nil media source")
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

func mapTools(defs []llm.ToolDef) []oai.ChatCompletionToolParam {
	out := make([]oai.ChatCompletionToolParam, 0, len(defs))
	for _, d := range defs {
		var params shared.FunctionParameters
		if d.Parameters != nil {
			_ = json.Unmarshal(d.Parameters, &params)
		}
		fd := shared.FunctionDefinitionParam{
			Name:       d.Name,
			Parameters: params,
		}
		if d.Description != "" {
			fd.Description = param.NewOpt(d.Description)
		}
		out = append(out, oai.ChatCompletionToolParam{Function: fd})
	}
	return out
}

// ---------- response mapping ----------

func mapCompletion(c *oai.ChatCompletion) llm.Response {
	var resp llm.Response
	if len(c.Choices) > 0 {
		resp.Content = c.Choices[0].Message.Content
		resp.Role = string(c.Choices[0].Message.Role)
		resp.FinishReason = c.Choices[0].FinishReason
	}
	resp.Usage = llm.Usage{
		PromptTokens:     int(c.Usage.PromptTokens),
		CompletionTokens: int(c.Usage.CompletionTokens),
		TotalTokens:      int(c.Usage.TotalTokens),
	}
	return resp
}

func mapToolResponse(c *oai.ChatCompletion) llm.ToolResponse {
	var resp llm.ToolResponse
	if len(c.Choices) > 0 {
		resp.Content = c.Choices[0].Message.Content
		for _, tc := range c.Choices[0].Message.ToolCalls {
			resp.ToolCalls = append(resp.ToolCalls, llm.ToolCall{
				ID:        tc.ID,
				Name:      tc.Function.Name,
				Arguments: json.RawMessage(tc.Function.Arguments),
			})
		}
	}
	return resp
}

// ---------- streaming ----------

type streamIter struct {
	stream *ssestream.Stream[oai.ChatCompletionChunk]
	scheme string
	model  string
	done   bool
}

func (s *streamIter) Next() (llm.Token, error) {
	if s.done {
		return llm.Token{}, io.EOF
	}
	if !s.stream.Next() {
		s.done = true
		err := s.stream.Err()
		if err != nil {
			return llm.Token{}, mapError(err, s.scheme, s.model)
		}
		return llm.Token{Done: true}, nil
	}

	chunk := s.stream.Current()
	var content string
	if len(chunk.Choices) > 0 {
		content = chunk.Choices[0].Delta.Content
	}
	return llm.Token{Content: content}, nil
}

func (s *streamIter) Close() error {
	return s.stream.Close()
}

// ---------- error mapping ----------

func mapError(err error, scheme, model string) error {
	// Errors quote the request URL; a base URL may carry a credential.
	err = llm.RedactURLError(err)
	var apiErr *oai.Error
	if errors.As(err, &apiErr) {
		redactRequestURL(apiErr.Request)
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
					apiErr.StatusCode, apiErr.Message,
				)
			}
		}
	}
	return err
}
