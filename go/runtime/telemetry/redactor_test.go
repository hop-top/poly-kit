package telemetry

import (
	"context"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
)

// resetRedactObserverForTest restores the default no-op observer.
// Tests that install an observer MUST defer this to avoid bleed
// between cases.
func resetRedactObserverForTest() {
	redactObserver.Store(observerBox{obs: noopRedactObserver{}})
}

func TestMustLoadRedactor_ReturnsNonNil(t *testing.T) {
	r := MustLoadRedactor()
	if r == nil {
		t.Fatal("MustLoadRedactor returned nil")
	}
	s := r.Stats()
	if s.Rules == 0 {
		t.Fatal("MustLoadRedactor returned redactor with 0 rules; corpus did not load")
	}
}

func TestRedactEvent_AnonModeUnchanged(t *testing.T) {
	r := MustLoadRedactor()
	in := Event{
		Mode: "anon",
		Args: []string{"user@example.com", "plain-arg"},
	}
	out := redactEvent(r, in)
	if len(out.Args) != 2 {
		t.Fatalf("anon mode dropped args: got %d, want 2", len(out.Args))
	}
	if out.Args[0] != "user@example.com" {
		t.Errorf("anon mode mutated args[0]: got %q, want %q", out.Args[0], "user@example.com")
	}
	if out.Args[1] != "plain-arg" {
		t.Errorf("anon mode mutated args[1]: got %q, want %q", out.Args[1], "plain-arg")
	}
}

func TestRedactEvent_FullModeRedactsArgs(t *testing.T) {
	r := MustLoadRedactor()
	in := Event{
		Mode: "full",
		Args: []string{"user@example.com", "plain-arg"},
	}
	out := redactEvent(r, in)
	if len(out.Args) != 2 {
		t.Fatalf("full mode args length changed: got %d, want 2", len(out.Args))
	}
	if out.Args[0] == "user@example.com" {
		t.Errorf("full mode did NOT redact email arg: got %q", out.Args[0])
	}
	if out.Args[0] == "" {
		t.Errorf("full mode emptied the arg instead of redacting it")
	}
	// Plain string should pass through unchanged.
	if out.Args[1] != "plain-arg" {
		t.Errorf("full mode mutated non-matching arg: got %q, want %q", out.Args[1], "plain-arg")
	}
}

func TestRedactEvent_FullModeRedactsFlagValues(t *testing.T) {
	r := MustLoadRedactor()
	in := Event{
		Mode: "full",
		Flags: map[string]string{
			"--contact": "alice@example.com",
			"--name":    "bob",
		},
	}
	out := redactEvent(r, in)
	if _, ok := out.Flags["--contact"]; !ok {
		t.Fatal("full mode dropped flag key --contact")
	}
	if _, ok := out.Flags["--name"]; !ok {
		t.Fatal("full mode dropped flag key --name")
	}
	if out.Flags["--contact"] == "alice@example.com" {
		t.Errorf("full mode did NOT redact email flag value: got %q", out.Flags["--contact"])
	}
	if out.Flags["--name"] != "bob" {
		t.Errorf("full mode mutated non-matching flag value: got %q, want %q", out.Flags["--name"], "bob")
	}
}

func TestRedactEvent_FullModeDoesNotMutateInput(t *testing.T) {
	r := MustLoadRedactor()
	origArgs := []string{"user@example.com", "plain"}
	origFlags := map[string]string{"--email": "bob@example.com"}
	in := Event{
		Mode:  "full",
		Args:  origArgs,
		Flags: origFlags,
	}
	_ = redactEvent(r, in)

	// Original slice elements MUST be unchanged.
	if origArgs[0] != "user@example.com" {
		t.Errorf("redactEvent mutated caller's Args slice: got %q, want %q", origArgs[0], "user@example.com")
	}
	if origArgs[1] != "plain" {
		t.Errorf("redactEvent mutated caller's Args[1]: got %q, want %q", origArgs[1], "plain")
	}
	// Original map values MUST be unchanged.
	if origFlags["--email"] != "bob@example.com" {
		t.Errorf("redactEvent mutated caller's Flags map: got %q, want %q", origFlags["--email"], "bob@example.com")
	}
}

func TestRedactEvent_DefensiveCopyOnArgsSlice(t *testing.T) {
	// Even with no Args content, the returned slice header must not
	// share backing storage with the input.
	r := MustLoadRedactor()
	in := Event{
		Mode: "full",
		Args: []string{"plain1", "plain2"},
	}
	out := redactEvent(r, in)
	// Mutate out — should NOT affect in.
	out.Args[0] = "MUTATED"
	if in.Args[0] == "MUTATED" {
		t.Error("redactEvent did not defensively copy Args; mutation bled back")
	}
}

// countingObserver records every OnMatch call for inspection.
type countingObserver struct {
	mu       sync.Mutex
	calls    int32
	last     match
	allCalls []match
}

type match struct {
	fieldPath string
	ruleID    string
}

func (o *countingObserver) OnMatch(_ context.Context, fieldPath, ruleID string) {
	o.mu.Lock()
	defer o.mu.Unlock()
	atomic.AddInt32(&o.calls, 1)
	o.last = match{fieldPath: fieldPath, ruleID: ruleID}
	o.allCalls = append(o.allCalls, match{fieldPath: fieldPath, ruleID: ruleID})
}

