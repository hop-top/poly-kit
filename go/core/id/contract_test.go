package id_test

// Cross-language parity contract test for the typeid primitive.
//
// Loads contracts/typeid-v1/fixtures.json (single source of truth shared
// with the Rust, TypeScript, Python and PHP SDKs) and asserts that this
// Go SDK produces and parses the canonical typeid strings byte-for-byte.
//
// A failure here means either:
//   - the upstream go.jetify.com/typeid encoder drifted, or
//   - the contract fixtures themselves were edited without updating the
//     other-language SDKs.
// Either way the parity matrix is broken; fix the encoder or coordinate
// a fixtures.json + 5-SDK bump (tlc T-0753).

import (
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"testing"

	"github.com/google/uuid"
	typeid "go.jetify.com/typeid"

	"hop.top/kit/go/core/id"
)

type contractVector struct {
	Name   string   `json:"name"`
	Prefix string   `json:"prefix"`
	UUID   string   `json:"uuid"`
	TypeID string   `json:"typeid"`
	SkipIn []string `json:"skip_in,omitempty"`
	Note   string   `json:"note,omitempty"`
}

type contractFile struct {
	Version string           `json:"version"`
	Spec    string           `json:"spec"`
	Vectors []contractVector `json:"vectors"`
}

// loadContract finds the repo root by walking up from this test file's
// own location until contracts/typeid-v1/fixtures.json appears. Avoids
// embed-style directives (which forbid paths outside the package
// directory) and CWD-relative reads (fragile under `go test ./...`).
func loadContract(t *testing.T) contractFile {
	t.Helper()
	_, here, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("runtime.Caller(0) failed; cannot locate contracts/typeid-v1/fixtures.json")
	}
	dir := filepath.Dir(here)
	for i := 0; i < 10; i++ {
		candidate := filepath.Join(dir, "contracts", "typeid-v1", "fixtures.json")
		if _, err := os.Stat(candidate); err == nil {
			raw, err := os.ReadFile(candidate)
			if err != nil {
				t.Fatalf("read %s: %v", candidate, err)
			}
			var cf contractFile
			if err := json.Unmarshal(raw, &cf); err != nil {
				t.Fatalf("parse %s: %v", candidate, err)
			}
			return cf
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			break
		}
		dir = parent
	}
	t.Fatal("contracts/typeid-v1/fixtures.json: not found walking up from test file")
	return contractFile{}
}

func TestContract_Version(t *testing.T) {
	cf := loadContract(t)
	if cf.Version != "v1" {
		t.Errorf("contract version: want v1, got %q", cf.Version)
	}
	if cf.Spec != "jetify-typeid-v0.3" {
		t.Errorf("contract spec: want jetify-typeid-v0.3, got %q", cf.Spec)
	}
	if len(cf.Vectors) == 0 {
		t.Fatal("contract has no vectors")
	}
}

// TestContract_Generation walks every vector and verifies that the
// upstream encoder (the same one the kit's New() path delegates to)
// produces the pinned canonical typeid string for the pinned UUID. This
// is the "wire format hasn't drifted" guard for the Go SDK.
func TestContract_Generation(t *testing.T) {
	cf := loadContract(t)
	for _, v := range cf.Vectors {
		v := v
		t.Run(v.Name, func(t *testing.T) {
			if slices.Contains(v.SkipIn, "go") {
				t.Skipf("skip_in includes 'go': %s", v.Note)
			}
			tid, err := typeid.FromUUIDWithPrefix(v.Prefix, v.UUID)
			if err != nil {
				t.Fatalf("typeid.FromUUIDWithPrefix(%q, %q): %v", v.Prefix, v.UUID, err)
			}
			if got := tid.String(); got != v.TypeID {
				t.Errorf("canonical typeid drift:\n got:  %q\n want: %q\n(prefix=%q uuid=%q)",
					got, v.TypeID, v.Prefix, v.UUID)
			}
		})
	}
}

// TestContract_Parse exercises the kit's public Parse() against every
// vector. The (prefix, uuid) recovered from the canonical string must
// equal the pinned input.
func TestContract_Parse(t *testing.T) {
	cf := loadContract(t)
	for _, v := range cf.Vectors {
		v := v
		t.Run(v.Name, func(t *testing.T) {
			if slices.Contains(v.SkipIn, "go") {
				t.Skipf("skip_in includes 'go': %s", v.Note)
			}
			parsed, err := id.Parse(v.TypeID)
			if err != nil {
				t.Fatalf("id.Parse(%q): %v", v.TypeID, err)
			}
			if parsed.Prefix != v.Prefix {
				t.Errorf("prefix mismatch: want %q, got %q", v.Prefix, parsed.Prefix)
			}
			wantUUID, err := uuid.Parse(v.UUID)
			if err != nil {
				t.Fatalf("uuid.Parse fixture %q: %v", v.UUID, err)
			}
			if parsed.UUID != wantUUID {
				t.Errorf("uuid mismatch: want %s, got %s", wantUUID, parsed.UUID)
			}
		})
	}
}
