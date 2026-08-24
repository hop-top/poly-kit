package local_test

import (
	"bytes"
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
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

// failingReader yields n bytes then fails, simulating an interrupted write.
type failingReader struct {
	data []byte
	n    int
}

func (f *failingReader) Read(p []byte) (int, error) {
	if f.n <= 0 {
		return 0, errors.New("boom")
	}
	k := copy(p, f.data[:min(len(p), f.n)])
	f.n -= k
	return k, nil
}

func TestPutFailureLeavesPreviousValue(t *testing.T) {
	s := setup(t)
	ctx := context.Background()

	if err := s.Put(ctx, "k.txt", strings.NewReader("original"), ""); err != nil {
		t.Fatal(err)
	}

	err := s.Put(ctx, "k.txt", &failingReader{data: bytes.Repeat([]byte("B"), 64), n: 32}, "")
	if err == nil {
		t.Fatal("expected write error")
	}

	rc, err := s.Get(ctx, "k.txt")
	if err != nil {
		t.Fatal(err)
	}
	defer rc.Close()
	got, _ := io.ReadAll(rc)
	if string(got) != "original" {
		t.Fatalf("got %q, want previous value intact", got)
	}
}

func TestPutFailureLeavesNoKeyAndNoTemp(t *testing.T) {
	dir := t.TempDir()
	s, err := local.New(dir)
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()

	if err := s.Put(ctx, "new.txt", &failingReader{data: []byte("xxxx"), n: 2}, ""); err == nil {
		t.Fatal("expected write error")
	}

	ok, err := s.Exists(ctx, "new.txt")
	if err != nil {
		t.Fatal(err)
	}
	if ok {
		t.Fatal("failed Put must not create the destination key")
	}

	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 0 {
		t.Fatalf("temp file left behind: %v", entries)
	}
}

func TestPutOverwriteNeverObservedPartial(t *testing.T) {
	s := setup(t)
	ctx := context.Background()

	small := "v1"
	large := strings.Repeat("Z", 1<<20)
	if err := s.Put(ctx, "atomic.bin", strings.NewReader(small), ""); err != nil {
		t.Fatal(err)
	}

	done := make(chan struct{})
	go func() {
		defer close(done)
		for i := 0; i < 20; i++ {
			_ = s.Put(ctx, "atomic.bin", strings.NewReader(large), "")
			_ = s.Put(ctx, "atomic.bin", strings.NewReader(small), "")
		}
	}()

	for {
		select {
		case <-done:
			return
		default:
		}
		rc, err := s.Get(ctx, "atomic.bin")
		if err != nil {
			t.Errorf("get during concurrent put: %v", err)
			return
		}
		got, err := io.ReadAll(rc)
		rc.Close()
		if err != nil {
			t.Errorf("read during concurrent put: %v", err)
			return
		}
		if string(got) != small && string(got) != large {
			t.Errorf("observed partial blob of %d bytes", len(got))
			return
		}
	}
}

func TestListSkipsTempFiles(t *testing.T) {
	dir := t.TempDir()
	s, err := local.New(dir)
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()

	if err := s.Put(ctx, "real.txt", strings.NewReader("r"), ""); err != nil {
		t.Fatal(err)
	}
	// Simulate a crash-orphaned staging file.
	if err := os.WriteFile(filepath.Join(dir, ".real.txt.123.tmp"), []byte("junk"), 0o600); err != nil {
		t.Fatal(err)
	}

	objs, err := s.List(ctx, "")
	if err != nil {
		t.Fatal(err)
	}
	if len(objs) != 1 || objs[0].Key != "real.txt" {
		t.Fatalf("got %+v, want only real.txt", objs)
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
