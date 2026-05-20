package cli

import (
	"os"
	"strings"
)

// invokedAsEnvVar names the environment variable that carries the
// caller-context signal. The convention mirrors other KIT_* env var
// names (KIT_DRY_RUN, KIT_TELEMETRY_MODE, …): kit-owned globals share
// the KIT_ prefix.
const invokedAsEnvVar = "KIT_INVOKED_AS"

// initInvokedAs reads KIT_INVOKED_AS from the process environment and
// stores the trimmed value on r. Empty string (the zero value) means
// the tool was invoked standalone. Called once from cli.New so the
// value is stable for the lifetime of Root — adopters and functional
// opts read it via Root.InvokedAs.
func (r *Root) initInvokedAs() {
	if r == nil {
		return
	}
	r.invokedAs = strings.TrimSpace(os.Getenv(invokedAsEnvVar))
}

// InvokedAs returns the caller-context signal captured from the
// KIT_INVOKED_AS environment variable at cli.New time.
//
// Empty string means standalone invocation — the tool was launched
// directly from a shell or another process that did not set the
// variable. A non-empty value names the upstream tool (e.g. "tlc",
// "hop") that exec'd this binary as a child, letting downstream
// kit-powered tools pick the right project-layer config or adjust UX
// (suppress prompts, defer to parent's logger, route hints back up
// the chain, …).
//
// The contract is env-var-only by design: callers like tlc and hop
// set KIT_INVOKED_AS before exec'ing the child. There is no
// --invoked-as flag — the signal is set programmatically, not by
// humans on the command line. Whitespace is trimmed so callers can be
// sloppy about quoting without breaking equality checks.
//
// Stability: the value is captured once at cli.New and never
// re-read. Mutating the env var after construction has no effect on
// the cached return value — this keeps the contract consistent
// across goroutines and avoids surprising drift mid-dispatch.
func (r *Root) InvokedAs() string {
	if r == nil {
		return ""
	}
	return r.invokedAs
}
