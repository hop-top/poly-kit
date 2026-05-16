package registry_test

import (
	"context"
	"testing"

	"hop.top/kit/go/ai/ext"
	"hop.top/kit/go/ai/ext/registry"
)

// stub implements ext.Extension for testing.
type stub struct {
	name string
	caps ext.Capability
}

func (s *stub) Meta() ext.Metadata           { return ext.Metadata{Name: s.name, Version: "0.1.0"} }
func (s *stub) Capabilities() ext.Capability { return s.caps }
func (s *stub) Init(context.Context) error   { return nil }
func (s *stub) Close() error                 { return nil }

func newStub(name string, caps ext.Capability) *stub {
	return &stub{name: name, caps: caps}
}

func TestRegisterAndGet(t *testing.T) {
	r := registry.New()
	s := newStub("alpha", ext.CapRegistry)

	r.Register(s)

	got, ok := r.Get("alpha")
	if !ok {
		t.Fatal("expected extension to be found")
	}
	if got.Meta().Name != "alpha" {
		t.Fatalf("expected name %q, got %q", "alpha", got.Meta().Name)
	}
}

func TestGetMissing(t *testing.T) {
	r := registry.New()

	_, ok := r.Get("nonexistent")
	if ok {
		t.Fatal("expected extension not to be found")
	}
}

func TestDuplicatePanics(t *testing.T) {
	r := registry.New()
	s := newStub("beta", ext.CapRegistry)
	r.Register(s)

	defer func() {
		if rec := recover(); rec == nil {
			t.Fatal("expected panic on duplicate registration")
		}
	}()

	r.Register(newStub("beta", ext.CapRegistry))
}

func TestListReturnsAll(t *testing.T) {
	r := registry.New()
	r.Register(newStub("one", ext.CapRegistry))
	r.Register(newStub("two", ext.CapRegistry))
	r.Register(newStub("three", ext.CapRegistry))

	got := r.List()
	if len(got) != 3 {
		t.Fatalf("expected 3 extensions, got %d", len(got))
	}

	// Verify registration order is preserved.
	want := []string{"one", "two", "three"}
	for i, name := range want {
		if got[i].Meta().Name != name {
			t.Errorf("index %d: expected %q, got %q", i, name, got[i].Meta().Name)
		}
	}
}

func TestMustGetPanicsOnMissing(t *testing.T) {
	r := registry.New()

	defer func() {
		if rec := recover(); rec == nil {
			t.Fatal("expected panic on MustGet for missing extension")
		}
	}()

	r.MustGet("ghost")
}

func TestMustGetReturnsExtension(t *testing.T) {
	r := registry.New()
	r.Register(newStub("gamma", ext.CapRegistry))

	got := r.MustGet("gamma")
	if got.Meta().Name != "gamma" {
		t.Fatalf("expected %q, got %q", "gamma", got.Meta().Name)
	}
}

func TestRejectWithoutCapRegistry(t *testing.T) {
	r := registry.New()

	defer func() {
		if rec := recover(); rec == nil {
			t.Fatal("expected panic when registering without CapRegistry")
		}
	}()

	r.Register(newStub("nocap", ext.CapHook))
}

func TestMultiCapIncludingRegistry(t *testing.T) {
	r := registry.New()
	s := newStub("multi", ext.CapRegistry|ext.CapHook)

	r.Register(s)

	got, ok := r.Get("multi")
	if !ok {
		t.Fatal("expected extension with multiple caps to be found")
	}
	if !got.Capabilities().Has(ext.CapRegistry) {
		t.Fatal("expected CapRegistry to be set")
	}
	if !got.Capabilities().Has(ext.CapHook) {
		t.Fatal("expected CapHook to be set")
	}
}
