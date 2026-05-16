package policy

import (
	"path"
	"strings"

	"github.com/spf13/cobra"
)

// sideEffectAnnotation is the cobra command annotation key kit reads
// to discover the declared side-effect class. Mirrored from
// cli.sideEffectAnnotation to break the import cycle; the spec
// reserves the exact string per §3.5.
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

// Authorize decides whether cmd may run under the active policy.
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

	cmdPath := cmd.CommandPath()
	verb := cmdPath
	// CommandPath includes the binary name; strip the leading "<tool> "
	// so allow-globs match adopter-friendly paths ("delete:*", not
	// "<tool> delete:*").
	if idx := strings.Index(cmdPath, " "); idx >= 0 {
		verb = strings.TrimSpace(cmdPath[idx+1:])
	}

	// Default-permit when no allow map is loaded. With a loaded
	// policy, an empty allow class categorically refuses that class.
	if e.policy.Allow != nil {
		patterns, classDeclared := e.policy.Allow[se]
		if classDeclared {
			if !matchAny(patterns, verb) {
				return false, false, "policy: " + string(se) + " not allowed for " + verb
			}
		}
	}

	// require_confirm match: any matching glob bumps the flag.
	if matchAny(e.policy.RequireConfirm, verb) {
		requireConfirm = true
	}
	return true, requireConfirm, ""
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
		// "delete subverb"; the colon form is what §8.6's example
		// uses. Translate "<verb>:*" → prefix match on the verb.
		if strings.HasSuffix(p, ":*") {
			prefix := strings.TrimSuffix(p, ":*")
			if value == prefix || strings.HasPrefix(value, prefix+" ") {
				return true
			}
		}
	}
	return false
}
