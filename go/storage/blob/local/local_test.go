package local_test

import (
	"bytes"
	"context"
	"errors"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"hop.top/kit/go/storage/blob"
	"hop.top/kit/go/storage/blob/blobtest"
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

func TestConformance(t *testing.T) {
	blobtest.Run(t, func(t *testing.T) blob.Store { return setup(t) })
}

func TestConformanceContentType(t *testing.T) {
	blobtest.RunContentType(t, func(t *testing.T) blob.Store { return setup(t) })
}

func listCT(t *testing.T, s *local.Store, prefix string) map[string]string {
	t.Helper()
	objs, err := s.List(context.Background(), prefix)
	if err != nil {
		t.Fatal(err)
	}
	m := map[string]string{}
	for _, o := range objs {
		m[o.Key] = o.ContentType
	}
	return m
}

func TestContentTypeSurvivesReopen(t *testing.T) {
	dir := t.TempDir()
	s1, _ := local.New(dir)
	if err := s1.Put(context.Background(), "a/b.json", strings.NewReader("{}"), "application/json"); err != nil {
		t.Fatal(err)
	}
	s2, _ := local.New(dir)
	if got := listCT(t, s2, "")["a/b.json"]; got != "application/json" {
		t.Fatalf("ContentType after reopen = %q", got)
	}
}

func TestLegacyStoreWithoutSidecar(t *testing.T) {
	dir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dir, "old"), 0o750); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "old", "f.txt"), []byte("hi"), 0o640); err != nil {
		t.Fatal(err)
	}
	s, _ := local.New(dir)
	got := listCT(t, s, "")
	if ct, ok := got["old/f.txt"]; !ok || ct != "" {
		t.Fatalf("legacy key: ct=%q ok=%v, want empty, listed", ct, ok)
	}
}

func TestDeleteRemovesSidecar(t *testing.T) {
	dir := t.TempDir()
	s, _ := local.New(dir)
	ctx := context.Background()
	_ = s.Put(ctx, "a/b.txt", strings.NewReader("x"), "text/plain")
	if err := s.Delete(ctx, "a/b.txt"); err != nil {
		t.Fatal(err)
	}
	var left []string
	_ = filepath.WalkDir(dir, func(p string, d os.DirEntry, err error) error {
		if err == nil && !d.IsDir() {
			left = append(left, p)
		}
		return nil
	})
	if len(left) != 0 {
		t.Fatalf("files left after Delete: %v", left)
	}
}

func TestPutWithoutTypeRemovesStaleSidecar(t *testing.T) {
	s := setup(t)
	ctx := context.Background()
	_ = s.Put(ctx, "k", strings.NewReader("1"), "text/plain")
	_ = s.Put(ctx, "k", strings.NewReader("2"), "")
	if got := listCT(t, s, "")["k"]; got != "" {
		t.Fatalf("stale ContentType %q", got)
	}
}

func TestCorruptSidecarIsEmptyNotError(t *testing.T) {
	dir := t.TempDir()
	s, _ := local.New(dir)
	ctx := context.Background()
	_ = s.Put(ctx, "k", strings.NewReader("1"), "text/plain")
	var side string
	_ = filepath.WalkDir(dir, func(p string, d os.DirEntry, err error) error {
		if err == nil && !d.IsDir() && filepath.Base(p) != "k" {
			side = p
		}
		return nil
	})
	if side == "" {
		t.Fatal("no sidecar written")
	}
	if err := os.WriteFile(side, []byte("{not json"), 0o640); err != nil {
		t.Fatal(err)
	}
	if got := listCT(t, s, "")["k"]; got != "" {
		t.Fatalf("ContentType = %q, want empty", got)
	}
}

func TestSidecarNamespaceCannotCollideWithKeys(t *testing.T) {
	s := setup(t)
	ctx := context.Background()
	// Keys that look like sidecar paths of other keys must stay ordinary.
	for _, k := range []string{"k.json", "k.json.json", "k"} {
		if err := s.Put(ctx, k, strings.NewReader(k), "text/"+k); err != nil {
			t.Fatalf("Put %s: %v", k, err)
		}
	}
	got := listCT(t, s, "")
	for _, k := range []string{"k.json", "k.json.json", "k"} {
		if got[k] != "text/"+k {
			t.Errorf("ContentType[%s] = %q", k, got[k])
		}
	}
	if len(got) != 3 {
		t.Fatalf("List = %v", got)
	}
	// The reserved metadata root is not a usable key.
	if err := s.Put(ctx, ".blob-meta/x", strings.NewReader("x"), ""); err == nil {
		t.Fatal("Put into reserved namespace: want error")
	}
	if _, err := s.Get(ctx, ".blob-meta/x"); err == nil {
		t.Fatal("Get from reserved namespace: want error")
	}
}

