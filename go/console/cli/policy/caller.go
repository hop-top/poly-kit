package policy

import (
	"errors"
	"fmt"
	"net/url"
	"path"
	"slices"
	"strconv"
	"time"
)

// DefaultBudgetWindow is the window a [CallerRule] budget counts over
// when the rule sets max_ops and no window.
const DefaultBudgetWindow = time.Hour

// Caller is who a served call comes from, as the transport
// established it. The policy never sees an unestablished caller: a
// claimed identity proves nothing, so such a call is answered by the
// policy's base rules alone, like a CLI invocation.
type Caller struct {
	// Principal is the established principal; empty when the
	// transport established the caller without naming one.
	Principal string
	// Tenant is the principal's tenant, when its credential carries
	// one.
	Tenant string
	// Scopes are the scopes the caller's verified credential holds.
	Scopes []string
	// Owner marks a caller the transport itself proved holds the
	// owner's authority — the owner-only socket file, the stdio
	// spawn. There is no credential to hold a scope, and whoever
	// holds that authority could run the command from the CLI, so
	// the owner is taken to hold every scope.
	Owner bool
}

// holds reports whether c holds scope.
func (c Caller) holds(scope string) bool {
	return c.Owner || slices.Contains(c.Scopes, scope)
}

// CallerRule widens or narrows the policy for the callers it matches,
// on served surfaces. It lives in the policy's callers section:
//
//	callers:
//	  - principal: "svc-*"        # glob on the established principal
//	    tenant: acme              # glob on the principal's tenant
//	    scope: widgets:write      # a scope the credential must hold
//	    allow:                    # answers the classes it declares
//	      write: ["*"]
//	    max_ops: 100              # mutating calls per window
//	    window: 24h               # default 1h
//
// A rule matches a caller when every match field it sets matches; a
// rule setting none matches every established caller. Globs use
// path.Match syntax, and "*" alone matches any value, the empty one
// included. The rules are tried in order and the first match answers.
//
// Allow has the shape and semantics of [Policy.Allow], for the classes
// the rule declares; a class it does not declare is answered by the
// policy's own allow map. MaxOps > 0 gives each matching principal
// (per tenant) a budget of mutating calls — write and destructive —
// over fixed windows of Window, kept by the serving process.
type CallerRule struct {
	Principal string                  `yaml:"principal,omitempty"`
	Tenant    string                  `yaml:"tenant,omitempty"`
	Scope     string                  `yaml:"scope,omitempty"`
	Allow     map[SideEffect][]string `yaml:"allow,omitempty"`
	MaxOps    int                     `yaml:"max_ops,omitempty"`
	Window    time.Duration           `yaml:"window,omitempty"`
}

// Matches reports whether the rule applies to c.
func (r CallerRule) Matches(c Caller) bool {
	if r.Principal != "" && !globMatch(r.Principal, c.Principal) {
		return false
	}
	if r.Tenant != "" && !globMatch(r.Tenant, c.Tenant) {
		return false
	}
	if r.Scope != "" && !c.holds(r.Scope) {
		return false
	}
	return true
}

// globMatch is path.Match with "*" alone matching every value.
func globMatch(pattern, value string) bool {
	if pattern == "*" {
		return true
	}
	ok, _ := path.Match(pattern, value)
	return ok
}

// Budget is the mutating-op budget a [CallerRule] gives a caller on
// served surfaces.
type Budget struct {
	// Key names the budget: the policy, the rule, the principal and
	// its tenant. Callers sharing a key share a budget.
	Key string
	// MaxOps is the number of mutating calls the window admits.
	MaxOps int
	// Window is the length of the fixed windows the budget resets on.
	Window time.Duration
}

// RuleFor returns the first rule matching c, and its index in
// Callers. ok is false for a nil caller — one the transport did not
// establish — and when no rule matches.
func (p Policy) RuleFor(c *Caller) (rule CallerRule, index int, ok bool) {
	if c == nil {
		return CallerRule{}, -1, false
	}
	for i, r := range p.Callers {
		if r.Matches(*c) {
			return r, i, true
		}
	}
	return CallerRule{}, -1, false
}

// HasBudgets reports whether any caller rule sets max_ops: whether
// serving the policy needs somewhere to count.
func (p Policy) HasBudgets() bool {
	for _, r := range p.Callers {
		if r.MaxOps > 0 {
			return true
		}
	}
	return false
}

// validate checks the callers section. It is the only part of a
// policy Load refuses on: the rest keeps the lenient reading policy
// files have always had.
func (p Policy) validate() error {
	var errs []error
	for i, r := range p.Callers {
		at := func(format string, args ...any) {
			errs = append(errs, fmt.Errorf("callers[%d]: "+format, append([]any{i}, args...)...))
		}
		for _, f := range [][2]string{{"principal", r.Principal}, {"tenant", r.Tenant}} {
			if _, err := path.Match(f[1], ""); err != nil {
				at("%s %q: %v", f[0], f[1], err)
			}
		}
		for class := range r.Allow {
			if _, known := classOf(class); !known {
				at("allow: unknown side-effect class %q", class)
			}
		}
		if r.MaxOps < 0 {
			at("max_ops %d is negative; 0 means no budget", r.MaxOps)
		}
		if r.Window < 0 {
			at("window %s is negative", r.Window)
		}
	}
	return errors.Join(errs...)
}

// budgetKey names the budget of rule index for c.
func (p Policy) budgetKey(index int, c *Caller) string {
	return "policy/" + url.PathEscape(p.Name) + "/" + strconv.Itoa(index) + "/" +
		url.PathEscape(c.Principal) + "/" + url.PathEscape(c.Tenant)
}
