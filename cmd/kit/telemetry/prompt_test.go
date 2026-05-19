package telemetry

import (
	"bytes"
	"context"
	"errors"
	"io"
	"strings"
	"sync"
	"testing"
	"time"

	"hop.top/kit/go/core/consent"
)

// memStore is an in-memory consent.Store sufficient for the prompt
// tests. The real FileStore is exercised in
// hop.top/kit/go/core/consent/file_store_test.go; here we only need
// to verify what Decision the prompt writes, not how it's serialized.
type memStore struct {
	mu      sync.Mutex
	current consent.Decision // zero value = "no record"
	hasRec  bool

	setErr error // injected error from Set, if non-nil

	setCalls int
}

func (s *memStore) Get(ctx context.Context) (consent.Decision, error) {
	if err := ctx.Err(); err != nil {
		return consent.Decision{}, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if !s.hasRec {
		return consent.Decision{State: consent.StateUnknown}, nil
	}
	return s.current, nil
}

func (s *memStore) Set(ctx context.Context, d consent.Decision) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.setErr != nil {
		return s.setErr
	}
	s.current = d
	s.hasRec = true
	s.setCalls++
	return nil
}

func (s *memStore) Clear(ctx context.Context) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.current = consent.Decision{}
	s.hasRec = false
	return nil
}

func (s *memStore) snapshot() (consent.Decision, bool, int) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.current, s.hasRec, s.setCalls
}

// fixedTime returns a deterministic "now" for assertions on
// DecidedAt. We don't care what the time is, only that the prompt
// stamps SOMETHING non-zero so downstream "when did this happen?"
// queries work.
func fixedTime() time.Time {
	return time.Date(2026, 5, 19, 12, 0, 0, 0, time.UTC)
}

// envFunc builds a consent.EnvProvider from a map. Same shape as
// consent.MapEnv but local so the tests are self-contained.
func envFunc(m map[string]string) consent.EnvProvider {
	return func(k string) string { return m[k] }
}

// baseDeps is the canonical "TTY, no env overrides, deterministic
// clock" promptDeps. Tests override individual fields.
func baseDeps(in io.Reader, env map[string]string, isTTY bool) promptDeps {
	return promptDeps{
		in:    in,
		out:   &bytes.Buffer{},
		env:   envFunc(env),
		isTTY: func() bool { return isTTY },
		now:   fixedTime,
	}
}

func TestPrompt_NonTTYAutoDeny(t *testing.T) {
	t.Parallel()
	store := &memStore{}
	deps := baseDeps(strings.NewReader(""), nil, false /* not a TTY */)

	d, err := promptInternal(context.Background(), store, deps)
	if err != nil {
		t.Fatalf("promptInternal: %v", err)
	}

	if d.State != consent.StateDenied {
		t.Errorf("State = %q, want %q", d.State, consent.StateDenied)
	}
	if d.DecisionSource != consent.SourceConfig {
		t.Errorf("DecisionSource = %q, want %q", d.DecisionSource, consent.SourceConfig)
	}
	if d.PromptVersion != PromptVersion {
		t.Errorf("PromptVersion = %d, want %d", d.PromptVersion, PromptVersion)
	}
	if d.DecidedAt.IsZero() {
		t.Error("DecidedAt is zero; want stamped")
	}
	persisted, has, calls := store.snapshot()
	if !has {
		t.Fatal("decision was not persisted")
	}
	if persisted != d {
		t.Errorf("persisted = %+v, want %+v", persisted, d)
	}
	if calls != 1 {
		t.Errorf("Set called %d times, want 1", calls)
	}
}

func TestPrompt_DoNotTrackSkips(t *testing.T) {
	t.Parallel()
	store := &memStore{}
	// Even with a fully-typed "yes" on stdin and TTY claimed, env
	// must win: this is the non-overridable rail.
	deps := baseDeps(
		strings.NewReader("y\n"),
		map[string]string{"DO_NOT_TRACK": "1"},
		true,
	)

	d, err := promptInternal(context.Background(), store, deps)
	if err != nil {
		t.Fatalf("promptInternal: %v", err)
	}
	if d.State != consent.StateDenied {
		t.Errorf("State = %q, want denied", d.State)
	}
	if d.DecisionSource != consent.SourceEnv {
		t.Errorf("DecisionSource = %q, want env", d.DecisionSource)
	}
	if _, has, _ := store.snapshot(); !has {
		t.Error("decision was not persisted")
	}
}

func TestPrompt_ModeOffSkips(t *testing.T) {
	t.Parallel()
	store := &memStore{}
	deps := baseDeps(
		strings.NewReader("y\n"),
		map[string]string{"KIT_TELEMETRY_MODE": "off"},
		true,
	)

	d, err := promptInternal(context.Background(), store, deps)
	if err != nil {
		t.Fatalf("promptInternal: %v", err)
	}
	if d.State != consent.StateDenied {
		t.Errorf("State = %q, want denied", d.State)
	}
	if d.DecisionSource != consent.SourceEnv {
		t.Errorf("DecisionSource = %q, want env", d.DecisionSource)
	}
}

func TestPrompt_PersistedFreshSkips(t *testing.T) {
	t.Parallel()
	preseed := consent.Decision{
		State:          consent.StateGranted,
		DecidedAt:      time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC),
		PromptVersion:  PromptVersion,
		DecisionSource: consent.SourcePrompt,
	}
	store := &memStore{current: preseed, hasRec: true}
	deps := baseDeps(strings.NewReader(""), nil, true)

	d, err := promptInternal(context.Background(), store, deps)
	if err != nil {
		t.Fatalf("promptInternal: %v", err)
	}
	if d != preseed {
		t.Errorf("returned = %+v, want preseed %+v", d, preseed)
	}
	if _, _, calls := store.snapshot(); calls != 0 {
		t.Errorf("Set was called %d times; fresh persisted decision should be a no-op", calls)
	}
}

