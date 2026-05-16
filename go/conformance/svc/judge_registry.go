package svc

import (
	"context"
	"errors"
	"fmt"
	"os"
	"sort"
	"sync"

	"gopkg.in/yaml.v3"
)

// ErrModelNotRegistered signals a missing model lookup. Handlers map
// this to CodeJudgeUnavailable (502) per design §10.
var ErrModelNotRegistered = errors.New("judge: model not registered")

// ModelRegistry resolves a named model into an AIJudge implementation.
//
// Production providers (Anthropic, OpenAI, Bedrock, local) plug in via
// follow-up tracks. v1 ships three bootstrap registries:
//   - NullRegistry  — rejects all (default, refuse-by-default)
//   - StubRegistry  — canned responses for adopter dev + tests
//   - ConfigRegistry — loads judges.yaml at boot; provider impls hook
//     in later
type ModelRegistry interface {
	Resolve(modelName string) (AIJudge, error)
	RegisteredModels() []string
}

// NullRegistry rejects every Resolve call. This is the production
// default until an operator explicitly configures judges via
// judges.yaml. Refuse-by-default keeps AI-judged scenarios disabled
// out-of-the-box.
type NullRegistry struct{}

// Resolve always returns ErrModelNotRegistered.
func (NullRegistry) Resolve(_ string) (AIJudge, error) { return nil, ErrModelNotRegistered }

// RegisteredModels returns an empty list.
func (NullRegistry) RegisteredModels() []string { return nil }

// StubRegistry returns deterministic canned responses for testing and
// adopter dev. Score returns a fixed verdict regardless of input.
type StubRegistry struct {
	// CannedVerdict drives the default judge verdict; "pass" if empty.
	CannedVerdict string
	// CannedScore is the score returned; 1.0 if zero.
	CannedScore float64
	// Models is the set of names callers may resolve. Empty = match any.
	Models []string
}

// Resolve returns a stub AIJudge.
func (s StubRegistry) Resolve(model string) (AIJudge, error) {
	if len(s.Models) > 0 {
		ok := false
		for _, m := range s.Models {
			if m == model {
				ok = true
				break
			}
		}
		if !ok {
			return nil, ErrModelNotRegistered
		}
	}
	v := s.CannedVerdict
	if v == "" {
		v = "pass"
	}
	score := s.CannedScore
	if score == 0 {
		score = 1.0
	}
	return stubJudge{model: model, verdict: v, score: score}, nil
}

// RegisteredModels returns s.Models or ["stub"] when empty.
func (s StubRegistry) RegisteredModels() []string {
	if len(s.Models) > 0 {
		return s.Models
	}
	return []string{"stub"}
}

type stubJudge struct {
	model   string
	verdict string
	score   float64
}

func (j stubJudge) Score(_ context.Context, req JudgeRequest) (JudgeResponse, error) {
	return JudgeResponse{
		Verdict:   j.verdict,
		Score:     j.score,
		Rationale: fmt.Sprintf("stub verdict for %s", j.model),
		TokensIn:  len(req.Prompt) + len(req.CapturedText),
		TokensOut: 8,
	}, nil
}

// ConfigRegistry loads judges.yaml at boot. Each entry declares a
// provider + the env var that carries the API key. The actual provider
// impls (anthropic, openai, etc.) are registered via RegisterProvider;
// v1 ships nothing pre-registered.
//
// judges.yaml shape:
//
//	models:
//	  claude-sonnet-4:
//	    provider: anthropic
//	    api_key_env: ANTHROPIC_API_KEY
//	  gpt-4o:
//	    provider: openai
//	    api_key_env: OPENAI_API_KEY
type ConfigRegistry struct {
	mu        sync.RWMutex
	models    map[string]ConfigModelEntry
	providers map[string]ProviderFactory
}

// ConfigModelEntry is one row from judges.yaml.
type ConfigModelEntry struct {
	Provider  string `yaml:"provider"`
	APIKeyEnv string `yaml:"api_key_env"`
}

// ProviderFactory builds an AIJudge from a model entry. Provider
// follow-up tracks (anthropic, openai, …) call RegisterProvider at
// init() time.
type ProviderFactory func(modelName string, entry ConfigModelEntry) (AIJudge, error)

// NewConfigRegistry loads judges.yaml from path. Returns an empty
// registry when path is empty.
func NewConfigRegistry(path string) (*ConfigRegistry, error) {
	r := &ConfigRegistry{
		models:    make(map[string]ConfigModelEntry),
		providers: make(map[string]ProviderFactory),
	}
	if path == "" {
		return r, nil
	}
	b, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("configregistry: read %s: %w", path, err)
	}
	var doc struct {
		Models map[string]ConfigModelEntry `yaml:"models"`
	}
	if err := yaml.Unmarshal(b, &doc); err != nil {
		return nil, fmt.Errorf("configregistry: parse: %w", err)
	}
	for k, v := range doc.Models {
		r.models[k] = v
	}
	return r, nil
}

// RegisterProvider associates a provider name with its factory. Called
// by provider impls (e.g. the anthropic follow-up) at boot.
func (r *ConfigRegistry) RegisterProvider(name string, factory ProviderFactory) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.providers[name] = factory
}

// Resolve implements ModelRegistry.
func (r *ConfigRegistry) Resolve(model string) (AIJudge, error) {
	r.mu.RLock()
	entry, ok := r.models[model]
	factory := r.providers[entry.Provider]
	r.mu.RUnlock()
	if !ok {
		return nil, ErrModelNotRegistered
	}
	if factory == nil {
		return nil, fmt.Errorf("judge: provider %q not loaded (follow-up track required)", entry.Provider)
	}
	return factory(model, entry)
}

// RegisteredModels returns the configured model names.
func (r *ConfigRegistry) RegisteredModels() []string {
	r.mu.RLock()
	defer r.mu.RUnlock()
	out := make([]string, 0, len(r.models))
	for k := range r.models {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}