// On case-insensitive filesystems (macOS, Windows) and on Windows, which
// drops trailing dots and spaces, these spellings land in the real
// metadata directory, so they must be reserved too.
func TestReservedNamespaceIgnoresCaseAndTrailingDotsSpaces(t *testing.T) {
	s := setup(t)
	ctx := context.Background()
	for _, k := range []string{".BLOB-META/x", ".Blob-Meta", ".blob-meta./x", ".blob-meta /x", "a/../.BLOB-META/x"} {
		if err := s.Put(ctx, k, strings.NewReader("x"), "text/plain"); err == nil {
			t.Errorf("Put %q: want reserved-namespace error", k)
		}
	}
	// A key merely starting with the name is an ordinary key.
	if err := s.Put(ctx, ".blob-metadata/x", strings.NewReader("x"), ""); err != nil {
		t.Fatalf("Put .blob-metadata/x: %v", err)
	}
}

func TestConformanceStat(t *testing.T) {
	blobtest.RunStat(t, func(t *testing.T) blob.Store { return setup(t) })
}

func TestStatMissingIsNotExist(t *testing.T) {
	_, err := setup(t).Stat(context.Background(), "nope")
	if !errors.Is(err, fs.ErrNotExist) {
		t.Fatalf("Stat(missing) = %v, want fs.ErrNotExist like Get", err)
	}
	if !errors.Is(err, blob.ErrNotFound) {
		t.Fatalf("Stat(missing) = %v, want blob.ErrNotFound", err)
	}
}

// A missing key matches the shared sentinel and, for compat, the
// filesystem one, from Get, Stat, and Stat of a directory key.
func TestMissingKeyMatchesBothSentinels(t *testing.T) {
	ctx := context.Background()
	s := setup(t)
	if err := s.Put(ctx, "dir/leaf", strings.NewReader("x"), ""); err != nil {
		t.Fatal(err)
	}
	_, getErr := s.Get(ctx, "nope")
	_, statErr := s.Stat(ctx, "nope")
	_, dirErr := s.Stat(ctx, "dir")
	for name, err := range map[string]error{"Get": getErr, "Stat": statErr, "Stat(dir)": dirErr} {
		if !errors.Is(err, blob.ErrNotFound) {
			t.Errorf("%s: %v, want blob.ErrNotFound", name, err)
		}
		if !errors.Is(err, fs.ErrNotExist) {
			t.Errorf("%s: %v, want fs.ErrNotExist", name, err)
		}
	}
}

// Errors that are not "missing" must not claim to be.
func TestInvalidKeyIsNotNotFound(t *testing.T) {
	s := setup(t)
	_, err := s.Get(context.Background(), "../escape")
	if err == nil || errors.Is(err, blob.ErrNotFound) {
		t.Fatalf("Get(../escape) = %v, want non-NotFound error", err)
	}
}

func TestStatReadsSidecar(t *testing.T) {
	dir := t.TempDir()
	s, _ := local.New(dir)
	if err := s.Put(context.Background(), "k", strings.NewReader("abc"), "text/x-a"); err != nil {
		t.Fatal(err)
	}
	// Rewrite the sidecar out of band: Stat must report what is on disk.
	meta := filepath.Join(dir, ".blob-meta", "k.json")
	if err := os.WriteFile(meta, []byte(`{"content_type":"text/x-b"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	o, err := s.Stat(context.Background(), "k")
	if err != nil || o.ContentType != "text/x-b" {
		t.Fatalf("Stat = %+v, %v; want sidecar type text/x-b", o, err)
	}
}

func TestStatNoSidecarEmptyType(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "old"), []byte("12"), 0o600); err != nil {
		t.Fatal(err)
	}
	s, _ := local.New(dir)
	o, err := s.Stat(context.Background(), "old")
	if err != nil || o.ContentType != "" || o.Size != 2 {
		t.Fatalf("Stat = %+v, %v", o, err)
	}
}

// A key below a file cannot exist: it is missing, not an I/O failure,
// for every operation.
func TestKeyBelowAFileIsMissing(t *testing.T) {
	ctx := context.Background()
	s := setup(t)
	if err := s.Put(ctx, "file", strings.NewReader("x"), ""); err != nil {
		t.Fatal(err)
	}
	_, getErr := s.Get(ctx, "file/child")
	_, statErr := s.Stat(ctx, "file/child")
	for name, err := range map[string]error{"Get": getErr, "Stat": statErr} {
		if !errors.Is(err, blob.ErrNotFound) || !errors.Is(err, fs.ErrNotExist) {
			t.Errorf("%s: %v, want blob.ErrNotFound and fs.ErrNotExist", name, err)
		}
	}
	if ok, err := s.Exists(ctx, "file/child"); err != nil || ok {
		t.Errorf("Exists = %v, %v; want false, nil", ok, err)
	}
	if err := s.Delete(ctx, "file/child"); err != nil {
		t.Errorf("Delete = %v, want nil", err)
	}
}
