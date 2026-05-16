package router

import (
	"context"
	"fmt"
	"strconv"
	"strings"
	"sync"

	"hop.top/kit/go/ai/llm"
)

// Controller manages router selection, threshold parsing, middleware chains,
// and delegates completions to the appropriate model via an llm.Provider.
type Controller struct {
	mu          sync.RWMutex
	registry    *Registry
	defaultPair ModelPair
	middleware  []Middleware
	provider    llm.Provider
	modelCounts map[string]map[string]int
}

// ControllerOption configures a Controller.
type ControllerOption func(*Controller)

// WithMiddleware appends middleware to the controller chain.
func WithMiddleware(mw ...Middleware) ControllerOption {
	return func(c *Controller) {
		c.middleware = append(c.middleware, mw...)
	}
}

// WithProvider sets the LLM provider used for completions.
func WithProvider(p llm.Provider) ControllerOption {
	return func(c *Controller) {
		c.provider = p
	}
}

// NewController creates a Controller with the given registry, default model
// pair, and options.
func NewController(
	reg *Registry,
	defaultPair ModelPair,
	opts ...ControllerOption,
) *Controller {
	c := &Controller{
		registry:    reg,
		defaultPair: defaultPair,
		modelCounts: make(map[string]map[string]int),
	}
	for _, o := range opts {
		o(c)
	}
	return c
}

// ParseModelString parses a model string of the form "router-<name>-<threshold>"
// and returns the router name and threshold. The threshold must be in [0,1].
func ParseModelString(model string) (routerName string, threshold float64, err error) {
	if !strings.HasPrefix(model, "router-") {
		return "", 0, NewRoutingError(fmt.Sprintf(
			"invalid model %q: must start with 'router-'", model,
		))
	}

	rest := model[len("router-"):]
	lastDash := strings.LastIndex(rest, "-")
	if lastDash < 0 || lastDash == 0 || lastDash == len(rest)-1 {
		return "", 0, NewRoutingError(fmt.Sprintf(
			"invalid model %q: expected format 'router-<name>-<threshold>'",
			model,
		))
	}

	routerName = rest[:lastDash]
	thresholdStr := rest[lastDash+1:]

	threshold, err = strconv.ParseFloat(thresholdStr, 64)
	if err != nil {
		return "", 0, NewRoutingError(fmt.Sprintf(
			"invalid threshold %q: %v", thresholdStr, err,
		))
	}

	if threshold < 0 || threshold > 1 {
		return "", 0, NewRoutingError(fmt.Sprintf(
			"threshold %.4f out of [0,1] range", threshold,
		))
	}

	return routerName, threshold, nil
}

// Route scores the signal with the named router and returns the selected
// model name (strong or weak). Routers implementing [ModalRouter] receive
// the full signal; plain [Router] implementations receive only the text.
func (c *Controller) Route(
	ctx context.Context,
	sig UserSignal,
	routerName string,
	threshold float64,
) (string, error) {
	router, err := c.validateRouterThreshold(routerName, threshold)
	if err != nil {
		return "", err
	}

	pair := c.getModelPair(ctx, sig.Text)

	var score float64
	if mr, ok := router.(ModalRouter); ok {
		score, err = mr.ScoreSignal(ctx, sig)
	} else {
		score, err = router.Score(ctx, sig.Text)
	}
	if err != nil {
		return "", fmt.Errorf("router %q scoring failed: %w", routerName, err)
	}

	var selected string
	if score >= threshold {
		selected = pair.Strong
	} else {
		selected = pair.Weak
	}

	c.recordCount(routerName, selected)
	return selected, nil
}

// RouteFromModel parses a model string and routes accordingly.
func (c *Controller) RouteFromModel(
	ctx context.Context, sig UserSignal, model string,
) (string, error) {
	routerName, threshold, err := ParseModelString(model)
	if err != nil {
		return "", err
	}
	return c.Route(ctx, sig, routerName, threshold)
}

