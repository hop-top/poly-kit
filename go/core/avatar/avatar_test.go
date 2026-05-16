package avatar

import (
	"context"
	"strings"
	"testing"
)

func TestDefault_DicebearRegistered(t *testing.T) {
	if got := DefaultProvider(); got != DicebearProviderName {
		t.Fatalf("expected default %q, got %q", DicebearProviderName, got)
	}
	p, ok := LookupProvider(DicebearProviderName)
	if !ok {
		t.Fatal("dicebear not registered")
	}
	if p.Name() != DicebearProviderName {
		t.Errorf("provider Name() = %q, want %q", p.Name(), DicebearProviderName)
	}
}

func TestGenerate_DefaultProvider(t *testing.T) {
	url, err := Generate(context.Background(), Options{Seed: "noor"})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(url, "dicebear.com") {
		t.Errorf("expected dicebear URL, got %s", url)
	}
	if !strings.Contains(url, "shapes") {
		t.Errorf("expected default style 'shapes', got %s", url)
	}
	if !strings.Contains(url, "seed=noor") {
		t.Errorf("expected seed query, got %s", url)
	}
}

func TestGenerate_AllOptions(t *testing.T) {
	url, err := Generate(context.Background(), Options{
		Seed:   "kai",
		Style:  "bottts",
		Size:   256,
		Format: "png",
		Extra:  map[string]string{"backgroundColor": "transparent"},
	})
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"bottts", "png", "size=256", "seed=kai", "backgroundColor=transparent"} {
		if !strings.Contains(url, want) {
			t.Errorf("url missing %q: %s", want, url)
		}
	}
}

func TestGenerate_MissingSeed(t *testing.T) {
	_, err := Generate(context.Background(), Options{})
	if err == nil {
		t.Fatal("expected error on empty seed")
	}
}

func TestGenerate_UnknownProvider(t *testing.T) {
	_, err := Generate(context.Background(), Options{Seed: "x", Provider: "nope"})
	if err == nil {
		t.Fatal("expected error on unknown provider")
	}
}

func TestSetDefaultProvider(t *testing.T) {
	orig := DefaultProvider()
	t.Cleanup(func() { _ = SetDefaultProvider(orig) })

	if err := SetDefaultProvider("nope"); err == nil {
		t.Error("expected error setting unknown default")
	}

	// Re-set to the same valid provider should succeed.
	if err := SetDefaultProvider(DicebearProviderName); err != nil {
		t.Errorf("setting valid default: %v", err)
	}
}

func TestProviders_IncludesDicebear(t *testing.T) {
	names := Providers()
	found := false
	for _, n := range names {
		if n == DicebearProviderName {
			found = true
			break
		}
	}
	if !found {
		t.Errorf("Providers() = %v, missing dicebear", names)
	}
}

func TestDicebearStyles(t *testing.T) {
	p, _ := LookupProvider(DicebearProviderName)
	styles := p.Styles()
	if len(styles) == 0 {
		t.Fatal("expected non-empty style list")
	}
	for _, s := range []string{"shapes", "bottts", "identicon"} {
		found := false
		for _, x := range styles {
			if x == s {
				found = true
				break
			}
		}
		if !found {
			t.Errorf("expected style %q in list, got %v", s, styles)
		}
	}
}

type stubProvider struct {
	name string
	tag  string
}

func (s stubProvider) Name() string                                          { return s.name }
func (s stubProvider) Styles() []string                                      { return nil }
func (s stubProvider) Generate(_ context.Context, _ Options) (string, error) { return s.tag, nil }

func TestRegisterProvider_Replaces(t *testing.T) {
	first := stubProvider{name: "stub", tag: "first"}
	second := stubProvider{name: "stub", tag: "second"}
	RegisterProvider(first)
	RegisterProvider(second)

	got, err := Generate(context.Background(), Options{Seed: "x", Provider: "stub"})
	if err != nil {
		t.Fatal(err)
	}
	if got != "second" {
		t.Errorf("expected replacement provider, got %q", got)
	}
}

func TestGenerate_Deterministic(t *testing.T) {
	a, _ := Generate(context.Background(), Options{Seed: "noor"})
	b, _ := Generate(context.Background(), Options{Seed: "noor"})
	if a != b {
		t.Errorf("non-deterministic: %s vs %s", a, b)
	}
}
