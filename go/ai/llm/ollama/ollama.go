// Package ollama implements the Ollama LLM adapter using a thin HTTP
// client that speaks the Ollama REST API directly, avoiding the heavy
// ollama/ollama dependency tree.
//
// Scheme: ollama
// Default base URL: http://localhost:11434
// Implements: [llm.Provider], [llm.Completer], [llm.Streamer]
package ollama

import (
	"bufio"
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"

	"hop.top/kit/go/ai/llm"
	llmerrors "hop.top/kit/go/ai/llm/errors"
)

const (
	defaultBaseURL = "http://localhost:11434"
	chatEndpoint   = "/api/chat"
)

// Adapter is the Ollama provider. It implements [llm.Provider],
// [llm.Completer], and [llm.Streamer].
type Adapter struct {
	baseURL string
	model   string
	client  *http.Client
}

// New creates a new Ollama adapter from resolved config.
// It is a valid [llm.Factory].
func New(cfg llm.ResolvedConfig) (llm.Provider, error) {
	base := cfg.Provider.BaseURL
	if base == "" {
		base = defaultBaseURL
	}
	base = strings.TrimRight(base, "/")

	model := cfg.Provider.Model
	if model == "" {
		return nil, fmt.Errorf("ollama: model is required")
	}

	return &Adapter{
		baseURL: base,
		model:   model,
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
// Ollama API types
// ---------------------------------------------------------------------------

type chatRequest struct {
	Model    string         `json:"model"`
	Messages []chatMessage  `json:"messages"`
	Stream   bool           `json:"stream"`
	Options  map[string]any `json:"options,omitempty"`
}

type chatMessage struct {
	Role    string   `json:"role"`
	Content string   `json:"content"`
	Images  []string `json:"images,omitempty"`
}

type chatResponse struct {
	Message       chatMessage `json:"message"`
	Done          bool        `json:"done"`
	TotalDuration int64       `json:"total_duration,omitempty"`
	EvalCount     int         `json:"eval_count,omitempty"`
	PromptEval    int         `json:"prompt_eval_count,omitempty"`
}

type errorResponse struct {
	Error string `json:"error"`
}

// ---------------------------------------------------------------------------
// Completer
// ---------------------------------------------------------------------------

// Complete sends a non-streaming chat request.
func (a *Adapter) Complete(
	ctx context.Context, req llm.Request,
) (llm.Response, error) {
	model := a.effectiveModel(req)
	body, err := a.buildBody(ctx, req, false)
	if err != nil {
		return llm.Response{}, err
	}

	httpReq, err := http.NewRequestWithContext(
		ctx, http.MethodPost,
		a.baseURL+chatEndpoint, bytes.NewReader(body),
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

	var cr chatResponse
	if err := json.NewDecoder(resp.Body).Decode(&cr); err != nil {
		return llm.Response{}, fmt.Errorf("ollama: decode response: %w", err)
	}

	return llm.Response{
		Content: cr.Message.Content,
		Role:    cr.Message.Role,
		Usage: llm.Usage{
			PromptTokens:     cr.PromptEval,
			CompletionTokens: cr.EvalCount,
			TotalTokens:      cr.PromptEval + cr.EvalCount,
		},
		FinishReason: "stop",
	}, nil
}

// ---------------------------------------------------------------------------
// Streamer
// ---------------------------------------------------------------------------

// Stream sends a streaming chat request and returns a [llm.TokenIterator].
func (a *Adapter) Stream(
	ctx context.Context, req llm.Request,
) (llm.TokenIterator, error) {
	model := a.effectiveModel(req)
	body, err := a.buildBody(ctx, req, true)
	if err != nil {
		return nil, err
	}

	httpReq, err := http.NewRequestWithContext(
		ctx, http.MethodPost,
		a.baseURL+chatEndpoint, bytes.NewReader(body),
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

	return &streamIterator{
		reader: bufio.NewReader(resp.Body),
		body:   resp.Body,
	}, nil
}

// streamIterator reads NDJSON lines from a streaming response.
type streamIterator struct {
	reader *bufio.Reader
	body   io.ReadCloser
	done   bool
}

// Next reads the next token from the stream.
func (s *streamIterator) Next() (llm.Token, error) {
	if s.done {
		return llm.Token{}, io.EOF
	}

	line, err := s.reader.ReadBytes('\n')
	if err != nil && len(line) == 0 {
		s.done = true
		if err == io.EOF {
			return llm.Token{Done: true}, nil
		}
		return llm.Token{}, err
	}

	line = bytes.TrimSpace(line)
	if len(line) == 0 {
		// skip empty lines
		return s.Next()
	}

	var cr chatResponse
	if err := json.Unmarshal(line, &cr); err != nil {
		return llm.Token{}, fmt.Errorf("ollama: decode stream chunk: %w", err)
	}

	if cr.Done {
		s.done = true
		return llm.Token{Content: cr.Message.Content, Done: true}, nil
	}

	return llm.Token{Content: cr.Message.Content}, nil
}

// Close releases the underlying response body.
func (s *streamIterator) Close() error {
	return s.body.Close()
}

// ---------------------------------------------------------------------------
// Helpers
// ---------------------------------------------------------------------------

func (a *Adapter) buildBody(ctx context.Context, req llm.Request, stream bool) ([]byte, error) {
	msgs := make([]chatMessage, 0, len(req.Messages))
	for _, m := range req.Messages {
		cm, err := mapOllamaMessage(ctx, m)
		if err != nil {
			return nil, err
		}
		msgs = append(msgs, cm)
	}

	model := a.model
	if req.Model != "" {
		model = req.Model
	}

	cr := chatRequest{
		Model:    model,
		Messages: msgs,
		Stream:   stream,
	}

	// Map temperature into options.
	if req.Temperature > 0 {
		cr.Options = map[string]any{"temperature": req.Temperature}
	}

	return json.Marshal(cr)
}

// mapOllamaMessage converts an llm.Message to an Ollama chatMessage.
// When Parts are present, text parts are concatenated into Content and
// image parts are base64-encoded into Images (plain base64, no data URI).
func mapOllamaMessage(ctx context.Context, m llm.Message) (chatMessage, error) {
	if len(m.Parts) == 0 {
		return chatMessage{Role: m.Role, Content: m.Content}, nil
	}

	var sb strings.Builder
	var images []string

	for _, p := range m.Parts {
		switch p.Type {
		case llm.PartTypeText:
			sb.WriteString(p.Text)

		case llm.PartTypeImage:
			if p.Source == nil {
				return chatMessage{}, fmt.Errorf("ollama: image part has nil source")
			}
			rc, err := p.Source.Reader(ctx)
			if err != nil {
				return chatMessage{}, fmt.Errorf("ollama: read image: %w", err)
			}
			raw, err := io.ReadAll(rc)
			rc.Close()
			if err != nil {
				return chatMessage{}, fmt.Errorf("ollama: read image bytes: %w", err)
			}
			images = append(images, base64.StdEncoding.EncodeToString(raw))

		default:
			return chatMessage{}, llmerrors.NewUnsupportedModality(string(p.Type), "ollama", nil)
		}
	}

	return chatMessage{
		Role:    m.Role,
		Content: sb.String(),
		Images:  images,
	}, nil
}

func (a *Adapter) checkStatus(resp *http.Response, model string) error {
	if resp.StatusCode >= 200 && resp.StatusCode < 300 {
		return nil
	}

	body, _ := io.ReadAll(resp.Body)

	// Try to parse Ollama error body.
	var errResp errorResponse
	if json.Unmarshal(body, &errResp) == nil && errResp.Error != "" {
		if isModelNotFound(errResp.Error) {
			return llmerrors.NewModel(model, "ollama")
		}
	}

	// Map HTTP status codes to typed errors.
	switch {
	case resp.StatusCode == 401 || resp.StatusCode == 403:
		return llmerrors.NewAuth(
			"ollama", fmt.Errorf("%s", resp.Status),
		)
	case resp.StatusCode == 404:
		return llmerrors.NewModel(model, "ollama")
	case resp.StatusCode == 429:
		return llmerrors.NewRateLimit("ollama", 0)
	case resp.StatusCode >= 500:
		return llmerrors.NewHTTPStatusError(
			resp.StatusCode, resp.Status,
		)
	default:
		return fmt.Errorf(
			"ollama: unexpected status %d: %s",
			resp.StatusCode, string(body),
		)
	}
}

func isModelNotFound(msg string) bool {
	lower := strings.ToLower(msg)
	return strings.Contains(lower, "model") &&
		(strings.Contains(lower, "not found") ||
			strings.Contains(lower, "does not exist"))
}