func TestPrompt_StalePromptVersionReprompts(t *testing.T) {
	t.Parallel()
	// Stale: persisted at version 0, code at PromptVersion=1.
	preseed := consent.Decision{
		State:          consent.StateGranted,
		DecidedAt:      time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC),
		PromptVersion:  0,
		DecisionSource: consent.SourcePrompt,
	}
	store := &memStore{current: preseed, hasRec: true}
	// Non-TTY: cannot re-prompt → must auto-deny at current version.
	deps := baseDeps(strings.NewReader(""), nil, false)

	d, err := promptInternal(context.Background(), store, deps)
	if err != nil {
		t.Fatalf("promptInternal: %v", err)
	}

	if d.PromptVersion != PromptVersion {
		t.Errorf("PromptVersion = %d, want %d", d.PromptVersion, PromptVersion)
	}
	if d.State != consent.StateDenied {
		t.Errorf("State = %q, want denied (non-TTY can't grant on re-prompt)", d.State)
	}
	if d.DecisionSource != consent.SourceConfig {
		t.Errorf("DecisionSource = %q, want config", d.DecisionSource)
	}
	persisted, _, calls := store.snapshot()
	if calls != 1 {
		t.Errorf("Set called %d times, want 1 fresh write", calls)
	}
	if persisted.PromptVersion != PromptVersion {
		t.Errorf("persisted PromptVersion = %d, want %d", persisted.PromptVersion, PromptVersion)
	}
}

func TestPrompt_HappyGranted(t *testing.T) {
	t.Parallel()
	store := &memStore{}
	out := &bytes.Buffer{}
	deps := promptDeps{
		in:    strings.NewReader("y\n"),
		out:   out,
		env:   envFunc(nil),
		isTTY: func() bool { return true },
		now:   fixedTime,
	}

	d, err := promptInternal(context.Background(), store, deps)
	if err != nil {
		t.Fatalf("promptInternal: %v", err)
	}
	if d.State != consent.StateGranted {
		t.Errorf("State = %q, want granted", d.State)
	}
	if d.DecisionSource != consent.SourcePrompt {
		t.Errorf("DecisionSource = %q, want prompt", d.DecisionSource)
	}
	if d.PromptVersion != PromptVersion {
		t.Errorf("PromptVersion = %d, want %d", d.PromptVersion, PromptVersion)
	}
	// Cheap copy-shipped check: the disclosure must mention
	// DO_NOT_TRACK so the user knows the escape hatch exists.
	if !strings.Contains(out.String(), "DO_NOT_TRACK") {
		t.Error("prompt output did not mention DO_NOT_TRACK")
	}
	if _, has, _ := store.snapshot(); !has {
		t.Error("decision was not persisted")
	}
}

func TestPrompt_HappyDenied(t *testing.T) {
	t.Parallel()
	store := &memStore{}
	deps := promptDeps{
		in:    strings.NewReader("n\n"),
		out:   &bytes.Buffer{},
		env:   envFunc(nil),
		isTTY: func() bool { return true },
		now:   fixedTime,
	}

	d, err := promptInternal(context.Background(), store, deps)
	if err != nil {
		t.Fatalf("promptInternal: %v", err)
	}
	if d.State != consent.StateDenied {
		t.Errorf("State = %q, want denied", d.State)
	}
	if d.DecisionSource != consent.SourcePrompt {
		t.Errorf("DecisionSource = %q, want prompt", d.DecisionSource)
	}
}

func TestPrompt_EmptyResponseTakesDefault(t *testing.T) {
	t.Parallel()
	store := &memStore{}
	deps := promptDeps{
		// Just enter: scanner sees "\n" → empty trimmed line.
		in:    strings.NewReader("\n"),
		out:   &bytes.Buffer{},
		env:   envFunc(nil),
		isTTY: func() bool { return true },
		now:   fixedTime,
	}

	d, err := promptInternal(context.Background(), store, deps)
	if err != nil {
		t.Fatalf("promptInternal: %v", err)
	}
	if d.State != consent.StateDenied {
		t.Errorf("State = %q, want denied (default highlighted is No)", d.State)
	}
	if d.DecisionSource != consent.SourcePrompt {
		t.Errorf("DecisionSource = %q, want prompt", d.DecisionSource)
	}
}

func TestPrompt_StoreErrorSurfaces(t *testing.T) {
	t.Parallel()
	// Bonus rail: persistence failures bubble up, but the in-memory
	// Decision is still returned so callers can use it for this run.
	boom := errors.New("disk full")
	store := &memStore{setErr: boom}
	deps := baseDeps(strings.NewReader(""), nil, false)

	d, err := promptInternal(context.Background(), store, deps)
	if err == nil {
		t.Fatal("expected error, got nil")
	}
	if !errors.Is(err, boom) {
		t.Errorf("err = %v, want wrap of %v", err, boom)
	}
	if d.State != consent.StateDenied {
		t.Errorf("State = %q, want denied (callable even on persist failure)", d.State)
	}
}

func TestPrompt_ContextCancelled(t *testing.T) {
	t.Parallel()
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	store := &memStore{}
	deps := baseDeps(strings.NewReader(""), nil, false)

	_, err := promptInternal(ctx, store, deps)
	if !errors.Is(err, context.Canceled) {
		t.Errorf("err = %v, want context.Canceled", err)
	}
}
