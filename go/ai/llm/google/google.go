// Package google implements the Google Gemini LLM adapter using a thin
// HTTP client that speaks the Gemini REST API directly.
//
// Schemes: gemini, google
// Default base URL: https://generativelanguage.googleapis.com/v1beta
// Implements: [llm.Provider], [llm.Completer], [llm.Streamer],
// [llm.ToolCaller]
package google

import (
	"bufio"
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"

	"hop.top/kit/go/ai/llm"
	llmerrors "hop.top/kit/go/ai/llm/errors"
)

const (
	defaultBaseURL = "https://generativelanguage.googleapis.com/v1beta"
)

// Compile-time interface checks.
var (
	_ llm.Provider   = (*Adapter)(nil)
	_ llm.Completer  = (*Adapter)(nil)
	_ llm.Streamer   = (*Adapter)(nil)
	_ llm.ToolCaller = (*Adapter)(nil)
)

// Adapter is the Google Gemini provider. It implements [llm.Provider],
// [llm.Completer], [llm.Streamer], and [llm.ToolCaller].
type Adapter struct {
	baseURL string
	model   string
	apiKey  string
	client  *http.Client
}

// New creates a new Google Gemini adapter from resolved config.
// It is a valid [llm.Factory].
func New(cfg llm.ResolvedConfig) (llm.Provider, error) {
	base := cfg.Provider.BaseURL
	if base == "" {
		base = defaultBaseURL
	}
	base = strings.TrimRight(base, "/")

	model := cfg.Provider.Model
	if model == "" {
		return nil, fmt.Errorf("gemini: model is required")
	}

	apiKey := cfg.Provider.APIKey
	if apiKey == "" {
		apiKey = os.Getenv("GEMINI_API_KEY")
	}
	if apiKey == "" {
		apiKey = os.Getenv("LLM_API_KEY")
	}
	if apiKey == "" {
		return nil, fmt.Errorf(
			"gemini: API key is required (set via config, URI param, " +
				"GEMINI_API_KEY, or LLM_API_KEY)",
		)
	}

	return &Adapter{
		baseURL: base,
		model:   model,
		apiKey:  apiKey,
		client:  http.DefaultClient,
	}, nil
}

// Close is a no-op; the adapter uses a shared HTTP client.
func (a *Adapter) Close() error { return nil }

func (a *Adapter) effectiveModel(req llm.Request) string {
	if req.Model != "" {
		return req.Model
	}
	return a.model
}

// ---------------------------------------------------------------------------
// Gemini API types
// ---------------------------------------------------------------------------

type generateRequest struct {
	Contents          []content         `json:"contents"`
	SystemInstruction *content          `json:"systemInstruction,omitempty"`
	GenerationConfig  *generationConfig `json:"generationConfig,omitempty"`
	Tools             []toolDecl        `json:"tools,omitempty"`
}

type content struct {
	Role  string `json:"role"`
	Parts []part `json:"parts"`
}

type part struct {
	Text       string      `json:"text,omitempty"`
	InlineData *inlineData `json:"inlineData,omitempty"`

	// Tool-related fields.
	FunctionCall     *functionCall     `json:"functionCall,omitempty"`
	FunctionResponse *functionResponse `json:"functionResponse,omitempty"`
}

type inlineData struct {
	MimeType string `json:"mimeType"`
	Data     string `json:"data"`
}

type functionCall struct {
	Name string          `json:"name"`
	Args json.RawMessage `json:"args"`
}

type functionResponse struct {
	Name     string         `json:"name"`
	Response map[string]any `json:"response"`
}

type generationConfig struct {
	Temperature   *float64 `json:"temperature,omitempty"`
	MaxTokens     *int     `json:"maxOutputTokens,omitempty"`
	StopSequences []string `json:"stopSequences,omitempty"`
}

type toolDecl struct {
	FunctionDeclarations []functionDecl `json:"functionDeclarations"`
}

type functionDecl struct {
	Name        string          `json:"name"`
	Description string          `json:"description"`
	Parameters  json.RawMessage `json:"parameters,omitempty"`
}

type generateResponse struct {
	Candidates    []candidate    `json:"candidates"`
	UsageMetadata *usageMetadata `json:"usageMetadata,omitempty"`
	Error         *apiError      `json:"error,omitempty"`
}

type candidate struct {
	Content      content `json:"content"`
	FinishReason string  `json:"finishReason,omitempty"`
}

type usageMetadata struct {
	PromptTokenCount     int `json:"promptTokenCount"`
	CandidatesTokenCount int `json:"candidatesTokenCount"`
	TotalTokenCount      int `json:"totalTokenCount"`
}

type apiError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
	Status  string `json:"status"`
}

