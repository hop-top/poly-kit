package serve

import (
	"fmt"
	"sort"
	"strings"

	"hop.top/kit/go/console/output"
)

// FailurePolicy is the supervisor's answer to "one service failed
// while the others are running" (contract §"One service fails while
// others run"). Config key: services.failure_policy.
type FailurePolicy string

const (
	// FailFast shuts every service down and exits non-zero when any
	// running service fails. This is the default: a tool whose
	// transports front the same command tree and the same state is
	// degraded, not healthy, when one of them is gone.
	FailFast FailurePolicy = "fail-fast"

	// Isolate keeps the remaining services running and marks the
	// failed one failed. Appropriate only when the services are
	// genuinely independent and partial availability beats none.
	Isolate FailurePolicy = "isolate"
)

// DefaultFailurePolicy is the documented default, FailFast.
const DefaultFailurePolicy = FailFast

// IsValid reports whether p is a declared failure policy.
func (p FailurePolicy) IsValid() bool {
	return p == FailFast || p == Isolate
}

// String returns the policy as written in config.
func (p FailurePolicy) String() string { return string(p) }

// PolicyGate decides whether a service's declared class is permitted
// to run. It is the third validation gate (contract §"The override
// rule"), and is satisfied in production by a table from
// hop.top/kit/go/ai/toolspec/policy.
//
// A nil PolicyGate passes every service: a tool that has not wired a
// policy table has not expressed a restriction.
type PolicyGate interface {
	// Allow reports whether a service with the given side-effect and
	// network class may run. The reason is surfaced in the refusal
	// message and may be empty when allowed.
	Allow(sideEffect, network string) (ok bool, reason string)
}

// Request is a parsed `serve` invocation: the positional arguments as
// typed, plus the resolved per-service configuration and the gates
// resolution must apply.
type Request struct {
	// Args is the positional arguments after the `serve` word. Empty
	// means the supervisor form; exactly one means the selector form;
	// two or more is a usage error.
	Args []string

	// Configs is the resolved services.<name> block per service.
	// A service with no entry is not configured, and the supervisor
	// form skips it.
	Configs map[string]Config

	// Policy is the third validation gate. Nil passes everything.
	Policy PolicyGate
}

// Outcome is the result of resolving a [Request] against a
// [Registry]: either a runnable set of services or a rendered
// refusal.
type Outcome struct {
	// Selected is the service identifiers to run, in registration
	// order. Empty when Err is non-nil.
	Selected []string

	// Explicit is true when the selector form was used. Under it,
	// Selected holds exactly one name, and aggregate enablement was
	// overridden rather than consulted.
	Explicit bool

	// Skipped is the configured-but-disabled services the supervisor
	// form passed over, in registration order. Skipping is not an
	// error and must not affect the exit code.
	Skipped []string

	// Err is the refusal, already carrying its Code and ExitCode.
	// Nil on success.
	Err *output.Error
}

// Resolve turns a `serve` invocation into a runnable set, applying
// the hierarchy and override rules from contract §"Command hierarchy"
// and §"The override rule". It is pure: no service is started, no
// listener is bound, and nothing is written.
//
// Selector form (exactly one argument) runs the named service even
// when services.<name>.enabled is false, provided all three gates
// pass in order — registration, then configuration, then policy.
// Enablement is not a gate under the selector form: an operator
// naming a service has already made the decision the flag exists to
// automate.
//
// Supervisor form (no arguments) runs every service that is both
// configured and enabled, in registration order. A disabled service
// is skipped silently. Resolving to zero services is a usage error,
// not a clean exit: a process that exits 0 without listening is
// indistinguishable from a successful start to systemd or a
// container runtime.
func Resolve(reg *Registry, req Request) Outcome {
	if reg == nil {
		return Outcome{Err: output.UsageError("serve: no service registry configured")}
	}

	switch len(req.Args) {
	case 0:
		return resolveAggregate(reg, req)
	case 1:
		return resolveExplicit(reg, req, req.Args[0])
	default:
		return Outcome{Err: output.UsageError(fmt.Sprintf(
			"serve accepts at most one service name, got %d", len(req.Args),
		))}
	}
}

