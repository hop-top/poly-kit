package uxp

import (
	"slices"
	"testing"
)

func TestDefaultRegistryReturnsSingleton(t *testing.T) {
	a := DefaultRegistry()
	b := DefaultRegistry()
	if a != b {
		t.Fatal("DefaultRegistry must return the same pointer")
	}
}

func TestAllReturns15Entries(t *testing.T) {
	reg := DefaultRegistry()
	all := reg.All()
	if got := len(all); got != 15 {
		t.Fatalf("All(): got %d entries, want 15", got)
	}
}

func TestGetClaude(t *testing.T) {
	reg := DefaultRegistry()
	info, ok := reg.Get(CLIClaude)
	if !ok {
		t.Fatal("Get(claude): want ok=true")
	}
	if len(info.BinaryNames) == 0 || info.BinaryNames[0] != "claude" {
		t.Errorf("BinaryNames: got %v, want [claude]", info.BinaryNames)
	}
	if info.StoreRootPaths.Data != "~/.claude/projects/" {
		t.Errorf("StoreRootPaths.Data: got %q, want %q",
			info.StoreRootPaths.Data, "~/.claude/projects/")
	}
}

func TestGetAntigravity(t *testing.T) {
	reg := DefaultRegistry()
	info, ok := reg.Get(CLIAntigravity)
	if !ok {
		t.Fatal("Get(antigravity): want ok=true")
	}
	if len(info.BinaryNames) == 0 || info.BinaryNames[0] != "agy" {
		t.Errorf("BinaryNames: got %v, want [agy]", info.BinaryNames)
	}
	want := "~/Library/Application Support/Antigravity/"
	if info.StoreRootPaths.Data != want {
		t.Errorf("StoreRootPaths.Data: got %q, want %q",
			info.StoreRootPaths.Data, want)
	}
}

func TestGetUnknownReturnsFalse(t *testing.T) {
	reg := DefaultRegistry()
	_, ok := reg.Get("unknown")
	if ok {
		t.Fatal("Get(unknown): want ok=false")
	}
}

func TestNamesReturns15Sorted(t *testing.T) {
	reg := DefaultRegistry()
	names := reg.Names()
	if got := len(names); got != 15 {
		t.Fatalf("Names(): got %d, want 15", got)
	}
	if !slices.IsSorted(names) {
		t.Error("Names() must be sorted")
	}
}
