// Package routellm adapts a RouteLLM server to the llm provider interfaces.
//
// Registers scheme: routellm.
//
// The model field takes either of the two shapes a RouteLLM server
// advertises on /v1/models:
//
//   - router_name:threshold — routellm://mf:0.7, translated to the
//     server's "router-[name]-[threshold]" model string. The client
//     picks the router and the threshold.
//   - tier name — routellm://private, sent verbatim. The server
//     resolves router and threshold from its own tier config, so the
//     client supplies only the name.
//
// The adapter delegates HTTP completions to an inner openai adapter
// pointed at the RouteLLM server.
package routellm

import (
	"context"
	"fmt"
	"math"
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
//
// routerName and threshold describe a client-selected router and are set
// only for the router_name:threshold shape. For a tier the server owns
// both, so they stay at their zero values and tier holds the name; use
// [Adapter.IsTier] rather than reading threshold == 0, which is also a
// legitimate client-selected threshold.
type Adapter struct {
	inner      llm.Provider
	config     RouterConfig
	routerName string
	threshold  float64
	tier       string
}

// IsTier reports whether the adapter targets a server-resolved tier
// rather than a client-selected router_name:threshold pair.
func (a *Adapter) IsTier() bool { return a.tier != "" }

// compile-time interface checks.
var (
	_ llm.Provider   = (*Adapter)(nil)
	_ llm.Completer  = (*Adapter)(nil)
	_ llm.Streamer   = (*Adapter)(nil)
	_ llm.ToolCaller = (*Adapter)(nil)
)

// New creates an Adapter from the resolved config.
//
// It parses the URI model field as either a router_name:threshold pair
// (validating the threshold is in [0,1]) or a tier name, and creates an
// inner openai adapter pointed at the RouteLLM server's base_url.
func New(cfg llm.ResolvedConfig) (llm.Provider, error) {
	rcfg, err := ParseRouterConfig(cfg.Provider.Extras)
	if err != nil {
		return nil, fmt.Errorf("routellm: %w", err)
	}

	model := cfg.Provider.Model
	if model == "" {
		model = cfg.URI.Model
	}

	parsed, err := parseModelField(model)
	if err != nil {
		return nil, fmt.Errorf("routellm: %w", err)
	}

	// Build the model string the server expects. A tier travels
	// verbatim; a router pair becomes "router-[name]-[threshold]".
	serverModel := parsed.tier
	if !parsed.isTier {
		if parsed.threshold < 0 || parsed.threshold > 1 {
			return nil, fmt.Errorf(
				"routellm: threshold %.4f out of range [0, 1]",
				parsed.threshold,
			)
		}
		serverModel = fmt.Sprintf("router-%s-%s",
			parsed.routerName,
			strconv.FormatFloat(parsed.threshold, 'f', -1, 64),
		)
	}

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
		routerName: parsed.routerName,
		threshold:  parsed.threshold,
		tier:       parsed.tier,
	}, nil
}

// parsedModel is the result of classifying the model field. Exactly one
// of the two shapes is populated: isTier selects tier, otherwise
// routerName and threshold hold the client-selected pair.
type parsedModel struct {
	isTier     bool
	tier       string
	routerName string
	threshold  float64
}

// parseModelField classifies the model field as a router_name:threshold
// pair or a tier name.
//
// # Disambiguation
//
// A RouteLLM server advertises both shapes on /v1/models — bare tier
// names ("private", "coding_fast") alongside "router-mf-0.5" — and a
// tier name is an operator-chosen string that may contain anything,
// colons included. The two shapes are therefore told apart by the only
// part that is structurally constrained: the text after the *last*
// colon must parse as a float for the model to be a router pair.
// Everything else is a tier, passed through verbatim.
//
// Splitting on the last colon rather than the first means a tier named
// "a:b" is not mistaken for router "a" with threshold "b" — "b" does
// not parse, so the whole string stays a tier. It also lets a router
// name itself contain a colon, which SplitN on the first colon could
// not express.
//
// Consequently "mf:notanumber" is now a tier rather than the error it
// used to be. That is the deliberate trade: the server holds the list
// of real tier names and this adapter does not, so guessing that a
// non-numeric suffix "meant" a threshold would reject model names the
// server accepts. An unknown tier fails at the server with its own
// model-not-found error, which names the actual problem, where a
// client-side threshold-parse error would have misdescribed it.
func parseModelField(model string) (parsedModel, error) {
	if model == "" {
		return parsedModel{}, fmt.Errorf(
			"model field is required (router_name:threshold or tier name)",
		)
	}

	// idx > 0 rejects a leading colon (empty router name); the upper
	// bound rejects a trailing one (empty threshold). Both stay tiers.
	if idx := strings.LastIndex(model, ":"); idx > 0 && idx < len(model)-1 {
		// ParseFloat also accepts "NaN" and "Inf". Neither is a
		// threshold, and NaN would slip past the [0,1] range check
		// (every comparison against it is false), so exclude both
		// here — they fall through and become tiers.
		threshold, err := strconv.ParseFloat(model[idx+1:], 64)
		if err == nil && !math.IsNaN(threshold) && !math.IsInf(threshold, 0) {
			return parsedModel{
				routerName: model[:idx],
				threshold:  threshold,
			}, nil
		}
	}

	return parsedModel{isTier: true, tier: model}, nil
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
