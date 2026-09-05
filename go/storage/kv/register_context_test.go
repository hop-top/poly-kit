package kv_test

import (
	"context"
	"errors"
	"slices"
	"testing"

	"hop.top/kit/go/storage/kv"
)

type stubStore struct{ kv.Store }

// registerPlain and registerCtx register a stub backend for the duration of
// one test. The registries are process-wide, so a stub left behind would
// break TestBackendsMatchesDocumentedSet, which asserts the exact shipped
// set and is the guard against doc drift.
func registerPlain(t *testing.T, name string, fn kv.Opener) {
	t.Helper()
	kv.RegisterBackend(name, fn)
	t.Cleanup(func() { kv.UnregisterBackend(name) })
}

func registerCtx(t *testing.T, name string, fn kv.ContextOpener) {
	t.Helper()
	kv.RegisterBackendContext(name, fn)
	t.Cleanup(func() { kv.UnregisterBackend(name) })
}

// A driver registered the old way must keep working through OpenContext.
// That is the compatibility promise of adding ContextOpener alongside
// Opener rather than widening Opener: an out-of-repo driver that never
// heard of ContextOpener still opens.
func TestOpenContext_FallsBackToPlainOpener(t *testing.T) {
	const name = "compat-legacy-opener"
	var called bool
	registerPlain(t, name, func(cfg kv.Config) (kv.Store, error) {
		called = true
		if cfg.Path != "legacy" {
			t.Errorf("Config not passed through: %+v", cfg)
		}
		return stubStore{}, nil
	})

	store, err := kv.OpenContext(t.Context(), kv.Config{Backend: name, Path: "legacy"})
	if err != nil {
		t.Fatalf("OpenContext on a plain Opener: %v", err)
	}
	if !called {
		t.Fatal("registered Opener was never invoked")
	}
	if store == nil {
		t.Fatal("OpenContext returned no Store")
	}
}

// An error from a plain Opener must reach the caller unchanged, so the
// fallback is a real dispatch rather than a swallow.
func TestOpenContext_PropagatesPlainOpenerError(t *testing.T) {
	const name = "compat-legacy-opener-err"
	sentinel := errors.New("legacy opener refused")
	registerPlain(t, name, func(kv.Config) (kv.Store, error) { return nil, sentinel })

	if _, err := kv.OpenContext(t.Context(), kv.Config{Backend: name}); !errors.Is(err, sentinel) {
		t.Fatalf("plain Opener error not propagated: %v", err)
	}
}

// A ContextOpener must actually receive the caller's context, not a
// substitute. Without this, registering one would look right and police
// nothing.
func TestOpenContext_PassesCallerContextToContextOpener(t *testing.T) {
	const name = "compat-ctx-opener"
	type ctxKey struct{}
	var got context.Context
	registerCtx(t, name, func(ctx context.Context, _ kv.Config) (kv.Store, error) {
		got = ctx
		return stubStore{}, nil
	})

	want := context.WithValue(t.Context(), ctxKey{}, "sentinel")
	if _, err := kv.OpenContext(want, kv.Config{Backend: name}); err != nil {
		t.Fatalf("OpenContext: %v", err)
	}
	if got == nil || got.Value(ctxKey{}) != "sentinel" {
		t.Fatal("ContextOpener did not receive the caller's context")
	}
}

// When a backend registers both, the context-aware factory wins: that
// preference is the entire reason the second registry exists.
func TestOpenContext_PrefersContextOpenerOverPlain(t *testing.T) {
	const name = "compat-both-openers"
	registerPlain(t, name, func(kv.Config) (kv.Store, error) {
		t.Error("plain Opener used despite a ContextOpener being registered")
		return stubStore{}, nil
	})
	var ctxUsed bool
	registerCtx(t, name, func(context.Context, kv.Config) (kv.Store, error) {
		ctxUsed = true
		return stubStore{}, nil
	})

	if _, err := kv.OpenContext(t.Context(), kv.Config{Backend: name}); err != nil {
		t.Fatalf("OpenContext: %v", err)
	}
	if !ctxUsed {
		t.Fatal("ContextOpener was not preferred")
	}
}

// Open has no context, but it must still reach a backend that registered
// only a ContextOpener — otherwise converting the shipped drivers would
// have broken every existing kv.Open call.
func TestOpen_ReachesContextOnlyBackend(t *testing.T) {
	const name = "compat-ctx-only"
	var called bool
	registerCtx(t, name, func(ctx context.Context, _ kv.Config) (kv.Store, error) {
		called = true
		if ctx == nil {
			t.Error("Open handed a nil context to a ContextOpener")
		}
		return stubStore{}, nil
	})

	if _, err := kv.Open(kv.Config{Backend: name}); err != nil {
		t.Fatalf("Open on a context-only backend: %v", err)
	}
	if !called {
		t.Fatal("context-only backend not reached through Open")
	}
}

// Backends() must list a name registered either way, and list it once even
// when both registries hold it.
func TestBackends_ListsBothRegistriesWithoutDuplicates(t *testing.T) {
	const ctxOnly = "compat-listed-ctx-only"
	const both = "compat-listed-both"
	registerCtx(t, ctxOnly, func(context.Context, kv.Config) (kv.Store, error) {
		return stubStore{}, nil
	})
	registerPlain(t, both, func(kv.Config) (kv.Store, error) { return stubStore{}, nil })
	registerCtx(t, both, func(context.Context, kv.Config) (kv.Store, error) {
		return stubStore{}, nil
	})

	names := kv.Backends()
	if !slices.Contains(names, ctxOnly) {
		t.Errorf("context-only backend missing from Backends(): %v", names)
	}
	if n := slices.Index(names, both); n < 0 {
		t.Errorf("dual-registered backend missing from Backends(): %v", names)
	}
	var count int
	for _, n := range names {
		if n == both {
			count++
		}
	}
	if count != 1 {
		t.Errorf("dual-registered backend listed %d times, want 1: %v", count, names)
	}
	if !slices.IsSorted(names) {
		t.Errorf("Backends() not sorted: %v", names)
	}
}

// An unregistered name must still name its remedy, and the message must
// not regress now that two registries are consulted.
func TestOpenContext_UnknownBackendStillReportsRemedy(t *testing.T) {
	_, err := kv.OpenContext(t.Context(), kv.Config{Backend: "redis"})
	if err == nil {
		t.Fatal("unknown backend accepted")
	}
	if got := err.Error(); !contains(got, "unknown backend") {
		t.Errorf("unhelpful error for an unknown backend: %q", got)
	}
}

func contains(s, sub string) bool {
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return true
		}
	}
	return false
}
