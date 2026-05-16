// Package cel is the CEL backend for runtime/policy.
//
// Isolates github.com/google/cel-go transitive deps from the parent
// policy package so callers wiring a non-CEL evaluator avoid them.
package cel

import (
	"fmt"
	"sync"

	celgo "github.com/google/cel-go/cel"
)

// Evaluator compiles + evaluates CEL boolean expressions against the
// activation map shape policy.Engine builds (principal/resource/
// context/payload). One Evaluator per Engine; goroutine-safe.
type Evaluator struct {
	env *celgo.Env

	mu    sync.RWMutex
	progs map[string]celgo.Program // policy name → compiled program
}

// New returns a fresh Evaluator with the standard activation shape
// declared (principal, resource, context, payload, stage as dyn maps).
//
// The `stage` binding is populated by runtime/policy when a stage
// resolver is wired (see policy.WithStageResolver). Policies that don't
// reference `stage` see an empty map and pay no cost.
func New() (*Evaluator, error) {
	env, err := celgo.NewEnv(
		celgo.Variable("principal", celgo.MapType(celgo.StringType, celgo.DynType)),
		celgo.Variable("resource", celgo.MapType(celgo.StringType, celgo.DynType)),
		celgo.Variable("context", celgo.MapType(celgo.StringType, celgo.DynType)),
		celgo.Variable("payload", celgo.MapType(celgo.StringType, celgo.DynType)),
		celgo.Variable("stage", celgo.MapType(celgo.StringType, celgo.DynType)),
		celgo.Variable("entity", celgo.MapType(celgo.StringType, celgo.DynType)),
	)
	if err != nil {
		return nil, fmt.Errorf("policy/cel: env: %w", err)
	}
	return &Evaluator{env: env, progs: map[string]celgo.Program{}}, nil
}

// Compile parses+type-checks expr, caches the resulting Program under
// name, and returns any compile error. Re-Compile under the same name
// replaces the cached program.
func (e *Evaluator) Compile(name, expr string) error {
	ast, iss := e.env.Compile(expr)
	if iss != nil && iss.Err() != nil {
		return fmt.Errorf("policy/cel: compile %q: %w", name, iss.Err())
	}
	prg, err := e.env.Program(ast)
	if err != nil {
		return fmt.Errorf("policy/cel: program %q: %w", name, err)
	}
	e.mu.Lock()
	e.progs[name] = prg
	e.mu.Unlock()
	return nil
}

// Eval runs the cached program for name against activation. Returns
// (matched, error). Missing program returns an error — callers should
// Compile every policy at engine init.
func (e *Evaluator) Eval(name string, activation map[string]any) (bool, error) {
	e.mu.RLock()
	prg, ok := e.progs[name]
	e.mu.RUnlock()
	if !ok {
		return false, fmt.Errorf("policy/cel: no compiled program for %q", name)
	}
	out, _, err := prg.Eval(activation)
	if err != nil {
		return false, fmt.Errorf("policy/cel: eval %q: %w", name, err)
	}
	b, ok := out.Value().(bool)
	if !ok {
		return false, fmt.Errorf("policy/cel: %q did not return bool (got %T)", name, out.Value())
	}
	return b, nil
}
