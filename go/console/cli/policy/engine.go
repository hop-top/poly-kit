package policy

import (
	"path"
	"strings"

	"github.com/spf13/cobra"
)

// sideEffectAnnotation is the cobra command annotation key kit reads
// to discover the declared side-effect class. Mirrored from
// cli.sideEffectAnnotation to break the import cycle; kit reserves
// the exact string under its kit/ prefix.
const sideEffectAnnotation = "kit/side-effect"

// readSideEffect returns the side-effect tag on cmd as a local
// SideEffect value. Returns ("", false) when the annotation is absent.
func readSideEffect(cmd *cobra.Command) (SideEffect, bool) {
	if cmd == nil || cmd.Annotations == nil {
		return "", false
	}
	v, ok := cmd.Annotations[sideEffectAnnotation]
	if !ok {
		return "", false
	}
	return SideEffect(v), true
}

// Engine carries the per-invocation enforcement state. Construct one
// at the start of an invocation; Authorize each command before RunE
// runs and RecordOp after a successful mutating run.
//
// Zero-value Engine corresponds to "no policy loaded" — Authorize
// returns (true, false, "") for any command and RecordOp tracks the
// mutating-op count without an upper bound.
type Engine struct {
	policy   Policy
	opsCount int
	maxOps   int // mirror of policy.MaxOps so MaxOps overrides via the
	// engine constructor (e.g. --max-ops flag) compose cleanly.
}

// NewEngine constructs an Engine bound to p. maxOpsOverride > 0 takes
// precedence over policy.MaxOps so the --max-ops CLI flag can tighten
// (or set, when no policy is loaded) the budget without rewriting the
// loaded YAML. maxOpsOverride == 0 leaves policy.MaxOps in force.
func NewEngine(p Policy, maxOpsOverride int) *Engine {
	max := p.MaxOps
	if maxOpsOverride > 0 {
		max = maxOpsOverride
	}
	return &Engine{policy: p, maxOps: max}
}

// Policy returns the Engine's underlying policy. Useful for adopters
// that need to inspect allow/require_confirm separately (e.g. for
// audit logging).
func (e *Engine) Policy() Policy { return e.policy }

// MaxOps returns the active per-invocation budget. 0 means unlimited.
func (e *Engine) MaxOps() int { return e.maxOps }

// OpsCount returns how many mutating ops have been recorded so far.
func (e *Engine) OpsCount() int { return e.opsCount }

// Authorize decides whether cmd may run under the active policy, for
// a caller the policy knows nothing about: the CLI, or a served call
// whose identity the transport did not establish. It is
// AuthorizeFor(cmd, nil).
//
// allowed is true when the command's side-effect class passes the
// policy's allow rules (or when no policy is loaded — the engine's
// allow map is then nil and we default-permit).
//
// requireConfirm is true when the policy explicitly lists this
// command path under require_confirm. The cli middleware OR's this
// with annotation-driven typed-token requirements.
//
// reason is a short human-friendly explanation populated only when
// allowed=false.
//
// A read-tagged command always returns (true, false, ""). A command
// without a side-effect tag is treated as read for safety; the
// validator catches missing tags separately.
func (e *Engine) Authorize(cmd *cobra.Command) (allowed bool, requireConfirm bool, reason string) {
	return e.AuthorizeFor(cmd, nil)
}

// AuthorizeFor is [Engine.Authorize] for caller c: the first of the
// policy's caller rules matching c answers for the side-effect
// classes it declares, and the policy's own allow map for the rest.
// A nil c is a caller nobody established, answered by the policy's
// own rules alone.
//
// A side-effect tier of the expanded ladder (write-local,
// write-shared, destructive-local, destructive-shared) is answered by
// an allow entry naming it exactly, else by its legacy class's entry
// (write, destructive).
func (e *Engine) AuthorizeFor(cmd *cobra.Command, c *Caller) (allowed bool, requireConfirm bool, reason string) {
	if e == nil {
		return true, false, ""
	}

	se, ok := readSideEffect(cmd)
	if !ok {
		// Untagged: defer to the validator (which refuses missing
		// tags). Allow here so we don't double-error.
		return true, false, ""
	}
	if se == SideEffectRead {
		return true, false, ""
	}

	verb := verbOf(cmd)
	rule, _, matched := e.policy.RuleFor(c)
	if !e.allows(rule.Allow, matched, se, verb) {
		return false, false, "policy: " + string(se) + " not allowed for " + verb
	}

	// require_confirm match: any matching glob bumps the flag.
	if matchAny(e.policy.RequireConfirm, verb) {
		requireConfirm = true
	}
	return true, requireConfirm, ""
}

