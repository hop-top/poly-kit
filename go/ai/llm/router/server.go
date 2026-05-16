package router

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"net/http"
	"time"

	"hop.top/kit/go/ai/llm"
)

// Server provides OpenAI-compatible HTTP endpoints for routed completions.
type Server struct {
	ctrl *Controller
	mux  *http.ServeMux
}

// NewServer creates a Server wrapping the given Controller.
func NewServer(ctrl *Controller) *Server {
	s := &Server{ctrl: ctrl, mux: http.NewServeMux()}
	s.mux.HandleFunc("POST /v1/chat/completions", s.handleChatCompletions)
	s.mux.HandleFunc("GET /health", s.handleHealth)
	return s
}

// ServeHTTP implements http.Handler.
func (s *Server) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	s.mux.ServeHTTP(w, r)
}

// chatCompletionRequest mirrors the OpenAI chat completion request.
type chatCompletionRequest struct {
	Model       string          `json:"model"`
	Messages    []chatMessage   `json:"messages"`
	Temperature *float64        `json:"temperature,omitempty"`
	MaxTokens   *int            `json:"max_tokens,omitempty"`
	Stream      bool            `json:"stream,omitempty"`
	Stop        json.RawMessage `json:"stop,omitempty"`
}

type chatMessage struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

// chatCompletionResponse mirrors the OpenAI chat completion response.
type chatCompletionResponse struct {
	ID      string                 `json:"id"`
	Object  string                 `json:"object"`
	Created int64                  `json:"created"`
	Model   string                 `json:"model"`
	Choices []chatCompletionChoice `json:"choices"`
	Usage   chatUsage              `json:"usage"`
}

type chatCompletionChoice struct {
	Index        int         `json:"index"`
	Message      chatMessage `json:"message"`
	FinishReason string      `json:"finish_reason"`
}

type chatUsage struct {
	PromptTokens     int `json:"prompt_tokens"`
	CompletionTokens int `json:"completion_tokens"`
	TotalTokens      int `json:"total_tokens"`
}

type errorResponse struct {
	Object  string `json:"object"`
	Message string `json:"message"`
}

func (s *Server) handleChatCompletions(
	w http.ResponseWriter, r *http.Request,
) {
	var req chatCompletionRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest,
			fmt.Sprintf("invalid request body: %v", err))
		return
	}

	// Convert to llm.Request.
	msgs := make([]llm.Message, len(req.Messages))
	for i, m := range req.Messages {
		msgs[i] = llm.Message{Role: m.Role, Content: m.Content}
	}

	llmReq := llm.Request{
		Model:    req.Model,
		Messages: msgs,
	}
	if req.Temperature != nil {
		llmReq.Temperature = *req.Temperature
	}
	if req.MaxTokens != nil {
		llmReq.MaxTokens = *req.MaxTokens
	}

	resp, err := s.ctrl.Complete(r.Context(), llmReq)
	if err != nil {
		var re *RoutingError
		if errors.As(err, &re) {
			writeError(w, http.StatusBadRequest, err.Error())
			return
		}
		log.Printf("completion error: %v", err)
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}

	out := chatCompletionResponse{
		ID:      generateID(),
		Object:  "chat.completion",
		Created: time.Now().Unix(),
		Model:   req.Model,
		Choices: []chatCompletionChoice{
			{
				Index: 0,
				Message: chatMessage{
					Role:    resp.Role,
					Content: resp.Content,
				},
				FinishReason: coalesce(resp.FinishReason, "stop"),
			},
		},
		Usage: chatUsage{
			PromptTokens:     resp.Usage.PromptTokens,
			CompletionTokens: resp.Usage.CompletionTokens,
			TotalTokens:      resp.Usage.TotalTokens,
		},
	}

	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(out)
}

func (s *Server) handleHealth(
	w http.ResponseWriter, _ *http.Request,
) {
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]string{
		"status": "online",
	})
}

func writeError(w http.ResponseWriter, code int, msg string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	_ = json.NewEncoder(w).Encode(errorResponse{
		Object:  "error",
		Message: msg,
	})
}

func generateID() string {
	b := make([]byte, 12)
	_, _ = rand.Read(b)
	return "chatcmpl-" + hex.EncodeToString(b)
}

func coalesce(a, b string) string {
	if a != "" {
		return a
	}
	return b
}
