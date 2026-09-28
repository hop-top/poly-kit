// Package policy implements kit's delegation-safety policy engine.
// Adopter tools wire enforcement
// once via cli.WithPolicy; this package owns:
//
//   - Policy: the loaded YAML shape (allow / max_ops / require_confirm,
//     and the permissions: rules a served tool compiles)
//   - Engine: per-invocation enforcement state — Authorize gates the
//     side-effect tag, RecordOp accounts mutating-op budget.
//   - Load: reads $XDG_CONFIG_HOME/<tool>/policies/<name>.yaml.
//
// The Policy values are inert; the Engine is mutable and lives for one
// command invocation only — kit constructs a fresh Engine inside the
// RunE middleware on every Execute. Engine is not safe for concurrent
// use; the cli middleware never calls into it from multiple goroutines.
//
// SideEffect is mirrored as a local string type so this package can
// be imported by cli without an import cycle. The values must match
// cli.SideEffect{Read,Write,Destructive,Interactive}.
package policy

import (
	"errors"
	"fmt"
)

// SideEffect mirrors cli.SideEffect as a local type so the Policy
// schema is a plain string-keyed map. The cli package converts
// between the two transparently.
type SideEffect string

// Side-effect class constants — must align with cli.SideEffect*.
const (
	SideEffectRead        SideEffect = "read"
	SideEffectWrite       SideEffect = "write"
	SideEffectDestructive SideEffect = "destructive"
	SideEffectInteractive SideEffect = "interactive"
)

// Policy declares per-side-effect-class rules. Loaded from YAML in
// $XDG_CONFIG_HOME/<tool>/policies/<name>.yaml.
//
// Field semantics (locked):
//   - Allow: per-side-effect verb glob list. The empty list under a
//     class categorically refuses that class (e.g.
//     `destructive: []` blocks every destructive command).
//   - MaxOps: per-invocation cap on mutating ops. 0 means unlimited.
//   - RequireConfirm: command-path globs that always require explicit
//     confirmation regardless of --confirm value. A typed-confirmation
//     command is recognized via the kit/destructive-token annotation;
//     this list adds extra command paths beyond annotation-driven
//     ones.
//   - Permissions: expression rules for served invocations, the
//     `permissions:` block. Inert here: the Engine never reads them.
//     A served tool compiles them into its permission gate, where
//     they run after the scope check and Allow, and can only narrow
//     (see PermissionRule).
//   - Callers: per-caller rules for served surfaces, a section of its
//     own (see [CallerRule]). A policy without it applies the same
//     rules to every caller, as it always has.
type Policy struct {
	Name           string                  `yaml:"name"`
	Allow          map[SideEffect][]string `yaml:"allow"`
	MaxOps         int                     `yaml:"max_ops"`
	RequireConfirm []string                `yaml:"require_confirm"`
	Permissions    []PermissionRule        `yaml:"permissions"`

	// Unannotated is the side-effect class a command declaring no
	// kit/side-effect is authorized as. Empty keeps the default: such
	// a command passes, and the validator refuses it separately.
	Unannotated SideEffect `yaml:"unannotated,omitempty"`

	// Callers are the per-caller rules, consulted for a caller whose
	// identity the transport established. The first rule that
	// matches the caller answers for the side-effect classes it
	// declares and may set a budget; the fields above answer the rest.
	Callers []CallerRule `yaml:"callers,omitempty"`

	// Remedy, when set, is appended to every refusal the policy
	// gives, telling the caller how to get past it. Code only: the
	// shipped policies set it.
	Remedy string `yaml:"-"`
}

// PermissionRule is one entry of a policy's `permissions:` block: a
// named boolean expression over a served invocation, in the rule
// language of hop.top/kit/go/runtime/policy. Effect applies when When
// is true, Otherwise when it is false; across rules deny overrides,
// and a rule that fails to evaluate denies.
//
// This package only carries and validates the rules. Compiling and
// evaluating them is the evaluator's job — the CEL one lives in
// hop.top/kit/go/console/cli/celpermission — so a tool that never
// serves links no expression engine.
type PermissionRule struct {
	// Name identifies the rule in refusals and audit records. Required
	// and unique within the block.
	Name string `yaml:"name"`
	// When is the expression. Required.
	When string `yaml:"when"`
	// Effect is the verdict when When is true: allow or deny.
	Effect string `yaml:"effect"`
	// Otherwise is the verdict when When is false: allow or deny.
	Otherwise string `yaml:"otherwise"`
	// Message is the refusal's human-readable explanation.
	Message string `yaml:"message"`
}

// Rule effects, the values PermissionRule.Effect and Otherwise take.
const (
	RuleAllow = "allow"
	RuleDeny  = "deny"
)

// ValidatePermissionRules checks the shape of a permissions: block:
// every rule named, names unique, an expression present, and both
// effects allow or deny. It does not compile the expressions; the
// evaluator does, and names the rule it cannot compile.
func ValidatePermissionRules(rules []PermissionRule) error {
	seen := make(map[string]struct{}, len(rules))
	for i, r := range rules {
		if r.Name == "" {
			return fmt.Errorf("permissions[%d]: name required", i)
		}
		if _, dup := seen[r.Name]; dup {
			return fmt.Errorf("permission rule %q: duplicate name", r.Name)
		}
		seen[r.Name] = struct{}{}
		if r.When == "" {
			return fmt.Errorf("permission rule %q: 'when' required", r.Name)
		}
		for _, f := range []struct{ field, value string }{{"effect", r.Effect}, {"otherwise", r.Otherwise}} {
			switch f.value {
			case RuleAllow, RuleDeny:
			case "":
				return fmt.Errorf("permission rule %q: %s required (allow|deny)", r.Name, f.field)
			default:
				return fmt.Errorf("permission rule %q: %s %q invalid (want allow|deny)", r.Name, f.field, f.value)
			}
		}
	}
	return nil
}

// ErrMaxOpsExceeded is returned by Engine.RecordOp when the per-
// invocation budget has been hit. The cli middleware translates this
// into output.RateLimitedError so the process exits with
// EXIT_RATE_LIMITED (64).
var ErrMaxOpsExceeded = errors.New("policy: max-ops budget exceeded")
