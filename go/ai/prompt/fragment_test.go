package prompt_test

import (
	"context"
	"errors"
	"strings"
	"testing"

	"hop.top/kit/go/ai/prompt"
)

func TestMemStoreAndResolve(t *testing.T) {
	s := prompt.NewMemStore()
	s.Set("a", []byte("alpha"))
	s.Set("b", []byte("beta"))
	got, err := prompt.ResolveFragments(context.Background(), s, []string{"b", "a"})
	if err != nil || got != "beta\n---\nalpha" {
		t.Fatalf("%q %v", got, err)
	}
	if got, err = prompt.ResolveFragments(context.Background(), s, nil); err != nil || got != "" {
		t.Fatalf("%q %v", got, err)
	}
	_, err = prompt.ResolveFragments(context.Background(), s, []string{"a", "zzz"})
	if !errors.Is(err, prompt.ErrNotFound) || !strings.Contains(err.Error(), `"zzz"`) {
		t.Fatalf("%v", err)
	}
}

func TestMemStoreCopies(t *testing.T) {
	s := prompt.NewMemStore()
	in := []byte("abc")
	s.Set("a", in)
	in[0] = 'X'
	out, _ := s.Get(context.Background(), "a")
	out[1] = 'Y'
	again, _ := s.Get(context.Background(), "a")
	if string(again) != "abc" {
		t.Fatalf("store aliased a caller slice: %q", again)
	}
}

func TestStoresHonorCancel(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	s := prompt.NewMemStore()
	s.Set("a", []byte("x"))
	if _, err := s.Get(ctx, "a"); !errors.Is(err, context.Canceled) {
		t.Errorf("mem: %v", err)
	}
	if _, err := prompt.NewDirStore(t.TempDir()).Get(ctx, "a"); !errors.Is(err, context.Canceled) {
		t.Errorf("dir: %v", err)
	}
}

func TestDirStore(t *testing.T) {
	dir := t.TempDir()
	write(t, dir, "style", "be terse")
	s := prompt.NewDirStore(dir)
	b, err := s.Get(context.Background(), "style")
	if err != nil || string(b) != "be terse" {
		t.Fatalf("%q %v", b, err)
	}
	if _, err = s.Get(context.Background(), "nope"); !errors.Is(err, prompt.ErrNotFound) {
		t.Fatalf("%v", err)
	}
	for _, bad := range []string{"", "../style", "a/b", ".."} {
		if _, err = s.Get(context.Background(), bad); !errors.Is(err, prompt.ErrInvalid) {
			t.Errorf("%q: %v", bad, err)
		}
	}
}
