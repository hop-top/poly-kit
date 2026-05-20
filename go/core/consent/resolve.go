package consent

import (
	"context"
	"fmt"
	"os"
	"strings"
)

// EnvProvider abstracts env-var reads so the resolver stays pure: tests
// inject a deterministic lookup function instead of mutating process
// state via t.Setenv. The contract matches os.Getenv exactly — return
// the empty string for any unset key.
type EnvProvider func(key string) string

// OSEnv returns an EnvProvider backed by os.Getenv. Use this in
// production paths; tests should construct a map-backed provider via
// MapEnv (or an inline closure) instead.
func OSEnv() EnvProvider {
	return os.Getenv
}

// MapEnv returns an EnvProvider that serves values from m. Convenient
// for table-driven tests: each row builds its own env vector without
// touching os.Environ.
func MapEnv(m map[string]string) EnvProvider {
	return func(k string) string {
		return m[k]
	}
}

// Inputs is the resolver's input vector. All fields are pure data —
// no I/O, no global state. The caller is responsible for loading the
// persisted decision (via a Store) and for parsing CLI flags into the
// shape below.
type Inputs struct {
	// TelemetryFlag holds the parsed value of --telemetry. nil means
	// the flag was not set on this invocation; non-nil pointers carry
	// *true for "--telemetry=on" and *false for "--telemetry=off".
	TelemetryFlag *bool

	// YesFlag mirrors --yes / --confirm=yes. --yes is the
	// non-interactive confirmation hint paired with --telemetry=on;
	// it does NOT grant consent on its own.
	YesFlag bool

	// Env supplies the env-var lookup. nil falls back to OSEnv() so
	// callers that don't need to inject can leave it zero.
	Env EnvProvider

	// AppPrefix is the embedding application's telemetry mode prefix
	// (per telemetry.CurrentAppPrefix). Empty means no app-level
	// override env var is checked; only KIT_TELEMETRY_MODE is. When
	// set, "<AppPrefix>_TELEMETRY_MODE" is consulted BEFORE
	// "KIT_TELEMETRY_MODE".
	AppPrefix string

	// Persisted is the on-disk decision loaded by the caller. A
	// zero-value (State == "") indicates no record yet — the resolver
	// falls through to the default branch in that case.
	Persisted Decision
}

// ResolveError describes a non-fatal diagnostic encountered while
// resolving — for example, KIT_TELEMETRY_CONSENT set to a legacy
// "allow|deny" value. The resolver continues past the offending input
// (treats it as unset) and emits one ResolveError per problem so the
// CLI can surface a warning. The returned Decision is always usable.
type ResolveError struct {
	Var     string
	Value   string
	Message string
}

// Error renders the diagnostic in the shape "<var>=<value>: <message>".
func (e *ResolveError) Error() string {
	return fmt.Sprintf("%s=%q: %s", e.Var, e.Value, e.Message)
}

// Resolve returns the effective Decision for the given Inputs without
// surfacing diagnostics. It is shorthand for callers that don't care
// about diagnostic messages (e.g. the ConsentHook hot path).
func Resolve(ctx context.Context, in Inputs) Decision {
	d, _ := ResolveWithDiagnostics(ctx, in)
	return d
}