// Complete parses the request model string, routes, and delegates to the
// provider. If no provider is set, returns an error.
func (c *Controller) Complete(
	ctx context.Context, req llm.Request,
) (llm.Response, error) {
	if c.provider == nil {
		return llm.Response{}, NewRoutingError("no provider configured")
	}

	comp, ok := c.provider.(llm.Completer)
	if !ok {
		return llm.Response{}, NewRoutingError(
			"provider does not support Complete",
		)
	}

	sig := lastUserSignal(req.Messages)
	selected, err := c.RouteFromModel(ctx, sig, req.Model)
	if err != nil {
		return llm.Response{}, err
	}

	routed := req
	routed.Model = selected
	return comp.Complete(ctx, routed)
}

// ModelCounts returns a snapshot of routing counts per router per model.
func (c *Controller) ModelCounts() map[string]map[string]int {
	c.mu.RLock()
	defer c.mu.RUnlock()
	out := make(map[string]map[string]int, len(c.modelCounts))
	for r, counts := range c.modelCounts {
		m := make(map[string]int, len(counts))
		for model, n := range counts {
			m[model] = n
		}
		out[r] = m
	}
	return out
}

// validateRouterThreshold checks that the router exists and threshold is
// valid. Returns the resolved Router to avoid a redundant registry lookup.
func (c *Controller) validateRouterThreshold(
	routerName string, threshold float64,
) (Router, error) {
	r, err := c.registry.Get(routerName)
	if err != nil {
		return nil, err
	}
	if threshold < 0 || threshold > 1 {
		return nil, NewRoutingError(fmt.Sprintf(
			"threshold %.4f out of [0,1] range", threshold,
		))
	}
	return r, nil
}

// getModelPair applies middleware chain to get the model pair for a prompt.
func (c *Controller) getModelPair(ctx context.Context, prompt string) ModelPair {
	pair := c.defaultPair
	for _, mw := range c.middleware {
		if mp, err := mw.GetModelPair(ctx, prompt); err == nil && mp != nil {
			pair = *mp
		}
	}
	return pair
}

// recordCount increments the routing counter for a router+model.
func (c *Controller) recordCount(routerName, model string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.modelCounts[routerName] == nil {
		c.modelCounts[routerName] = make(map[string]int)
	}
	c.modelCounts[routerName][model]++
}

// UserSignal carries text and modality flags extracted from a user message.
type UserSignal struct {
	Text     string
	HasImage bool
	HasAudio bool
	HasVideo bool
}

// ModalRouter scores using the full multimodal signal. Routers that only
// care about text implement [Router]; those that route on modality
// implement this interface instead (which embeds Router for fallback).
//
// When ScoreSignal is available the controller calls it instead of
// [Router.Score]; Score is only used for plain Router implementations.
type ModalRouter interface {
	Router
	ScoreSignal(ctx context.Context, sig UserSignal) (float64, error)
}

// lastUserSignal extracts text and modality flags from the last user message.
func lastUserSignal(msgs []llm.Message) UserSignal {
	var msg *llm.Message
	for i := len(msgs) - 1; i >= 0; i-- {
		if msgs[i].Role == "user" {
			msg = &msgs[i]
			break
		}
	}
	if msg == nil {
		if len(msgs) > 0 {
			msg = &msgs[len(msgs)-1]
		} else {
			return UserSignal{}
		}
	}

	var sig UserSignal
	if len(msg.Parts) > 0 {
		var b strings.Builder
		for _, p := range msg.Parts {
			switch p.Type {
			case llm.PartTypeText:
				b.WriteString(p.Text)
			case llm.PartTypeImage:
				sig.HasImage = true
			case llm.PartTypeAudio:
				sig.HasAudio = true
			case llm.PartTypeVideo:
				sig.HasVideo = true
			}
		}
		sig.Text = b.String()
	} else {
		sig.Text = msg.Content
	}
	return sig
}
