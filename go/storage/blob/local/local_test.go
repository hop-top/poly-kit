package local_test

import (
	"bytes"
	"context"
	"io"
	"strings"
	"testing"

	"hop.top/kit/go/storage/blob/local"
)

func setup(t *testing.T) *local.Store {
	t.Helper()
	s, err := local.New(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	return s
}

func TestPutGetRoundtrip(t *testing.T) {
	s := setup(t)
	ctx := context.Background()

	data := "hello world"
	if err := s.Put(ctx, "greet.txt", strings.NewReader(data), "text/plain"); err != nil {
		t.Fatal(err)
	}
	rc, err := s.Get(ctx, "greet.txt")
	if err != nil {
		t.Fatal(err)
	}
	defer rc.Close()
	got, _ := io.ReadAll(rc)
	if string(got) != data {
		t.Fatalf("got %q, want %q", got, data)
	}
}

func TestNestedDirs(t *testing.T) {
	s := setup(t)
	ctx := context.Background()

	if err := s.Put(ctx, "a/b/c.txt", strings.NewReader("nested"), ""); err != nil {
		t.Fatal(err)
	}
	rc, err := s.Get(ctx, "a/b/c.txt")
	if err != nil {
		t.Fatal(err)
	}
	defer rc.Close()
	got, _ := io.ReadAll(rc)
	if string(got) != "nested" {
		t.Fatalf("got %q", got)
	}
}

func TestGetMissingKey(t *testing.T) {
	s := setup(t)
	_, err := s.Get(context.Background(), "nope")
	if err == nil {
		t.Fatal("expected error for missing key")
	}
}

func TestDelete(t *testing.T) {
	s := setup(t)
	ctx := context.Background()

	_ = s.Put(ctx, "del.txt", strings.NewReader("x"), "")
	if err := s.Delete(ctx, "del.txt"); err != nil {
		t.Fatal(err)
	}
	ok, _ := s.Exists(ctx, "del.txt")
	if ok {
		t.Fatal("expected deleted")
	}
}

func TestListPrefix(t *testing.T) {
	s := setup(t)
	ctx := context.Background()

	_ = s.Put(ctx, "logs/a.log", strings.NewReader("1"), "")
	_ = s.Put(ctx, "logs/b.log", strings.NewReader("2"), "")
	_ = s.Put(ctx, "data/x.bin", strings.NewReader("3"), "")

	objs, err := s.List(ctx, "logs/")
	if err != nil {
		t.Fatal(err)
	}
	if len(objs) != 2 {
		t.Fatalf("got %d objects, want 2", len(objs))
	}
}

func TestExists(t *testing.T) {
	s := setup(t)
	ctx := context.Background()

	ok, err := s.Exists(ctx, "nope")
	if err != nil {
		t.Fatal(err)
	}
	if ok {
		t.Fatal("expected false")
	}

	_ = s.Put(ctx, "yes.txt", strings.NewReader("y"), "")
	ok, err = s.Exists(ctx, "yes.txt")
	if err != nil {
		t.Fatal(err)
	}
	if !ok {
		t.Fatal("expected true")
	}
}

func TestStreaming1MB(t *testing.T) {
	s := setup(t)
	ctx := context.Background()

	data := bytes.Repeat([]byte("A"), 1<<20)
	if err := s.Put(ctx, "big.bin", bytes.NewReader(data), "application/octet-stream"); err != nil {
		t.Fatal(err)
	}
	rc, err := s.Get(ctx, "big.bin")
	if err != nil {
		t.Fatal(err)
	}
	defer rc.Close()
	got, _ := io.ReadAll(rc)
	if len(got) != len(data) {
		t.Fatalf("got %d bytes, want %d", len(got), len(data))
	}
}
