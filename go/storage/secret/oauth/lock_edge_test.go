package oauth

import (
	"context"
	"os"
	"path/filepath"
	"runtime"
	"sync/atomic"
	"testing"
	"time"

	"hop.top/kit/go/storage/secret"
	"hop.top/kit/go/storage/secret/memory"
)

// badLockDirs returns lock directories the lock must refuse or fail on.
func badLockDirs(t *testing.T) map[string]string {
	t.Helper()
	file := filepath.Join(t.TempDir(), "file")
	if err := os.WriteFile(file, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	dirs := map[string]string{"parent is a file": filepath.Join(file, "locks")}
	if runtime.GOOS != "windows" {
		open := filepath.Join(t.TempDir(), "open")
		if err := os.Mkdir(open, 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.Chmod(open, 0o777); err != nil {
			t.Fatal(err)
		}
		dirs["world writable"] = open
		grp := filepath.Join(t.TempDir(), "grp")
		if err := os.Mkdir(grp, 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.Chmod(grp, 0o770); err != nil {
			t.Fatal(err)
		}
		dirs["group writable"] = grp
	}
	return dirs
}

func TestLockRefRefusesUnsafeDir(t *testing.T) {
	for name, dir := range badLockDirs(t) {
		if unlock, err := lockRef(context.Background(), dir, "g"); err == nil {
			unlock()
			t.Errorf("%s: lock taken", name)
		}
	}
	dir := filepath.Join(t.TempDir(), "locks")
	unlock, err := lockRef(context.Background(), dir, "g")
	if err != nil {
		t.Fatalf("fresh private dir: %v", err)
	}
	unlock()
}

// Non-rotating grants only cache: without a usable lock they still mint.
// A rotating grant needs the lock to serialize write-back and fails.
func TestLockFailureByGrantKind(t *testing.T) {
	for name, dir := range badLockDirs(t) {
		var calls atomic.Int32
		srv := srcServer(t, 3600, &calls)
		st := &wStore{Store: memory.New()}
		ccSetup(t, st, srv.URL)
		ctx := context.Background()
		src, err := TokenSource(ctx, st, "g", WithHTTPClient(srv.Client()), WithLockDir(dir))
		if err != nil {
			t.Fatal(err)
		}
		if tok, err := src.Token(ctx); err != nil || tok.AccessToken != "at-1" {
			t.Errorf("%s: client_credentials: %+v %v, want a minted token", name, tok, err)
		}

		rs := newRotServer(t, "rt-fake-0", 0)
		rst := &wStore{Store: memory.New()}
		rtSetup(t, rst, rs.URL, "rt-fake-0")
		rsrc, err := TokenSource(ctx, rst, "g", WithHTTPClient(rs.Client()), WithLockDir(dir),
			WithWriteBackRetry(1, time.Millisecond))
		if err != nil {
			t.Fatal(err)
		}
		if _, err := rsrc.Token(ctx); err == nil {
			t.Errorf("%s: refresh_token minted without the lock", name)
		}
		if rs.calls != 0 {
			t.Errorf("%s: provider called %d times without the lock", name, rs.calls)
		}
	}
}

var _ secret.MutableStore = (*wStore)(nil)
