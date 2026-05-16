// Package llm provides a ToolSpec source that uses an LLM to generate
// error patterns and workflows for CLI tools.
package llm

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	kitllm "hop.top/kit/go/ai/llm"
	"hop.top/kit/go/ai/toolspec"
)

// Config controls the LLM source behavior.
type Config struct {
	Client  kitllm.Completer // required
	Model   string           // optional; forwarded to Request
	Enabled bool             // must be true for source to fire
}

// LLMSource resolves partial ToolSpec data (Commands, ErrorPatterns,
// Workflows) by asking an LLM. Commands include Intent and SuggestedNext
// fields; ErrorPatterns and Workflows include Provenance metadata.
type LLMSource struct {
	cfg Config
}

// NewLLMSource returns a ready-to-use LLM source.
func NewLLMSource(cfg Config) *LLMSource { return &LLMSource{cfg: cfg} }

// Resolve implements toolspec.Source.
func (s *LLMSource) Resolve(tool string) (*toolspec.ToolSpec, error) {
	if !s.cfg.Enabled {
		return nil, nil
	}
	if s.cfg.Client == nil {
		return nil, fmt.Errorf("llm source: Client is required when Enabled")
	}

	prompt := fmt.Sprintf(
		`You are a CLI knowledge base. For the tool %q, generate a JSON object with:
- "error_patterns": array of {"pattern": "...", "fix": "...", "source": "llm"}
- "workflows": array of {"name": "...", "steps": ["..."]}
- "commands": array of {"name": "...", "intent": {"domain": "...", "category": "...", "tags": [...]}, "suggested_next": ["..."]}
Output ONLY valid JSON. No markdown, no explanation.`, tool)

	resp, err := s.cfg.Client.Complete(context.Background(), kitllm.Request{
		Messages: []kitllm.Message{{Role: "user", Content: prompt}},
		Model:    s.cfg.Model,
	})
	if err != nil {
		return nil, fmt.Errorf("llm source: %w", err)
	}

	spec, err := parseResponse(resp.Content)
	if err != nil {
		return nil, fmt.Errorf("llm source: parse: %w", err)
	}
	spec.Name = tool
	return spec, nil
}

// llmResult mirrors the JSON shape we ask the LLM to produce.
type llmResult struct {
	ErrorPatterns []toolspec.ErrorPattern `json:"error_patterns"`
	Workflows     []toolspec.Workflow     `json:"workflows"`
	Commands      []toolspec.Command      `json:"commands"`
}

// parseResponse strips optional markdown fences and unmarshals the JSON.
func parseResponse(raw string) (*toolspec.ToolSpec, error) {
	body := strings.TrimSpace(raw)

	// Strip ```json ... ``` fences.
	if strings.HasPrefix(body, "```") {
		if idx := strings.Index(body[3:], "\n"); idx >= 0 {
			body = body[3+idx+1:]
		}
		body = strings.TrimSuffix(body, "```")
		body = strings.TrimSpace(body)
	}

	var r llmResult
	if err := json.Unmarshal([]byte(body), &r); err != nil {
		return nil, err
	}

	prov := &toolspec.Provenance{
		Source:      "llm",
		RetrievedAt: time.Now().UTC().Format(time.RFC3339),
		Confidence:  0.6,
	}

	for i := range r.ErrorPatterns {
		r.ErrorPatterns[i].Provenance = prov
	}
	for i := range r.Workflows {
		r.Workflows[i].Provenance = prov
	}

	return &toolspec.ToolSpec{
		ErrorPatterns: r.ErrorPatterns,
		Workflows:     r.Workflows,
		Commands:      r.Commands,
	}, nil
}