// ResolveWithDiagnostics applies the precedence chain and returns the
// resolved Decision plus any non-fatal diagnostics encountered while
// reading env vars.
//
// Precedence (highest wins):
//
//  1. "<AppPrefix>_TELEMETRY_MODE=off" or "KIT_TELEMETRY_MODE=off"
//     -> denied (short-circuit). App prefix is checked BEFORE the kit
//     prefix; either being "off" wins.
//  2. DO_NOT_TRACK=1 -> denied (non-overridable; beats --telemetry=on).
//  3. --telemetry=on|off -> granted | denied (source=flag).
//  4. --yes paired with --telemetry=on -> granted (already covered by
//     step 3; --yes alone has no effect).
//  5. KIT_TELEMETRY_CONSENT=granted|denied -> granted | denied
//     (source=env). Legacy values ("allow"/"deny"/"1"/"0"/"true"/
//     "false") and any other non-canonical strings emit a ResolveError
//     and are treated as unset.
//  6. Persisted decision -> returned verbatim (decided_at,
//     prompt_version, decision_source all preserved).
//  7. Default -> denied, source=config.
//
// The resolver is PURE: same inputs produce the same outputs, with no
// file I/O and no env reads except via in.Env. It NEVER emits
// SourcePrompt — that source is set only by the interactive prompt
// path, not here. Overrides at steps 1-5 leave decided_at and
// prompt_version zero-valued in the returned Decision; the upstream
// caller is expected to stamp those only when writing the persisted
// config.
//
// On the AppMode=anon vs KitMode=off question (where the table-test
// "TestResolve_AppModeBeatsKitMode" probes intent): ONLY the literal
// "off" value short-circuits. Any other mode value (anon/full/empty)
// is consumed by kit-telemetry's CurrentMode resolver, NOT by this
// consent resolver — they answer different questions ("how much do we
// collect?" vs "do we collect at all?"). So if the app prefix is set
// to "anon" while KIT_TELEMETRY_MODE=off, the resolver short-circuits
// to denied because step 1 sees "off" on the kit prefix. Conversely,
// if the app prefix is "off" and kit prefix is "anon", step 1 still
// short-circuits — app prefix is checked first and "off" wins. The
// upshot: at step 1, "off-anywhere wins"; non-"off" mode values are
// invisible here.
func ResolveWithDiagnostics(ctx context.Context, in Inputs) (Decision, []ResolveError) {
	_ = ctx // reserved for future cancellation; resolver is pure today

	env := in.Env
	if env == nil {
		env = OSEnv()
	}

	var diags []ResolveError

	// Step 1: KIT_TELEMETRY_MODE / <APP>_TELEMETRY_MODE.
	// App prefix is checked first; either being "off" short-circuits.
	if in.AppPrefix != "" {
		appKey := strings.ToUpper(in.AppPrefix) + "_TELEMETRY_MODE"
		if strings.EqualFold(env(appKey), "off") {
			return Decision{State: StateDenied, DecisionSource: SourceEnv}, diags
		}
	}
	if strings.EqualFold(env("KIT_TELEMETRY_MODE"), "off") {
		return Decision{State: StateDenied, DecisionSource: SourceEnv}, diags
	}

	// Step 2: DO_NOT_TRACK — non-overridable. Beats --telemetry=on.
	// Honors the consoledonottrack.com convention: any non-empty value
	// other than "0"/"false" opts out (see DoNotTrackEnabled).
	if DoNotTrackEnabled(env) {
		return Decision{State: StateDenied, DecisionSource: SourceEnv}, diags
	}

	// Step 3 & 4: --telemetry flag (with --yes folded in: --yes alone
	// has no effect because the flag is what carries the decision,
	// not the confirmation).
	if in.TelemetryFlag != nil {
		if *in.TelemetryFlag {
			return Decision{State: StateGranted, DecisionSource: SourceFlag}, diags
		}
		return Decision{State: StateDenied, DecisionSource: SourceFlag}, diags
	}

	// Step 5: KIT_TELEMETRY_CONSENT. Strict vocabulary — granted|denied.
	// Anything else (including legacy allow|deny) emits a diagnostic
	// and falls through to the persisted layer.
	if raw := env("KIT_TELEMETRY_CONSENT"); raw != "" {
		switch raw {
		case string(StateGranted):
			return Decision{State: StateGranted, DecisionSource: SourceEnv}, diags
		case string(StateDenied):
			return Decision{State: StateDenied, DecisionSource: SourceEnv}, diags
		default:
			diags = append(diags, ResolveError{
				Var:     "KIT_TELEMETRY_CONSENT",
				Value:   raw,
				Message: `invalid value; expected "granted" or "denied"`,
			})
			// fall through
		}
	}

	// Step 6: Persisted decision. Return verbatim — decided_at,
	// prompt_version, and decision_source are preserved as stored.
	if in.Persisted.State == StateGranted || in.Persisted.State == StateDenied {
		return in.Persisted, diags
	}

	// Step 7: Default. Cold-start (no record yet, no overrides) is
	// denied with source=config.
	return Decision{State: StateDenied, DecisionSource: SourceConfig}, diags
}
