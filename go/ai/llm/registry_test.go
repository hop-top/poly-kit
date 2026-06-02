package llm_test

import (
	"context"
	"errors"
	"testing"

	"hop.top/aim"
	"hop.top/kit/go/ai/llm"
)

// stubSource is an in-memory [aim.Source]; returning an empty provider map
// avoids any network and lets tests build sentinel registries cheaply.
type stubSource struct{}

func (stubSource) Fetch(_ context.Context) (map[string]*aim.Provider, error) {
	return map[string]*aim.Provider{}, nil
}

func newStubRegistry() *aim.Registry {
	return aim.NewRegistry(aim.WithSource(stubSource{}))
}

func TestDefaultRegistry_LazyConstruction(t *testing.T) {
	t.Cleanup(llm.ResetDefaultRegistry)

	want := newStubRegistry()
	var calls int
	llm.SetDefaultRegistry(func(_ context.Context) (*aim.Registry, error) {
		calls++
		return want, nil
	})

	got1, err := llm.Default(context.Background())
	if err != nil {
		t.Fatalf("Default #1: %v", err)
	}
	got2, err := llm.Default(context.Background())
	if err != nil {
		t.Fatalf("Default #2: %v", err)
	}
	if got1 != want || got2 != want {
		t.Fatalf("Default should return the provider's registry; got1=%p got2=%p want=%p", got1, got2, want)
	}
	// The provider drives instance identity — both calls hit it.
	if calls != 2 {
		t.Fatalf("provider call count: want 2, got %d", calls)
	}
}

func TestSetDefaultRegistry_Override(t *testing.T) {
	t.Cleanup(llm.ResetDefaultRegistry)

	sentinel := newStubRegistry()
	llm.SetDefaultRegistry(func(_ context.Context) (*aim.Registry, error) {
		return sentinel, nil
	})

	got, err := llm.Default(context.Background())
	if err != nil {
		t.Fatalf("Default: %v", err)
	}
	if got != sentinel {
		t.Fatalf("Default returned %p, want sentinel %p", got, sentinel)
	}
}

func TestSetDefaultRegistry_Error(t *testing.T) {
	t.Cleanup(llm.ResetDefaultRegistry)

	wantErr := errors.New("boom")
	llm.SetDefaultRegistry(func(_ context.Context) (*aim.Registry, error) {
		return nil, wantErr
	})

	got, err := llm.Default(context.Background())
	if !errors.Is(err, wantErr) {
		t.Fatalf("Default error: got %v, want %v", err, wantErr)
	}
	if got != nil {
		t.Fatalf("Default registry: got %p, want nil", got)
	}
}

func TestResetDefaultRegistry(t *testing.T) {
	t.Cleanup(llm.ResetDefaultRegistry)

	first := newStubRegistry()
	llm.SetDefaultRegistry(func(_ context.Context) (*aim.Registry, error) {
		return first, nil
	})
	if got, _ := llm.Default(context.Background()); got != first {
		t.Fatalf("pre-reset Default: got %p, want %p", got, first)
	}

	llm.ResetDefaultRegistry()

	second := newStubRegistry()
	llm.SetDefaultRegistry(func(_ context.Context) (*aim.Registry, error) {
		return second, nil
	})
	got, err := llm.Default(context.Background())
	if err != nil {
		t.Fatalf("post-reset Default: %v", err)
	}
	if got != second {
		t.Fatalf("post-reset Default: got %p, want %p (reset failed to clear provider)", got, second)
	}
}