// allows answers the allow rules for tier se and verb: ruleAllow's
// entry when the matched rule declares one for se, else the policy's
// own. Default-permit when no allow map is loaded; with a loaded
// policy, an empty allow class categorically refuses that class.
func (e *Engine) allows(ruleAllow map[SideEffect][]string, matched bool, se SideEffect, verb string) bool {
	if matched {
		if patterns, declared := lookupClass(ruleAllow, se); declared {
			return matchAny(patterns, verb)
		}
	}
	if patterns, declared := lookupClass(e.policy.Allow, se); declared {
		return matchAny(patterns, verb)
	}
	return true
}

// RefusedForEveryone reports whether the policy refuses cmd whoever
// calls: its own rules refuse it and so does every caller rule. A
// transport uses it to withhold a command from discovery at mount,
// when no caller could run it.
func (e *Engine) RefusedForEveryone(cmd *cobra.Command) bool {
	if e == nil {
		return false
	}
	se, ok := readSideEffect(cmd)
	if !ok || se == SideEffectRead {
		return false
	}
	verb := verbOf(cmd)
	if e.allows(nil, false, se, verb) {
		return false
	}
	for _, r := range e.policy.Callers {
		if e.allows(r.Allow, true, se, verb) {
			return false
		}
	}
	return true
}

// BudgetFor returns the served budget the first caller rule matching
// c gives it. ok is false for a nil caller, when no rule matches, and
// when the matching rule sets no max_ops.
func (e *Engine) BudgetFor(c *Caller) (Budget, bool) {
	if e == nil {
		return Budget{}, false
	}
	rule, index, ok := e.policy.RuleFor(c)
	if !ok || rule.MaxOps <= 0 {
		return Budget{}, false
	}
	window := rule.Window
	if window <= 0 {
		window = DefaultBudgetWindow
	}
	return Budget{Key: e.policy.budgetKey(index, c), MaxOps: rule.MaxOps, Window: window}, true
}

// Mutating reports whether cmd's side-effect tag counts against a
// budget: a write or destructive tier.
func Mutating(cmd *cobra.Command) bool {
	se, ok := readSideEffect(cmd)
	if !ok {
		return false
	}
	class, _ := classOf(se)
	return class == SideEffectWrite || class == SideEffectDestructive
}

// verbOf is the command path minus the root name, so allow globs match
// adopter-friendly paths ("delete:*", not "<tool> delete:*").
func verbOf(cmd *cobra.Command) string {
	cmdPath := cmd.CommandPath()
	if idx := strings.Index(cmdPath, " "); idx >= 0 {
		return strings.TrimSpace(cmdPath[idx+1:])
	}
	return cmdPath
}

// classOf maps a side-effect tier onto its legacy class. known is
// false for a value outside kit's vocabulary, which maps to itself.
func classOf(se SideEffect) (class SideEffect, known bool) {
	switch se {
	case SideEffectRead, SideEffectWrite, SideEffectDestructive, SideEffectInteractive:
		return se, true
	case "write-local", "write-shared":
		return SideEffectWrite, true
	case "destructive-local", "destructive-shared":
		return SideEffectDestructive, true
	}
	return se, false
}

// lookupClass returns allow's entry for tier se: the exact tier first,
// then its legacy class.
func lookupClass(allow map[SideEffect][]string, se SideEffect) ([]string, bool) {
	if allow == nil {
		return nil, false
	}
	if patterns, ok := allow[se]; ok {
		return patterns, true
	}
	if class, _ := classOf(se); class != se {
		patterns, ok := allow[class]
		return patterns, ok
	}
	return nil, false
}

// RecordOp accounts a successful mutating operation against the
// budget. Only call after RunE has returned without error AND the
// command's side-effect class is write|destructive — kit's middleware
// enforces both preconditions before invoking RecordOp.
//
// Returns ErrMaxOpsExceeded once the budget is hit. The middleware
// translates this into output.RateLimitedError + ExitCode 64.
func (e *Engine) RecordOp(cmd *cobra.Command) error {
	if e == nil {
		return nil
	}
	e.opsCount++
	if e.maxOps > 0 && e.opsCount > e.maxOps {
		return ErrMaxOpsExceeded
	}
	return nil
}

// matchAny returns true when value matches any pattern in patterns.
// Patterns use shell-style globbing via path.Match — "*" matches one
// segment-free run of characters; full literal match is fine too.
// "*" alone matches anything.
func matchAny(patterns []string, value string) bool {
	for _, p := range patterns {
		if p == "*" {
			return true
		}
		if ok, _ := path.Match(p, value); ok {
			return true
		}
		// Convenience: "delete:*" should match "delete <id>" or
		// "delete subverb"; the colon form is the documented policy
		// shorthand. Translate "<verb>:*" → prefix match on the verb.
		if strings.HasSuffix(p, ":*") {
			prefix := strings.TrimSuffix(p, ":*")
			if value == prefix || strings.HasPrefix(value, prefix+" ") {
				return true
			}
		}
	}
	return false
}
