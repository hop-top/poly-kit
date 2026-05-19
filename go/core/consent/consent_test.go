package consent

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"hop.top/kit/go/runtime/telemetry"
)

// newTestStore wires NewFileStore against a fresh XDG_CONFIG_HOME under
// t.TempDir() so each test owns its on-disk state. Returns the store
// and the temp config root for direct file pokes.
func newTestStore(t *testing.T) (*FileStore, string) {
	t.Helper()
	dir := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", dir)
	s, err := NewFileStore()
	if err != nil {
		t.Fatalf("NewFileStore: %v", err)
	}
	return s, dir
}

func TestDecisionGranted(t *testing.T) {
	cases := []struct {
		name string
		d    Decision
		want bool
	}{
		{"granted", Decision{State: StateGranted}, true},
		{"denied", Decision{State: StateDenied}, false},
		{"unknown", Decision{State: StateUnknown}, false},
		{"zero", Decision{}, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := tc.d.Granted(); got != tc.want {
				t.Fatalf("Granted() = %v, want %v", got, tc.want)
			}
		})
	}
}

func TestNewFileStore_PathContainsKitTelemetry(t *testing.T) {
	s, _ := newTestStore(t)
	if !strings.HasSuffix(s.Path(), filepath.Join("kit", "telemetry.yaml")) {
		t.Fatalf("path %q does not end with kit/telemetry.yaml", s.Path())
	}
}

func TestFileStore_GetMissing_ReturnsUnknown(t *testing.T) {
	s, _ := newTestStore(t)
	d, err := s.Get(context.Background())
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if d.State != StateUnknown {
		t.Fatalf("State = %q, want %q", d.State, StateUnknown)
	}
}

func TestFileStore_RoundTrip_Granted(t *testing.T) {
	s, _ := newTestStore(t)
	want := Decision{
		State:          StateGranted,
		DecidedAt:      time.Date(2026, 5, 19, 12, 0, 0, 0, time.UTC),
		PromptVersion:  1,
		DecisionSource: SourcePrompt,
	}
	if err := s.Set(context.Background(), want); err != nil {
		t.Fatalf("Set: %v", err)
	}
	got, err := s.Get(context.Background())
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if got.State != want.State ||
		got.PromptVersion != want.PromptVersion ||
		got.DecisionSource != want.DecisionSource ||
		!got.DecidedAt.Equal(want.DecidedAt) {
		t.Fatalf("round trip mismatch: got %+v, want %+v", got, want)
	}
}

func TestFileStore_RoundTrip_Denied(t *testing.T) {
	s, _ := newTestStore(t)
	want := Decision{
		State:          StateDenied,
		DecidedAt:      time.Date(2026, 5, 19, 13, 30, 0, 0, time.UTC),
		PromptVersion:  2,
		DecisionSource: SourceFlag,
	}
	if err := s.Set(context.Background(), want); err != nil {
		t.Fatalf("Set: %v", err)
	}
	got, err := s.Get(context.Background())
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if got.State != StateDenied || got.DecisionSource != SourceFlag {
		t.Fatalf("got %+v, want denied/flag", got)
	}
	if got.Granted() {
		t.Fatal("Granted() = true on denied state")
	}
}

func TestFileStore_Set_AtomicAndPerms(t *testing.T) {
	s, _ := newTestStore(t)
	if err := s.Set(context.Background(), Decision{
		State:          StateGranted,
		DecidedAt:      time.Now().UTC(),
		PromptVersion:  1,
		DecisionSource: SourcePrompt,
	}); err != nil {
		t.Fatalf("Set: %v", err)
	}
	info, err := os.Stat(s.Path())
	if err != nil {
		t.Fatalf("stat: %v", err)
	}
	if got := info.Mode().Perm(); got != 0o600 {
		t.Fatalf("perm = %#o, want 0o600", got)
	}
}

func TestFileStore_Clear_RestoresUnknown(t *testing.T) {
	s, _ := newTestStore(t)
	if err := s.Set(context.Background(), Decision{
		State:          StateGranted,
		DecidedAt:      time.Now().UTC(),
		PromptVersion:  1,
		DecisionSource: SourcePrompt,
	}); err != nil {
		t.Fatalf("Set: %v", err)
	}
	if err := s.Clear(context.Background()); err != nil {
		t.Fatalf("Clear: %v", err)
	}
	d, err := s.Get(context.Background())
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if d.State != StateUnknown {
		t.Fatalf("State = %q, want %q after Clear", d.State, StateUnknown)
	}
}

