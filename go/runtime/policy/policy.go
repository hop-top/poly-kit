// Package policy is a host-tool-agnostic guard engine wired to kit's
// pre-* event seams. Adopters declare expression rules in YAML; the
// engine subscribes via kit/runtime/bus and vetoes mutating ops by
// returning a PolicyDeniedError (wraps domain.ErrConflict).
//
// Three veto-able topics are watched:
//
//   - kit.runtime.state.pre_transitioned  (state machine)
//   - kit.runtime.entity.pre_validated    (entity CRUD, raw input)
//   - kit.runtime.entity.pre_persisted    (entity CRUD, validated)
//
// Default for events with zero matching policies: ALLOW. Composition
// across N matching policies is deny-overrides; first-deny wins error.
//
// The core package is evaluator-agnostic: callers MUST supply an
// Evaluator. Use the policy/withcel subpackage for the CEL backend, or
// pass WithEvaluator to plug Cedar/OPA/etc.
//
// Wiring example with default CEL backend:
//
//	cfg, _ := policy.LoadConfig("policies.yaml")
//	eng,  _ := withcel.New(cfg)
//	policy.Wire(b, eng) // b is bus.Bus
//
// Wiring example with a custom evaluator:
//
//	eng, _ := policy.NewEngine(cfg, policy.WithEvaluator(myEval))
package policy

import (
	"context"
	"errors"
	"fmt"
)

// Decision is the outcome of evaluating a single Policy.
type Decision string

// Decision values.
const (
	DecisionAllow Decision = "allow"
	DecisionDeny  Decision = "deny"
	// DecisionSkip means the policy did not match (engine treats it as
	// allow when no other policy denies — see deny-overrides).
	DecisionSkip Decision = "skip"
)

// Effect is allow|deny — the resolution of a policy when its 'when'
// expression evaluates true (effect) or false (otherwise).
type Effect string

// Effect values.
const (
	EffectAllow Effect = "allow"
	EffectDeny  Effect = "deny"
)

// Policy is one declarative rule.
type Policy struct {
	Name      string
	On        string // kit topic this policy matches
	When      string // CEL expression
	Effect    Effect // resolution when When == true
	Otherwise Effect // resolution when When == false
	Message   string // surfaces in PolicyDeniedError

	// stageDriven is set at engine init when When references `stage`;
	// the violated emit path consults this flag to avoid emitting on
	// non-stage denials. Detection is a word-boundary scan — see
	// referencesStage().
	stageDriven bool
}

// stageGatedTopics enumerates the topics where a stage-driven denial
// triggers a kit.runtime.stage.violated emit. Mirrors allowedTopics in
// config.go (the three veto-able pre-* seams).
var stageGatedTopics = map[string]struct{}{
	"kit.runtime.state.pre_transitioned": {},
	"kit.runtime.entity.pre_validated":   {},
	"kit.runtime.entity.pre_persisted":   {},
}

// referencesStage reports whether a CEL expression references the
// `stage` binding. Word-boundary scan — matches `stage.mode`, `stage[`,
// `stage ==`, etc.; does NOT match `mystage` or `stage_thing`. Cheap
// and good-enough; the policy engine doesn't need to parse CEL itself.
func referencesStage(expr string) bool {
	const tok = "stage"
	for i := 0; i+len(tok) <= len(expr); i++ {
		// Match must start at a word boundary.
		if i > 0 {
			pc := expr[i-1]
			if isIdentChar(pc) {
				continue
			}
		}
		if expr[i:i+len(tok)] != tok {
			continue
		}
		// And end at a word boundary (i.e. the next char is not an
		// identifier char). End-of-string also counts as a boundary.
		if i+len(tok) == len(expr) {
			return true
		}
		nc := expr[i+len(tok)]
		if !isIdentChar(nc) {
			return true
		}
	}
	return false
}

// isIdentChar reports whether c is a CEL/identifier character.
func isIdentChar(c byte) bool {
	switch {
	case c >= 'a' && c <= 'z':
	case c >= 'A' && c <= 'Z':
	case c >= '0' && c <= '9':
	case c == '_':
	default:
		return false
	}
	return true
}

// Evaluator is the pluggable expression backend. The policy/withcel
// subpackage supplies a CEL implementation; adopters could wire
// Cedar/OPA via the same shape.
type Evaluator interface {
	Compile(name, expr string) error
	Eval(name string, activation map[string]any) (bool, error)
}

// Engine evaluates compiled policies against bus events.
type Engine struct {
	policies   []Policy
	eval       Evaluator
	princ      PrincipalResolver
	stageRes   StageResolver
	scopeRes   ScopeResolver
	violateOut ViolationPublisher
}

