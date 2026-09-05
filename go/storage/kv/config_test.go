package kv_test

import (
	"context"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"hop.top/kit/go/storage/kv"

	// Every driver kit ships is imported here so the table below exercises
	// the full documented set through Open.
	_ "hop.top/kit/go/storage/kv/badger"
	_ "hop.top/kit/go/storage/kv/etcd"
	_ "hop.top/kit/go/storage/kv/sqlite"
	_ "hop.top/kit/go/storage/kv/tidb"
)

// documentedBackends is the set the package doc and README advertise.
// Backends() must agree with it exactly; a driver added or renamed without
// updating the docs fails here.
var documentedBackends = []string{"badger", "etcd", "sqlite", "tidb"}

func TestBackendsMatchesDocumentedSet(t *testing.T) {
	got := kv.Backends()
	if !reflect.DeepEqual(got, documentedBackends) {
		t.Fatalf("Backends() = %v, want %v", got, documentedBackends)
	}
}

// Every documented backend must be reachable through Open. Each case either
// constructs a Store or fails for a reason that names the field it needs —
// never for a missing build tag, and never as an "unknown backend".
func TestOpenAcceptsEveryDocumentedBackend(t *testing.T) {
	for _, backend := range documentedBackends {
		t.Run(backend, func(t *testing.T) {
			// A Config naming only the backend is deliberately incomplete,
			// so this exercises dispatch and validation without a server.
			_, err := kv.Open(kv.Config{Backend: backend})
			if err == nil {
				t.Fatal("expected incomplete config to be rejected")
			}
			if strings.Contains(err.Error(), "unknown backend") {
				t.Fatalf("backend not registered: %v", err)
			}
			if strings.Contains(err.Error(), "build tag") {
				t.Fatalf("error cites a build tag that does not exist: %v", err)
			}
			if !strings.Contains(err.Error(), "requires") {
				t.Fatalf("error does not name the missing field: %v", err)
			}
		})
	}
}

// Configuration validation, per backend. These assert the required field is
// enforced before any network or disk work happens.
func TestOpenRequiredFields(t *testing.T) {
	tests := []struct {
		name string
		cfg  kv.Config
		want string
	}{
		{"sqlite_missing_path", kv.Config{Backend: "sqlite"}, "requires Path"},
		{"badger_missing_path", kv.Config{Backend: "badger"}, "requires Path"},
		{"etcd_missing_endpoints", kv.Config{Backend: "etcd"}, "requires Endpoints"},
		{"tidb_missing_dsn", kv.Config{Backend: "tidb"}, "requires DSN"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := kv.Open(tt.cfg)
			if err == nil {
				t.Fatal("expected error")
			}
			if !strings.Contains(err.Error(), tt.want) {
				t.Fatalf("error = %q, want it to contain %q", err, tt.want)
			}
		})
	}
}

// etcd's constructor does not dial on New, so this reaches a live Store
// without a server and proves the endpoints/prefix wiring compiles and runs.
func TestOpenEtcdConstructs(t *testing.T) {
	s, err := kv.Open(kv.Config{
		Backend:   "etcd",
		Endpoints: []string{"127.0.0.1:2379"},
		Prefix:    "app/",
	})
	if err != nil {
		t.Fatalf("Open etcd: %v", err)
	}
	if s == nil {
		t.Fatal("expected non-nil store")
	}
	defer s.Close()
}

// tidb pings in New, so without a server Open must surface the connection
// failure rather than a dispatch or validation error.
func TestOpenTiDBReportsConnectionFailure(t *testing.T) {
	_, err := kv.Open(kv.Config{
		Backend: "tidb",
		// Port 0 is never listening, so this fails fast without a server.
		DSN: "root@tcp(127.0.0.1:0)/testdb",
	})
	if err == nil {
		t.Fatal("expected connection failure")
	}
	if !strings.Contains(err.Error(), "tidb kv:") {
		t.Fatalf("error should come from the tidb driver, got %q", err)
	}
}

// An invalid table name is rejected by the driver before any DDL runs.
func TestOpenTiDBRejectsInvalidTable(t *testing.T) {
	_, err := kv.Open(kv.Config{
		Backend: "tidb",
		DSN:     "root@tcp(127.0.0.1:0)/testdb",
		Table:   "kv; DROP TABLE users",
	})
	if err == nil {
		t.Fatal("expected invalid table name to be rejected")
	}
	if !strings.Contains(err.Error(), "invalid table name") {
		t.Fatalf("error = %q, want invalid table name", err)
	}
}

func TestOpenUnknownBackend(t *testing.T) {
	_, err := kv.Open(kv.Config{Backend: "redis"})
	if err == nil {
		t.Fatal("expected error for unknown backend")
	}
	if !strings.Contains(err.Error(), "unknown backend") {
		t.Fatalf("error = %q, want unknown backend", err)
	}
	// The message lists what the caller can actually use.
	for _, backend := range documentedBackends {
		if !strings.Contains(err.Error(), backend) {
			t.Errorf("error %q does not list available backend %q", err, backend)
		}
	}
}

func TestOpenSQLiteRoundTrip(t *testing.T) {
	path := filepath.Join(t.TempDir(), "test.db")
	s, err := kv.Open(kv.Config{Backend: "sqlite", Path: path})
	if err != nil {
		t.Fatalf("Open sqlite: %v", err)
	}
	defer s.Close()

	ctx := context.Background()
	if err := s.Put(ctx, "k1", []byte("v1")); err != nil {
		t.Fatal(err)
	}
	val, ok, err := s.Get(ctx, "k1")
	if err != nil {
		t.Fatal(err)
	}
	if !ok || string(val) != "v1" {
		t.Fatalf("got ok=%v val=%q", ok, val)
	}
}

func TestOpenBadgerRoundTrip(t *testing.T) {
	dir := t.TempDir()
	s, err := kv.Open(kv.Config{Backend: "badger", Path: dir})
	if err != nil {
		t.Fatalf("Open badger: %v", err)
	}
	defer s.Close()

	ctx := context.Background()
	if err := s.Put(ctx, "k2", []byte("v2")); err != nil {
		t.Fatal(err)
	}
	val, ok, err := s.Get(ctx, "k2")
	if err != nil {
		t.Fatal(err)
	}
	if !ok || string(val) != "v2" {
		t.Fatalf("got ok=%v val=%q", ok, val)
	}
}

// The tidb table defaults rather than producing empty SQL.
func TestTiDBDefaultTable(t *testing.T) {
	if kv.DefaultTable == "" {
		t.Fatal("DefaultTable must not be empty")
	}
}