func TestFileStore_MalformedYAML_ReturnsError(t *testing.T) {
	s, _ := newTestStore(t)
	// Force-create the path with deliberately broken YAML. The parent
	// dir may not exist yet because NewFileStore doesn't touch the FS;
	// emulate the on-disk layout we'd see post-write.
	if err := os.MkdirAll(filepath.Dir(s.Path()), 0o700); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	bad := []byte("telemetry:\n  consent: : : not yaml\n")
	if err := os.WriteFile(s.Path(), bad, 0o600); err != nil {
		t.Fatalf("seed: %v", err)
	}
	if _, err := s.Get(context.Background()); err == nil {
		t.Fatal("Get returned nil error on malformed YAML")
	}
}

func TestFileStore_PreservesOtherTopLevelKeys(t *testing.T) {
	s, _ := newTestStore(t)
	if err := os.MkdirAll(filepath.Dir(s.Path()), 0o700); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	seed := []byte("other: foo\ntelemetry:\n  consent:\n    state: denied\n    decided_at: \"2026-01-01T00:00:00Z\"\n    prompt_version: 1\n    decision_source: flag\n")
	if err := os.WriteFile(s.Path(), seed, 0o600); err != nil {
		t.Fatalf("seed: %v", err)
	}

	// Sanity: Get sees the pre-seeded denial.
	d, err := s.Get(context.Background())
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if d.State != StateDenied {
		t.Fatalf("seeded state = %q, want %q", d.State, StateDenied)
	}

	// Set replaces the consent block. `other: foo` must survive.
	if err := s.Set(context.Background(), Decision{
		State:          StateGranted,
		DecidedAt:      time.Date(2026, 6, 1, 0, 0, 0, 0, time.UTC),
		PromptVersion:  2,
		DecisionSource: SourcePrompt,
	}); err != nil {
		t.Fatalf("Set: %v", err)
	}

	b, err := os.ReadFile(s.Path())
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	if !strings.Contains(string(b), "other: foo") {
		t.Fatalf("Set nuked sibling top-level key. file =\n%s", b)
	}
	if !strings.Contains(string(b), "state: granted") {
		t.Fatalf("Set did not write granted. file =\n%s", b)
	}
}

// stubStore is a Store double driven by callbacks; lets tests inject
// canned Get results without touching disk.
type stubStore struct {
	getFn func(ctx context.Context) (Decision, error)
}

func (s stubStore) Get(ctx context.Context) (Decision, error) {
	return s.getFn(ctx)
}
func (s stubStore) Set(context.Context, Decision) error { return nil }
func (s stubStore) Clear(context.Context) error         { return nil }

func TestNewHook_RespectsState(t *testing.T) {
	var current State
	stub := stubStore{getFn: func(context.Context) (Decision, error) {
		return Decision{State: current}, nil
	}}
	h := NewHook(stub)

	current = StateGranted
	if !h.Granted(context.Background()) {
		t.Fatal("hook denied when store granted")
	}

	current = StateDenied
	if h.Granted(context.Background()) {
		t.Fatal("hook granted when store denied")
	}

	current = StateUnknown
	if h.Granted(context.Background()) {
		t.Fatal("hook granted when store unknown")
	}
}

func TestNewHook_DefaultDeniesOnError(t *testing.T) {
	stub := stubStore{getFn: func(context.Context) (Decision, error) {
		return Decision{}, errors.New("disk on fire")
	}}
	h := NewHook(stub)
	if h.Granted(context.Background()) {
		t.Fatal("hook granted on Store error; must default-deny")
	}
}

// TestNewHook_NilStore_Panics asserts fail-fast on a programming error.
// Silently default-denying would mask the wiring bug and produce
// "telemetry mysteriously inert" reports in adopter binaries.
func TestNewHook_NilStore_Panics(t *testing.T) {
	defer func() {
		if r := recover(); r == nil {
			t.Fatal("NewHook(nil) did not panic")
		}
	}()
	_ = NewHook(nil)
}