// ---------------------------------------------------------------------------
// Completer
// ---------------------------------------------------------------------------

// Complete sends a non-streaming generateContent request.
func (a *Adapter) Complete(
	ctx context.Context, req llm.Request,
) (llm.Response, error) {
	model := a.effectiveModel(req)
	body, err := a.buildBody(ctx, req, nil)
	if err != nil {
		return llm.Response{}, err
	}

	url := fmt.Sprintf(
		"%s/models/%s:generateContent?key=%s",
		a.baseURL, model, a.apiKey,
	)

	httpReq, err := http.NewRequestWithContext(
		ctx, http.MethodPost, url, bytes.NewReader(body),
	)
	if err != nil {
		return llm.Response{}, err
	}
	httpReq.Header.Set("Content-Type", "application/json")

	resp, err := a.client.Do(httpReq)
	if err != nil {
		return llm.Response{}, err
	}
	defer resp.Body.Close()

	if err := a.checkStatus(resp, model); err != nil {
		return llm.Response{}, err
	}

	var gr generateResponse
	if err := json.NewDecoder(resp.Body).Decode(&gr); err != nil {
		return llm.Response{}, fmt.Errorf("gemini: decode response: %w", err)
	}

	if gr.Error != nil {
		return llm.Response{}, a.mapAPIError(gr.Error, model)
	}

	return a.parseResponse(gr), nil
}

// ---------------------------------------------------------------------------
// Streamer
// ---------------------------------------------------------------------------

// Stream sends a streaming generateContent request and returns a
// [llm.TokenIterator].
func (a *Adapter) Stream(
	ctx context.Context, req llm.Request,
) (llm.TokenIterator, error) {
	model := a.effectiveModel(req)
	body, err := a.buildBody(ctx, req, nil)
	if err != nil {
		return nil, err
	}

	url := fmt.Sprintf(
		"%s/models/%s:streamGenerateContent?alt=sse&key=%s",
		a.baseURL, model, a.apiKey,
	)

	httpReq, err := http.NewRequestWithContext(
		ctx, http.MethodPost, url, bytes.NewReader(body),
	)
	if err != nil {
		return nil, err
	}
	httpReq.Header.Set("Content-Type", "application/json")

	resp, err := a.client.Do(httpReq)
	if err != nil {
		return nil, err
	}

	if err := a.checkStatus(resp, model); err != nil {
		resp.Body.Close()
		return nil, err
	}

	scanner := bufio.NewScanner(resp.Body)
	scanner.Buffer(make([]byte, 0, 64*1024), 1024*1024)

	return &streamIterator{
		scanner: scanner,
		body:    resp.Body,
		model:   model,
	}, nil
}

// streamIterator reads SSE lines from a streaming response.
type streamIterator struct {
	scanner *bufio.Scanner
	body    io.ReadCloser
	model   string
	done    bool
}

// Next reads the next token from the stream.
func (s *streamIterator) Next() (llm.Token, error) {
	if s.done {
		return llm.Token{}, io.EOF
	}

	for s.scanner.Scan() {
		line := s.scanner.Text()

		// SSE format: "data: {json}"
		if !strings.HasPrefix(line, "data: ") {
			continue
		}

		data := strings.TrimPrefix(line, "data: ")
		if data == "" {
			continue
		}

		var gr generateResponse
		if err := json.Unmarshal([]byte(data), &gr); err != nil {
			return llm.Token{}, fmt.Errorf(
				"gemini: decode stream chunk: %w", err,
			)
		}

		if gr.Error != nil {
			s.done = true
			return llm.Token{}, mapStreamError(gr.Error, s.model)
		}

		text := extractText(gr)
		finish := extractFinishReason(gr)

		if finish == "STOP" || finish == "MAX_TOKENS" {
			s.done = true
			return llm.Token{Content: text, Done: true}, nil
		}

		if text != "" {
			return llm.Token{Content: text}, nil
		}
	}

	if err := s.scanner.Err(); err != nil {
		s.done = true
		return llm.Token{}, fmt.Errorf("gemini: stream read: %w", err)
	}

	// Stream ended without explicit STOP.
	s.done = true
	return llm.Token{Done: true}, nil
}

// Close releases the underlying response body.
func (s *streamIterator) Close() error {
	return s.body.Close()
}

// ---------------------------------------------------------------------------
// ToolCaller
// ---------------------------------------------------------------------------