// StageResolver returns the stage map binding (matches the stage.yaml
// documented shape: mode/since/until/reason/allow/deny) for the given
// scope. Policies that reference `stage.mode` get a real value through
// this. Returning an empty map signals "no state set" — the engine
// passes it to CEL as-is.
//
// Adopters typically wire this to stage.Read(scope) by re-marshaling
// the resulting State to a map. Kept as a function (not a hard import
// of core/stage) so policy stays optional-dep on the stage primitive
// for adopters that only want principal/resource gating.
type StageResolver func(scope string) (map[string]any, error)

// ScopeResolver extracts the scope identifier from an event's
// activation map. Default reads payload.scope, then resource.id, then
// returns "" — adopters override when their event payloads carry the
// scope under a different key.
type ScopeResolver func(activation map[string]any) string

// ViolationPublisher publishes the kit.runtime.stage.violated event
// when a stage-driven rule denies. nil = no emit (preserves current
// behavior).
type ViolationPublisher interface {
	Publish(topic string, payload any)
}

// PrincipalResolver returns the principal for the given context. The
// subscriber wiring calls this per event; the host tool can stuff a
// principal into ctx (or return one from env/static config). Default
// resolver checks ContextPrincipalKey on ctx then KIT_POLICY_ROLE env.
type PrincipalResolver func(ctx context.Context) Principal

// Principal is the actor exposed to CEL via the 'principal' binding.
type Principal struct {
	ID     string
	Role   string
	Source string // "ctx" | "env" | "none" — how role was resolved
}

// EngineOption configures an Engine.
type EngineOption func(*Engine)

// WithEvaluator installs the expression backend. Required: NewEngine
// returns an error if no evaluator is configured. Use the policy/withcel
// subpackage for the CEL backend, or supply your own implementation.
func WithEvaluator(ev Evaluator) EngineOption {
	return func(e *Engine) { e.eval = ev }
}

// WithPrincipalResolver overrides the default principal resolver.
func WithPrincipalResolver(p PrincipalResolver) EngineOption {
	return func(e *Engine) { e.princ = p }
}

// WithStageResolver wires a function that returns the `stage` CEL
// binding for a given scope. Required if any policy references the
// `stage` token (e.g. stage.yaml rules); with no resolver, every
// stage-driven policy sees an empty map and matches nothing.
func WithStageResolver(r StageResolver) EngineOption {
	return func(e *Engine) { e.stageRes = r }
}

// WithScopeResolver overrides the default scope-extraction function.
// Default tries activation["payload"]["scope"], then activation
// ["resource"]["id"], then "".
func WithScopeResolver(r ScopeResolver) EngineOption {
	return func(e *Engine) { e.scopeRes = r }
}

// WithViolationPublisher wires a publisher that receives the
// kit.runtime.stage.violated event when a stage-driven policy denies.
// Nil = no emit (default, preserves current behavior — denials still
// surface via PolicyDeniedError).
func WithViolationPublisher(p ViolationPublisher) EngineOption {
	return func(e *Engine) { e.violateOut = p }
}

// NewEngine compiles every policy in cfg and returns an Engine. An
// Evaluator MUST be supplied via WithEvaluator (or use withcel.New for
// the CEL backend); NewEngine returns an error otherwise. Compile
// errors surface here, not at first event, matching kit's fail-loud
// convention for boot-time misconfig.
func NewEngine(cfg *Config, opts ...EngineOption) (*Engine, error) {
	if cfg == nil {
		return nil, errors.New("policy: nil config")
	}
	e := &Engine{policies: cfg.Policies, princ: DefaultPrincipalResolver}
	for _, o := range opts {
		o(e)
	}
	if e.eval == nil {
		return nil, errors.New("policy: no evaluator configured (use withcel.New or pass policy.WithEvaluator)")
	}
	for i := range e.policies {
		p := &e.policies[i]
		if err := e.eval.Compile(p.Name, p.When); err != nil {
			return nil, fmt.Errorf("policy: %w", err)
		}
		p.stageDriven = referencesStage(p.When)
	}
	return e, nil
}