func TestInstall_HappyPath(t *testing.T) {
	// Each test owns its global-hook slot; reset on exit so we don't
	// leak a Store-backed hook into other tests that assume default-
	// deny.
	t.Cleanup(func() { telemetry.SetConsentHook(nil) })

	dir := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", dir)

	store, err := Install()
	if err != nil {
		t.Fatalf("Install: %v", err)
	}
	if store == nil {
		t.Fatal("Install returned nil Store on success")
	}

	ctx := context.Background()

	// Empty store: Unknown maps to false (default-deny baseline).
	if got := telemetry.CurrentConsentHook().Granted(ctx); got {
		t.Fatalf("CurrentConsentHook().Granted = true on fresh store; want false")
	}

	// Persist a granted decision via the returned Store; CurrentConsentHook
	// must immediately reflect it (hook reads through to disk on every call).
	if err := store.Set(ctx, Decision{
		State:          StateGranted,
		DecidedAt:      time.Now().UTC(),
		PromptVersion:  1,
		DecisionSource: SourcePrompt,
	}); err != nil {
		t.Fatalf("Set: %v", err)
	}
	if got := telemetry.CurrentConsentHook().Granted(ctx); !got {
		t.Fatal("CurrentConsentHook().Granted = false after store granted")
	}

	// Flip to denied; hook must follow.
	if err := store.Set(ctx, Decision{
		State:          StateDenied,
		DecidedAt:      time.Now().UTC(),
		PromptVersion:  1,
		DecisionSource: SourceFlag,
	}); err != nil {
		t.Fatalf("Set denied: %v", err)
	}
	if got := telemetry.CurrentConsentHook().Granted(ctx); got {
		t.Fatal("CurrentConsentHook().Granted = true after store denied")
	}
}

func TestInstall_FailureLeavesDefaultDeny(t *testing.T) {
	t.Cleanup(func() { telemetry.SetConsentHook(nil) })

	// Pre-install a permissive hook so we can detect whether Install
	// touches the global slot on the failure path. If Install leaves
	// the existing hook intact on error, this stays true; if it
	// accidentally swapped to a partially-wired Store hook (or to
	// default-deny), it would flip to false.
	telemetry.SetConsentHook(grantHookStub{})
	if !telemetry.CurrentConsentHook().Granted(context.Background()) {
		t.Fatal("test setup: pre-installed grantHookStub not active")
	}

	// Force xdg.ConfigFile to fail by making every candidate parent
	// read-only. The adrg/xdg library tries XDG_CONFIG_HOME plus a
	// platform-specific fallback chain (on darwin: HOME/Library/...,
	// /Library/..., HOME/.config/...). Pointing XDG_CONFIG_HOME and
	// HOME under a 0500 dir so MkdirAll fails for every reachable
	// candidate path.
	parent := t.TempDir()
	if err := os.Chmod(parent, 0o500); err != nil {
		t.Fatalf("chmod parent: %v", err)
	}
	t.Cleanup(func() { _ = os.Chmod(parent, 0o700) }) // let t.TempDir cleanup succeed
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(parent, "config"))
	t.Setenv("HOME", parent)

	store, err := Install()
	if err == nil {
		// Skip on the (unlikely) platform where the system-wide
		// /Library paths are writable by this UID — the failure path
		// would be unreachable from this test.
		if store != nil {
			t.Cleanup(func() { _ = store.Clear(context.Background()) })
		}
		t.Skip("xdg.ConfigFile succeeded despite read-only candidates; failure path unreachable here")
	}
	if store != nil {
		t.Fatalf("Install returned non-nil Store on error: %v", store)
	}

	// Critical assertion: the pre-installed hook is still active. A
	// failed Install must not touch the global slot.
	if !telemetry.CurrentConsentHook().Granted(context.Background()) {
		t.Fatal("Install failure mutated global hook; pre-installed hook lost")
	}
}

// grantHookStub is a ConsentHook double that always grants. Used to
// detect whether a code path (e.g. failed Install) silently rewrites
// the global hook slot.
type grantHookStub struct{}

func (grantHookStub) Granted(context.Context) bool { return true }