// resolveExplicit implements the selector form and its override rule.
func resolveExplicit(reg *Registry, req Request, name string) Outcome {
	// Gate 1: registration.
	svc, ok := reg.Lookup(name)
	if !ok {
		err := output.NotFoundError(fmt.Sprintf(
			"unknown service %q; known: %s", name, strings.Join(reg.Names(), ", "),
		))
		if fix := nearestName(name, reg.Names()); fix != "" {
			err.SuggestedFix = fmt.Sprintf("did you mean %q?", fix)
		}
		return Outcome{Explicit: true, Err: err}
	}

	// Gate 2: configuration.
	if err := validateConfig(svc); err != nil {
		return Outcome{Explicit: true, Err: output.UsageError(fmt.Sprintf(
			"service %q: %v", name, err,
		))}
	}

	// Gate 3: policy.
	if err := checkPolicy(req.Policy, svc); err != nil {
		return Outcome{Explicit: true, Err: err}
	}

	// Enablement is deliberately not consulted here.
	return Outcome{Selected: []string{name}, Explicit: true}
}

// resolveAggregate implements the supervisor form.
func resolveAggregate(reg *Registry, req Request) Outcome {
	var out Outcome
	for _, name := range reg.Names() {
		cfg, configured := req.Configs[name]
		if !configured {
			continue
		}
		if !cfg.Enabled {
			out.Skipped = append(out.Skipped, name)
			continue
		}
		svc, ok := reg.Lookup(name)
		if !ok {
			continue
		}
		if err := validateConfig(svc); err != nil {
			return Outcome{Err: output.UsageError(fmt.Sprintf("service %q: %v", name, err))}
		}
		if err := checkPolicy(req.Policy, svc); err != nil {
			return Outcome{Err: err}
		}
		out.Selected = append(out.Selected, name)
	}

	if len(out.Selected) == 0 {
		out.Err = output.UsageError(
			"no services configured and enabled; enable one under services.* or name one explicitly",
		)
		out.Err.SuggestedFix = "set services.<name>.enabled: true, or run: serve <service>"
	}
	return out
}

// validateConfig runs the optional [Validator] gate.
func validateConfig(svc Service) error {
	v, ok := svc.(Validator)
	if !ok {
		return nil
	}
	return v.Validate()
}

// checkPolicy runs the optional [Classified] service against the
// [PolicyGate]. An unclassified service, or a nil gate, passes.
func checkPolicy(gate PolicyGate, svc Service) *output.Error {
	if gate == nil {
		return nil
	}
	c, ok := svc.(Classified)
	if !ok {
		return nil
	}
	sideEffect, network := c.Class()
	allowed, reason := gate.Allow(sideEffect, network)
	if allowed {
		return nil
	}
	msg := fmt.Sprintf(
		"service %q denied by policy (side_effect=%s, network=%s)",
		svc.Name(), sideEffect, network,
	)
	if reason != "" {
		msg += ": " + reason
	}
	return output.UnauthorizedError(msg)
}

// nearestName returns the registered name closest to want by edit
// distance, or "" when nothing is close enough to suggest. The
// threshold scales with the length of the typed word so a short name
// does not attract an unrelated suggestion.
func nearestName(want string, known []string) string {
	best := ""
	bestDist := -1
	limit := len(want)/2 + 1
	sorted := make([]string, len(known))
	copy(sorted, known)
	sort.Strings(sorted)
	for _, k := range sorted {
		d := editDistance(want, k)
		if d > limit {
			continue
		}
		if bestDist == -1 || d < bestDist {
			best, bestDist = k, d
		}
	}
	return best
}

// editDistance is the Levenshtein distance between a and b.
func editDistance(a, b string) int {
	prev := make([]int, len(b)+1)
	cur := make([]int, len(b)+1)
	for j := range prev {
		prev[j] = j
	}
	for i := 1; i <= len(a); i++ {
		cur[0] = i
		for j := 1; j <= len(b); j++ {
			cost := 1
			if a[i-1] == b[j-1] {
				cost = 0
			}
			cur[j] = min(prev[j]+1, min(cur[j-1]+1, prev[j-1]+cost))
		}
		prev, cur = cur, prev
	}
	return prev[len(b)]
}