// CallWithTools sends a generateContent request with tool declarations.
func (a *Adapter) CallWithTools(
	ctx context.Context, req llm.Request, tools []llm.ToolDef,
) (llm.ToolResponse, error) {
	model := a.effectiveModel(req)
	body, err := a.buildBody(ctx, req, tools)
	if err != nil {
		return llm.ToolResponse{}, err
	}

	url := fmt.Sprintf(
		"%s/models/%s:generateContent?key=%s",
		a.baseURL, model, a.apiKey,
	)

	httpReq, err := http.NewRequestWithContext(
		ctx, http.MethodPost, url, bytes.NewReader(body),
	)
	if err != nil {
		return llm.ToolResponse{}, err
	}
	httpReq.Header.Set("Content-Type", "application/json")

	resp, err := a.client.Do(httpReq)
	if err != nil {
		return llm.ToolResponse{}, err
	}
	defer resp.Body.Close()

	if err := a.checkStatus(resp, model); err != nil {
		return llm.ToolResponse{}, err
	}

	var gr generateResponse
	if err := json.NewDecoder(resp.Body).Decode(&gr); err != nil {
		return llm.ToolResponse{}, fmt.Errorf(
			"gemini: decode response: %w", err,
		)
	}

	if gr.Error != nil {
		return llm.ToolResponse{}, a.mapAPIError(gr.Error, model)
	}

	return a.parseToolResponse(gr), nil
}

// ---------------------------------------------------------------------------
// Helpers
// ---------------------------------------------------------------------------

func (a *Adapter) buildBody(
	ctx context.Context, req llm.Request, tools []llm.ToolDef,
) ([]byte, error) {
	contents := make([]content, 0, len(req.Messages))
	var systemParts []part
	for _, m := range req.Messages {
		if m.Role == "system" {
			systemParts = append(systemParts, part{Text: m.Content})
			continue
		}
		c, err := mapMessage(ctx, m)
		if err != nil {
			return nil, err
		}
		contents = append(contents, c)
	}

	gr := generateRequest{Contents: contents}
	if len(systemParts) > 0 {
		gr.SystemInstruction = &content{
			Role:  "user",
			Parts: systemParts,
		}
	}

	// Generation config.
	var gc generationConfig
	hasConfig := false
	if req.Temperature > 0 {
		t := req.Temperature
		gc.Temperature = &t
		hasConfig = true
	}
	if req.MaxTokens > 0 {
		mt := req.MaxTokens
		gc.MaxTokens = &mt
		hasConfig = true
	}
	if len(req.StopSequences) > 0 {
		gc.StopSequences = req.StopSequences
		hasConfig = true
	}
	if hasConfig {
		gr.GenerationConfig = &gc
	}

	// Tools.
	if len(tools) > 0 {
		decls := make([]functionDecl, 0, len(tools))
		for _, t := range tools {
			decls = append(decls, functionDecl{
				Name:        t.Name,
				Description: t.Description,
				Parameters:  t.Parameters,
			})
		}
		gr.Tools = []toolDecl{{FunctionDeclarations: decls}}
	}

	return json.Marshal(gr)
}

// mapMessage converts an llm.Message to a Gemini content object.
// Gemini uses "user" and "model" roles; "assistant" is mapped to "model".
func mapMessage(ctx context.Context, m llm.Message) (content, error) {
	role := m.Role
	if role == "assistant" {
		role = "model"
	}

	if len(m.Parts) == 0 {
		return content{
			Role:  role,
			Parts: []part{{Text: m.Content}},
		}, nil
	}

	parts := make([]part, 0, len(m.Parts))
	for _, p := range m.Parts {
		switch p.Type {
		case llm.PartTypeText:
			parts = append(parts, part{Text: p.Text})

		case llm.PartTypeImage:
			if p.Source == nil {
				return content{}, fmt.Errorf(
					"gemini: image part has nil source",
				)
			}
			rc, err := p.Source.Reader(ctx)
			if err != nil {
				return content{}, fmt.Errorf(
					"gemini: read image: %w", err,
				)
			}
			raw, err := io.ReadAll(rc)
			rc.Close()
			if err != nil {
				return content{}, fmt.Errorf(
					"gemini: read image bytes: %w", err,
				)
			}
			mimeType := p.MimeType
			if mimeType == "" && p.Source != nil {
				mimeType = p.Source.MimeType()
			}
			if mimeType == "" {
				mimeType = "image/png"
			}
			parts = append(parts, part{
				InlineData: &inlineData{
					MimeType: mimeType,
					Data:     base64.StdEncoding.EncodeToString(raw),
				},
			})

		default:
			return content{}, llmerrors.NewUnsupportedModality(
				string(p.Type), "gemini", nil,
			)
		}
	}

	return content{Role: role, Parts: parts}, nil
}

