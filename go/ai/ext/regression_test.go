//go:build integration

package ext_test

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"hop.top/kit/go/ai/ext"
	"hop.top/kit/go/ai/ext/config"
	"hop.top/kit/go/ai/ext/discover"
)

// --- Fix 1: config Settings() returns defensive copy ---

func TestRegression_Settings_ReturnsCopy(t *testing.T) {
	s := config.NewStore()
	yaml := `extensions:
  myext:
    enabled: true
    settings:
      key: original
`
	if err := s.Load(strings.NewReader(yaml)); err != nil {
		t.Fatalf("Load: %v", err)
	}

	// Mutate the returned map.
	got := s.Settings("myext")
	got["key"] = "mutated"
	got["extra"] = "injected"

	// Internal state must be unchanged.
	fresh := s.Settings("myext")
	if fresh["key"] != "original" {
		t.Errorf("internal key mutated: got %q, want %q", fresh["key"], "original")
	}
	if _, ok := fresh["extra"]; ok {
		t.Error("injected key leaked into internal state")
	}
}

// --- Fix 2: config All() deep-copies Settings maps ---

func TestRegression_All_DeepCopiesSettings(t *testing.T) {
	s := config.NewStore()
	yaml := `extensions:
  plug:
    enabled: true
    settings:
      color: red
`
	if err := s.Load(strings.NewReader(yaml)); err != nil {
		t.Fatalf("Load: %v", err)
	}

	all := s.All()
	all["plug"].Settings["color"] = "blue"

	// Internal state must be unchanged.
	fresh := s.Settings("plug")
	if fresh["color"] != "red" {
		t.Errorf("All() did not deep-copy: color = %q, want %q", fresh["color"], "red")
	}
}

// --- Fix 3: Capability.String() handles combined bitmasks ---

func TestRegression_Capability_String(t *testing.T) {
	tests := []struct {
		cap  ext.Capability
		want string
	}{
		{ext.CapRegistry, "registry"},
		{ext.CapHook, "hook"},
		{ext.CapDiscover, "discover"},
		{ext.CapConfig, "config"},
		{ext.CapRegistry | ext.CapHook, "registry|hook"},
		{ext.CapRegistry | ext.CapHook | ext.CapDiscover | ext.CapConfig, "registry|hook|discover|config"},
		{0, "none"},
		{1 << 7, "unknown"},
	}
	for _, tt := range tests {
		got := tt.cap.String()
		if got != tt.want {
			t.Errorf("Capability(%d).String() = %q, want %q", int(tt.cap), got, tt.want)
		}
	}
}

// --- Fix 4: Manager.InitAll wraps sentinel error with extension name ---

func TestRegression_InitAll_WrapsSentinelWithName(t *testing.T) {
	m := ext.NewManager(nil)
	sentinel := errors.New("kaboom")
	m.Add(&stubExt{name: "badext", caps: ext.CapRegistry, initE: sentinel})

	err := m.InitAll(context.Background())
	if err == nil {
		t.Fatal("expected error from InitAll")
	}
	if !errors.Is(err, sentinel) {
		t.Errorf("error chain should contain sentinel; got: %v", err)
	}
	if !strings.Contains(err.Error(), "badext") {
		t.Errorf("error should mention extension name 'badext'; got: %s", err.Error())
	}
}

// --- Fix 5: Found.Enrich() populates metadata from Interrogate ---

func TestRegression_Enrich_PopulatesMeta(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("shell scripts not supported on Windows")
	}

	dir := t.TempDir()
	script := `#!/bin/sh
echo '{"name":"enriched","version":"2.0.0","description":"enriched desc","capabilities":["discover"]}'
`
	bin := filepath.Join(dir, "kit-enrich")
	if err := os.WriteFile(bin, []byte(script), 0755); err != nil {
		t.Fatal(err)
	}

	f := &discover.Found{
		Name: "enrich",
		Path: bin,
	}

	// Before Enrich, Meta() returns synthesised data.
	pre := f.Meta()
	if pre.Description != "" {
		t.Errorf("pre-enrich description should be empty, got %q", pre.Description)
	}

	if err := f.Enrich(); err != nil {
		t.Fatalf("Enrich: %v", err)
	}

	post := f.Meta()
	if post.Name != "enriched" {
		t.Errorf("post-enrich Name = %q, want %q", post.Name, "enriched")
	}
	if post.Version != "2.0.0" {
		t.Errorf("post-enrich Version = %q, want %q", post.Version, "2.0.0")
	}
	if post.Description != "enriched desc" {
		t.Errorf("post-enrich Description = %q, want %q", post.Description, "enriched desc")
	}
}
