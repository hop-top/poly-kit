// Package routellm adapts a RouteLLM server to the llm provider interfaces.
//
// Registers scheme: routellm.
// URI format: routellm://router_name:threshold (e.g. routellm://mf:0.7).
//
// The adapter delegates HTTP completions to an inner openai adapter pointed
// at the RouteLLM server, translating the URI model into the server's
// expected "router-[name]-[threshold]" format.
package routellm

import (
	"context"
	"fmt"
	"strconv"
	"strings"

	"hop.top/kit/go/ai/llm"
	llmerrors "hop.top/kit/go/ai/llm/errors"
	"hop.top/kit/go/ai/llm/openai"
)

func init() {
	llm.Register("routellm", New)
}

// Adapter wraps an inner openai adapter for HTTP completions to a RouteLLM
// server and the parsed RouterConfig.
type Adapter struct {
	inner      llm.Provider
	config     RouterConfig
	routerName string
	threshold  float64
}

// compile-time interface checks.
var (
	_ llm.Provider   = (*Adapter)(nil)
	_ llm.Completer  = (*Adapter)(nil)
	_ llm.Streamer   = (*Adapter)(nil)
	_ llm.ToolCaller = (*Adapter)(nil)
)

// New creates an Adapter from the resolved config.
//
// It parses the URI model field as "router_name:threshold", validates the
// threshold is in [0,1], and creates an inner openai adapter pointed at
// the RouteLLM server's base_url.
func New(cfg llm.ResolvedConfig) (llm.Provider, error) {
	rcfg, err := ParseRouterConfig(cfg.Provider.Extras)
	if err != nil {
		return nil, fmt.Errorf("routellm: %w", err)
	}

	// Parse model field as "router_name:threshold".
	model := cfg.Provider.Model
	if model == "" {
		model = cfg.URI.Model
	}

	routerName, threshold, err := parseModelField(model)
	if err != nil {
		return nil, fmt.Errorf("routellm: %w", err)
	}

	if threshold < 0 || threshold > 1 {
		return nil, fmt.Errorf(
			"routellm: threshold %.4f out of range [0, 1]", threshold,
		)
	}

	// Build model name in routellm server format: router-[name]-[threshold].
	serverModel := fmt.Sprintf("router-%s-%s",
		routerName, strconv.FormatFloat(threshold, 'f', -1, 64),
	)

	// Resolve base URL: explicit provider > router config > default.
	baseURL := cfg.Provider.BaseURL
	if baseURL == "" {
		baseURL = rcfg.BaseURL
	}
	if baseURL == "" {
		baseURL = "http://localhost:6060"
	}

	// Ensure /v1 suffix without double-appending.
	if !strings.HasSuffix(baseURL, "/v1") {
		baseURL += "/v1"
	}

	innerCfg := llm.ResolvedConfig{
		URI: cfg.URI,
		Provider: llm.ProviderConfig{
			APIKey:  cfg.Provider.APIKey,
			BaseURL: baseURL,
			Model:   serverModel,
		},
	}

	inner, err := openai.New(innerCfg)
	if err != nil {
		return nil, fmt.Errorf("routellm: create inner adapter: %w", err)
	}

	return &Adapter{
		inner:      inner,
		config:     rcfg,
		routerName: routerName,
		threshold:  threshold,
	}, nil
}

// parseModelField splits "router_name:threshold" into its components.
func parseModelField(model string) (string, float64, error) {
	if model == "" {
		return "", 0, fmt.Errorf("model field is required (format: router_name:threshold)")
	}

	parts := strings.SplitN(model, ":", 2)
	if len(parts) != 2 || parts[0] == "" || parts[1] == "" {
		return "", 0, fmt.Errorf(
			"invalid model %q: expected format router_name:threshold (e.g. mf:0.7)",
			model,
		)
	}

	threshold, err := strconv.ParseFloat(parts[1], 64)
	if err != nil {
		return "", 0, fmt.Errorf(
			"invalid threshold %q: %w", parts[1], err,
		)
	}

	return parts[0], threshold, nil
}

// Close closes the inner adapter.
func (a *Adapter) Close() error {
	return a.inner.Close()
}

// Complete delegates to the inner openai adapter.
func (a *Adapter) Complete(
	ctx context.Context, req llm.Request,
) (llm.Response, error) {
	c, ok := a.inner.(llm.Completer)
	if !ok {
		return llm.Response{}, llmerrors.NewCapabilityNotSupported(
			"complete", "routellm/inner",
		)
	}
	return c.Complete(ctx, req)
}

// Stream delegates to the inner openai adapter.
func (a *Adapter) Stream(
	ctx context.Context, req llm.Request,
) (llm.TokenIterator, error) {
	s, ok := a.inner.(llm.Streamer)
	if !ok {
		return nil, llmerrors.NewCapabilityNotSupported(
			"stream", "routellm/inner",
		)
	}
	return s.Stream(ctx, req)
}

// CallWithTools delegates to the inner openai adapter.
func (a *Adapter) CallWithTools(
	ctx context.Context, req llm.Request, tools []llm.ToolDef,
) (llm.ToolResponse, error) {
	tc, ok := a.inner.(llm.ToolCaller)
	if !ok {
		return llm.ToolResponse{}, llmerrors.NewCapabilityNotSupported(
			"call_with_tools", "routellm/inner",
		)
	}
	return tc.CallWithTools(ctx, req, tools)
}