func (a *Adapter) parseResponse(gr generateResponse) llm.Response {
	resp := llm.Response{
		Role:         "assistant",
		FinishReason: "stop",
	}

	if len(gr.Candidates) > 0 {
		c := gr.Candidates[0]
		resp.Content = extractContentText(c.Content)
		if c.FinishReason != "" {
			resp.FinishReason = mapFinishReason(c.FinishReason)
		}
	}

	if gr.UsageMetadata != nil {
		resp.Usage = llm.Usage{
			PromptTokens:     gr.UsageMetadata.PromptTokenCount,
			CompletionTokens: gr.UsageMetadata.CandidatesTokenCount,
			TotalTokens:      gr.UsageMetadata.TotalTokenCount,
		}
	}

	return resp
}

func (a *Adapter) parseToolResponse(gr generateResponse) llm.ToolResponse {
	resp := llm.ToolResponse{}

	if len(gr.Candidates) == 0 {
		return resp
	}

	c := gr.Candidates[0]
	var textParts []string
	for _, p := range c.Content.Parts {
		if p.Text != "" {
			textParts = append(textParts, p.Text)
		}
		if p.FunctionCall != nil {
			resp.ToolCalls = append(resp.ToolCalls, llm.ToolCall{
				Name:      p.FunctionCall.Name,
				Arguments: p.FunctionCall.Args,
			})
		}
	}
	resp.Content = strings.Join(textParts, "")

	return resp
}

func extractContentText(c content) string {
	var sb strings.Builder
	for _, p := range c.Parts {
		if p.Text != "" {
			sb.WriteString(p.Text)
		}
	}
	return sb.String()
}

func extractText(gr generateResponse) string {
	if len(gr.Candidates) == 0 {
		return ""
	}
	return extractContentText(gr.Candidates[0].Content)
}

func extractFinishReason(gr generateResponse) string {
	if len(gr.Candidates) == 0 {
		return ""
	}
	return gr.Candidates[0].FinishReason
}

func mapFinishReason(reason string) string {
	switch reason {
	case "STOP":
		return "stop"
	case "MAX_TOKENS":
		return "length"
	case "SAFETY":
		return "content_filter"
	default:
		return reason
	}
}

func (a *Adapter) checkStatus(
	resp *http.Response, model string,
) error {
	if resp.StatusCode >= 200 && resp.StatusCode < 300 {
		return nil
	}

	body, _ := io.ReadAll(resp.Body)

	// Try to parse Gemini error body.
	var errResp struct {
		Error *apiError `json:"error"`
	}
	if json.Unmarshal(body, &errResp) == nil && errResp.Error != nil {
		return a.mapAPIError(errResp.Error, model)
	}

	// Fall back to HTTP status code mapping.
	switch {
	case resp.StatusCode == 401 || resp.StatusCode == 403:
		return llmerrors.NewAuth(
			"gemini", fmt.Errorf("%s", resp.Status),
		)
	case resp.StatusCode == 404:
		return llmerrors.NewModel(model, "gemini")
	case resp.StatusCode == 429:
		return llmerrors.NewRateLimit("gemini", 0)
	case resp.StatusCode >= 500:
		return llmerrors.NewHTTPStatusError(resp.StatusCode, resp.Status)
	default:
		return fmt.Errorf(
			"gemini: unexpected status %d: %s",
			resp.StatusCode, string(body),
		)
	}
}

// mapStreamError maps an SSE error payload to typed llm/errors so
// fallback decisions work consistently for streaming.
func mapStreamError(e *apiError, model string) error {
	switch e.Code {
	case 401, 403:
		return llmerrors.NewAuth(
			"gemini", fmt.Errorf("%s", e.Message),
		)
	case 404:
		return llmerrors.NewModel(model, "gemini")
	case 429:
		return llmerrors.NewRateLimit("gemini", 0)
	default:
		if e.Code >= 500 {
			return llmerrors.NewHTTPStatusError(e.Code, e.Message)
		}
		return fmt.Errorf(
			"gemini: stream error %d: %s", e.Code, e.Message,
		)
	}
}

func (a *Adapter) mapAPIError(e *apiError, model string) error {
	switch e.Code {
	case 401, 403:
		return llmerrors.NewAuth(
			"gemini", fmt.Errorf("%s", e.Message),
		)
	case 404:
		return llmerrors.NewModel(model, "gemini")
	case 429:
		return llmerrors.NewRateLimit("gemini", 0)
	default:
		if e.Code >= 500 {
			return llmerrors.NewHTTPStatusError(e.Code, e.Message)
		}
		return fmt.Errorf("gemini: API error %d: %s", e.Code, e.Message)
	}
}

// ---------------------------------------------------------------------------
// Registration
// ---------------------------------------------------------------------------

func init() {
	llm.Register("gemini", New)
	llm.Register("google", New)
}
