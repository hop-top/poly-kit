package telemetry

import (
	"context"
	"fmt"
	"sync/atomic"

	"hop.top/kit/go/core/redact"
)

// MustLoadRedactor returns the kit-wide default Redactor (gitleaks +
// Presidio corpus, lazy-loaded on first call). Panics if the corpus
// loaded with zero rules — an empty rule set is a config bug, not a
// runtime decision: telemetry with no redaction is worse than no
// telemetry at all.
//
// Telemetry deliberately shares redact.Default() rather than building a
// private Redactor: one rule corpus across the process means one
// audit surface, one place to override via KIT_REDACT_RULES_PATH.
func MustLoadRedactor() *redact.Redactor {
	r := redact.Default()
	if s := r.Stats(); s.Rules == 0 {
		panic(fmt.Sprintf("telemetry.MustLoadRedactor: redact.Default() returned %d rules; refusing to emit unprotected telemetry", s.Rules))
	}
	return r
}

// RedactMatchObserver is fired when redactEvent rewrites a field. The
// kit-telemetry-compliance package subscribes via the
// `kit.telemetry.redact.matched` bus topic; the bus publish itself
// lives in the emitter. For now this is just the in-process hook.
//
// fieldPath is a dotted breadcrumb identifying which Event field
// produced the match: "args[2]" for the third arg, "flags.--token"
// for the value of flag --token. ruleID is the redact rule id that
// fired (e.g. "openai-api-key", "email-address-pii"). Implementations
// MUST NOT panic — observers run in the redact hot path.
type RedactMatchObserver interface {
	OnMatch(ctx context.Context, fieldPath string, ruleID string)
}

// noopRedactObserver is the default; satisfies the interface without
// allocation.
type noopRedactObserver struct{}

func (noopRedactObserver) OnMatch(context.Context, string, string) {}

// observerBox wraps RedactMatchObserver so atomic.Value sees one
// concrete type across all Store calls (atomic.Value panics on
// type-inconsistent Store).
type observerBox struct {
	obs RedactMatchObserver
}

// redactObserver is the package-global observer registry. atomic.Value
// holds an observerBox so SetRedactObserver can run from init()
// without locking, matching the appPrefix pattern in mode.go.
var redactObserver atomic.Value // observerBox

// SetRedactObserver installs obs as the package observer for redact
// matches in telemetry events. Pass nil to restore the no-op default.
// Reentrant; the last call wins. Typically called once at process init
// by kit-telemetry-compliance.
func SetRedactObserver(obs RedactMatchObserver) {
	if obs == nil {
		redactObserver.Store(observerBox{obs: noopRedactObserver{}})
		return
	}
	redactObserver.Store(observerBox{obs: obs})
}

// CurrentRedactObserver returns the currently installed observer,
// never nil. Defaults to a no-op observer when SetRedactObserver has
// not been called.
func CurrentRedactObserver() RedactMatchObserver {
	v := redactObserver.Load()
	if v == nil {
		return noopRedactObserver{}
	}
	box, ok := v.(observerBox)
	if !ok || box.obs == nil {
		return noopRedactObserver{}
	}
	return box.obs
}

// redactEvent returns a new Event with Args and Flag values rewritten
// through r. In ModeAnon ("anon") returns ev unchanged: redaction is a
// Full-tier-only concern (Args/Flags are empty in Anon). The input ev
// is NEVER mutated — Args and Flags are copied before substitution so
// caller-held references to the original slices/maps are safe.
//
// Implementation: per-field Scan first (to surface match metadata with
// field path to the observer), then Apply for substitution. Two passes
// trades a small CPU hit for clean observer wiring without mutating
// the shared redact.Default() observer list.
func redactEvent(r *redact.Redactor, ev Event) Event {
	if ev.Mode != "full" {
		return ev
	}
	out := ev
	obs := CurrentRedactObserver()

	if len(ev.Args) > 0 {
		args := make([]string, len(ev.Args))
		for i, a := range ev.Args {
			args[i] = redactField(r, obs, fmt.Sprintf("args[%d]", i), a)
		}
		out.Args = args
	} else {
		out.Args = append([]string(nil), ev.Args...)
	}

	if len(ev.Flags) > 0 {
		flags := make(map[string]string, len(ev.Flags))
		for k, v := range ev.Flags {
			flags[k] = redactField(r, obs, "flags."+k, v)
		}
		out.Flags = flags
	} else if ev.Flags != nil {
		out.Flags = make(map[string]string, 0)
	}

	// Copy CommandPath too — defensive, since callers may share the
	// slice with us. Mode/InstallationID/etc are value types so they
	// already copy via struct assignment.
	if ev.CommandPath != nil {
		out.CommandPath = append([]string(nil), ev.CommandPath...)
	}
	return out
}

// redactField runs r.Scan to surface matches to obs (each as a
// distinct OnMatch call carrying fieldPath), then returns r.Apply(s).
// Returning the Apply result rather than reconstructing from Scan
// indices keeps the substitution logic single-sourced in the redact
// package (Tag template, allowlist, custom strategy, etc).
func redactField(r *redact.Redactor, obs RedactMatchObserver, fieldPath, s string) string {
	if s == "" {
		return s
	}
	for _, m := range r.Scan(s) {
		obs.OnMatch(context.Background(), fieldPath, m.RuleID)
	}
	return r.Apply(s)
}