func TestRedactObserver_FiresOnMatch(t *testing.T) {
	defer resetRedactObserverForTest()

	r := MustLoadRedactor()
	obs := &countingObserver{}
	SetRedactObserver(obs)

	in := Event{
		Mode: "full",
		Args: []string{"user@example.com"},
	}
	_ = redactEvent(r, in)

	if atomic.LoadInt32(&obs.calls) == 0 {
		t.Fatal("observer did not fire on matching arg")
	}
	if obs.last.fieldPath == "" {
		t.Error("observer fieldPath empty")
	}
	if obs.last.ruleID == "" {
		t.Error("observer ruleID empty")
	}
	if !strings.HasPrefix(obs.last.fieldPath, "args[") {
		t.Errorf("expected args[N] fieldPath, got %q", obs.last.fieldPath)
	}
}

func TestRedactObserver_FiresWithFlagFieldPath(t *testing.T) {
	defer resetRedactObserverForTest()

	r := MustLoadRedactor()
	obs := &countingObserver{}
	SetRedactObserver(obs)

	in := Event{
		Mode:  "full",
		Flags: map[string]string{"--email": "alice@example.com"},
	}
	_ = redactEvent(r, in)

	if atomic.LoadInt32(&obs.calls) == 0 {
		t.Fatal("observer did not fire on matching flag value")
	}
	want := "flags.--email"
	found := false
	obs.mu.Lock()
	for _, c := range obs.allCalls {
		if c.fieldPath == want {
			found = true
			break
		}
	}
	obs.mu.Unlock()
	if !found {
		t.Errorf("expected observer call with fieldPath %q, got calls: %v", want, obs.allCalls)
	}
}

func TestRedactObserver_DefaultIsNoop(t *testing.T) {
	// Wipe any prior test's observer.
	resetRedactObserverForTest()

	r := MustLoadRedactor()
	in := Event{
		Mode: "full",
		Args: []string{"user@example.com"},
	}
	// Must not panic when observer is the default no-op.
	out := redactEvent(r, in)
	if out.Args[0] == "user@example.com" {
		t.Error("redactEvent should have rewritten the matching arg even with no observer")
	}
}

func TestRedactObserver_NoFireForNonMatchingInput(t *testing.T) {
	defer resetRedactObserverForTest()

	r := MustLoadRedactor()
	obs := &countingObserver{}
	SetRedactObserver(obs)

	in := Event{
		Mode: "full",
		Args: []string{"plain-string", "another-plain"},
	}
	_ = redactEvent(r, in)

	if atomic.LoadInt32(&obs.calls) != 0 {
		t.Errorf("observer fired %d time(s) on plain input; want 0", obs.calls)
	}
}

func TestSetRedactObserver_NilResetsToNoop(t *testing.T) {
	defer resetRedactObserverForTest()

	r := MustLoadRedactor()
	obs := &countingObserver{}
	SetRedactObserver(obs)

	// Confirm it fires.
	_ = redactEvent(r, Event{Mode: "full", Args: []string{"a@b.com"}})
	if atomic.LoadInt32(&obs.calls) == 0 {
		t.Fatal("precondition: observer should fire before nil reset")
	}

	beforeReset := atomic.LoadInt32(&obs.calls)
	SetRedactObserver(nil)

	// CurrentRedactObserver must return non-nil.
	cur := CurrentRedactObserver()
	if cur == nil {
		t.Fatal("CurrentRedactObserver returned nil after nil reset")
	}
	// Fire again; counting observer count should NOT grow.
	_ = redactEvent(r, Event{Mode: "full", Args: []string{"a@b.com"}})
	after := atomic.LoadInt32(&obs.calls)
	if after != beforeReset {
		t.Errorf("observer fired after SetRedactObserver(nil): before=%d after=%d", beforeReset, after)
	}
}

func TestCurrentRedactObserver_DefaultsToNoop(t *testing.T) {
	resetRedactObserverForTest()
	cur := CurrentRedactObserver()
	if cur == nil {
		t.Fatal("CurrentRedactObserver returned nil at default")
	}
	// no-op observer must accept calls without panic.
	cur.OnMatch(context.Background(), "x.y", "rule")
}

// Sanity: MustLoadRedactor returns the same singleton across calls.
// (Important so observer wiring is one-time, not per-load.)
func TestMustLoadRedactor_Singleton(t *testing.T) {
	r1 := MustLoadRedactor()
	r2 := MustLoadRedactor()
	if r1 != r2 {
		t.Error("MustLoadRedactor did not return the singleton across calls")
	}
}

// guard: redact package contract — Apply on the default redactor must
// not return empty for a non-empty input. If this fails, downstream
// assumptions in redactEvent break.
func TestRedactSingletonContract(t *testing.T) {
	r := MustLoadRedactor()
	out := r.Apply("user@example.com")
	if out == "" {
		t.Fatal("redact.Default().Apply emptied the input")
	}
	if out == "user@example.com" {
		t.Fatal("redact.Default() did not redact a clear PII fixture")
	}
}

// Compile-time guard: noopRedactObserver implements RedactMatchObserver.
var _ RedactMatchObserver = noopRedactObserver{}
var _ RedactMatchObserver = (*countingObserver)(nil)
