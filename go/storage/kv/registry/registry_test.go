// Package registry_test verifies kv.Open's behavior when no driver is
// imported. It lives in its own package precisely so that the blank imports
// in the kv package's own tests do not populate the registry here — the
// missing-driver message is only reachable from a binary that lacks the
// driver.
package registry_test

import (
	"strings"
	"testing"

	"hop.top/kit/go/storage/kv"
)

// With no driver imported, the registry is empty.
func TestBackendsEmptyWithoutDrivers(t *testing.T) {
	if got := kv.Backends(); len(got) != 0 {
		t.Fatalf("Backends() = %v, want empty without driver imports", got)
	}
}

// A backend kit ships, but whose driver this binary never imported, must
// name the import path that would enable it. This is the case the old code
// mishandled by blaming a build tag that does not exist.
func TestOpenNamesDriverPackageWhenUnimported(t *testing.T) {
	tests := []struct {
		backend string
		wantPkg string
	}{
		{"sqlite", "hop.top/kit/go/storage/kv/sqlite"},
		{"badger", "hop.top/kit/go/storage/kv/badger"},
		{"etcd", "hop.top/kit/go/storage/kv/etcd"},
		{"tidb", "hop.top/kit/go/storage/kv/tidb"},
	}
	for _, tt := range tests {
		t.Run(tt.backend, func(t *testing.T) {
			_, err := kv.Open(kv.Config{Backend: tt.backend})
			if err == nil {
				t.Fatal("expected error for unimported driver")
			}
			if !strings.Contains(err.Error(), tt.wantPkg) {
				t.Errorf("error %q does not name import path %q", err, tt.wantPkg)
			}
			if strings.Contains(err.Error(), "build tag") {
				t.Errorf("error cites a build tag that does not exist: %v", err)
			}
			if strings.Contains(err.Error(), "unknown backend") {
				t.Errorf("shipped backend must not read as unknown: %v", err)
			}
		})
	}
}

// A name kit does not ship stays an unknown backend.
func TestOpenUnknownBackendStaysUnknown(t *testing.T) {
	_, err := kv.Open(kv.Config{Backend: "redis"})
	if err == nil {
		t.Fatal("expected error")
	}
	if !strings.Contains(err.Error(), "unknown backend") {
		t.Fatalf("error = %q, want unknown backend", err)
	}
}