// Decide evaluates all policies on `topic` against activation. Returns
// nil when allowed (including the zero-matching-policies case).
//
// Composition: deny-overrides. Every matching policy is evaluated
// (no short-circuit on first allow) so audit hooks see the full set;
// the first denying policy supplies the returned error.
//
// Side effect: when a stage-driven policy denies on one of the
// stage-gated topics (state.pre_transitioned / entity.pre_validated /
// entity.pre_persisted) AND a ViolationPublisher is wired, Decide
// publishes kit.runtime.stage.violated with the denied policy's
// metadata. The denial still surfaces via PolicyDeniedError; the emit
// is for ops dashboards / audit fanout.
func (e *Engine) Decide(topic string, activation map[string]any) error {
	// Populate the `stage` binding before running CEL so policies that
	// reference it see the scope's current state. Skipped when no
	// resolver is wired (the default empty map keeps non-stage CEL
	// expressions cheap).
	e.populateStageBinding(topic, activation)

	var firstDeny *PolicyDeniedError
	var firstDenyPolicy *Policy
	for i := range e.policies {
		p := &e.policies[i]
		if p.On != topic {
			continue
		}
		matched, err := e.eval.Eval(p.Name, activation)
		if err != nil {
			if firstDeny == nil {
				firstDeny = &PolicyDeniedError{
					PolicyName: p.Name,
					Topic:      topic,
					Message:    fmt.Sprintf("evaluation error: %v", err),
					Decision:   DecisionDeny,
				}
				firstDenyPolicy = p
			}
			continue
		}
		eff := p.Otherwise
		if matched {
			eff = p.Effect
		}
		if eff == EffectDeny && firstDeny == nil {
			firstDeny = &PolicyDeniedError{
				PolicyName: p.Name,
				Topic:      topic,
				Message:    p.Message,
				Decision:   DecisionDeny,
			}
			firstDenyPolicy = p
		}
	}
	if firstDeny != nil {
		e.maybeEmitViolated(topic, activation, firstDenyPolicy, firstDeny)
		return firstDeny
	}
	return nil
}

// populateStageBinding resolves the scope from activation, calls the
// configured StageResolver, and stores the result under
// activation["stage"]. Idempotent — overwrites any prior value to keep
// the binding authoritative.
func (e *Engine) populateStageBinding(topic string, activation map[string]any) {
	if e.stageRes == nil {
		// No resolver: ensure `stage` is at least an empty map so CEL
		// doesn't blow up on unknown bindings.
		if _, ok := activation["stage"]; !ok {
			activation["stage"] = map[string]any{}
		}
		return
	}
	scope := e.resolveScope(activation)
	if scope == "" {
		activation["stage"] = map[string]any{}
		return
	}
	st, err := e.stageRes(scope)
	if err != nil || st == nil {
		activation["stage"] = map[string]any{}
		return
	}
	activation["stage"] = st
}

// resolveScope extracts the scope identifier from activation. Default
// reads payload.scope, then resource.id; adopters override with
// WithScopeResolver.
func (e *Engine) resolveScope(activation map[string]any) string {
	if e.scopeRes != nil {
		return e.scopeRes(activation)
	}
	if pl, ok := activation["payload"].(map[string]any); ok {
		if s, ok := pl["scope"].(string); ok && s != "" {
			return s
		}
	}
	if rs, ok := activation["resource"].(map[string]any); ok {
		if s, ok := rs["id"].(string); ok && s != "" {
			return s
		}
	}
	return ""
}

// maybeEmitViolated publishes kit.runtime.stage.violated when:
//   - a ViolationPublisher is wired, AND
//   - the denied policy referenced the `stage` binding (stage-driven), AND
//   - the topic is one of the stage-gated pre-* seams.
//
// All three guards must hold to avoid emit storms on non-stage denials.
func (e *Engine) maybeEmitViolated(topic string, activation map[string]any, p *Policy, denied *PolicyDeniedError) {
	if e.violateOut == nil || p == nil || !p.stageDriven {
		return
	}
	if _, ok := stageGatedTopics[topic]; !ok {
		return
	}
	scope := e.resolveScope(activation)
	stageVal := ""
	if st, ok := activation["stage"].(map[string]any); ok {
		if m, ok := st["mode"].(string); ok {
			stageVal = m
		}
	}
	entityKind := ""
	if ent, ok := activation["entity"].(map[string]any); ok {
		if k, ok := ent["kind"].(string); ok {
			entityKind = k
		}
	}
	if entityKind == "" {
		if rs, ok := activation["resource"].(map[string]any); ok {
			if k, ok := rs["kind"].(string); ok {
				entityKind = k
			}
		}
	}
	principal := ""
	if pr, ok := activation["principal"].(map[string]any); ok {
		if id, ok := pr["id"].(string); ok {
			principal = id
		}
	}
	payload := ViolationPayload{
		Scope:     scope,
		Stage:     stageVal,
		Topic:     topic,
		Entity:    entityKind,
		Principal: principal,
		Message:   denied.Message,
	}
	e.violateOut.Publish("kit.runtime.stage.violated", payload)
}

// ViolationPayload is the concrete payload published on
// kit.runtime.stage.violated by Decide. Mirrors core/stage's
// ViolatedPayload field-for-field — kept here to keep the policy
// package import-cycle-free with respect to core/stage's compiled
// dependency on bus topic strings.
type ViolationPayload struct {
	Scope     string `json:"scope"`
	Stage     string `json:"stage"`
	Topic     string `json:"topic"`
	Entity    string `json:"entity,omitempty"`
	Principal string `json:"principal,omitempty"`
	Message   string `json:"message,omitempty"`
}
