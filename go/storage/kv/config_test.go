package kv_test

import (
	"context"
	"path/filepath"
	"testing"

	"hop.top/kit/go/storage/kv"
)

func TestOpenSQLite(t *testing.T) {
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

func TestOpenBadger(t *testing.T) {
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

func TestOpenUnknownBackend(t *testing.T) {
	_, err := kv.Open(kv.Config{Backend: "redis"})
	if err == nil {
		t.Fatal("expected error for unknown backend")
	}
}

func TestOpenEtcdUnavailable(t *testing.T) {
	_, err := kv.Open(kv.Config{Backend: "etcd", DSN: "localhost:2379"})
	if err == nil {
		t.Fatal("expected error for etcd without build tag")
	}
}

func TestOpenTiDBUnavailable(t *testing.T) {
	_, err := kv.Open(kv.Config{Backend: "tidb", DSN: "localhost:4000"})
	if err == nil {
		t.Fatal("expected error for tidb without build tag")
	}
}

func TestOpenSQLiteMissingPath(t *testing.T) {
	_, err := kv.Open(kv.Config{Backend: "sqlite"})
	if err == nil {
		t.Fatal("expected error for missing path")
	}
}

func TestOpenBadgerMissingPath(t *testing.T) {
	_, err := kv.Open(kv.Config{Backend: "badger"})
	if err == nil {
		t.Fatal("expected error for missing path")
	}
}
