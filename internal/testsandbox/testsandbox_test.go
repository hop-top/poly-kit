package testsandbox

import (
	"os"
	"path/filepath"
	"testing"
)

func TestMain(m *testing.M) { os.Exit(Main(m)) }

// TestMainPointsEveryPerUserVarIntoOneSandbox pins what Main promises:
// HOME, each XDG base and the runtime dir sit under one fresh root
// that is none of the process's original per-user dirs.
func TestMainPointsEveryPerUserVarIntoOneSandbox(t *testing.T) {
	sandbox.Lock()
	root, orig := sandbox.root, sandbox.orig
	sandbox.Unlock()
	for _, k := range perUserVars {
		v := os.Getenv(k)
		if !within(root, v) || v == root {
			t.Errorf("%s = %q, want a dir under %q", k, v, root)
		}
		for _, o := range orig {
			if v == o {
				t.Errorf("%s still the original %q", k, o)
			}
		}
	}
	RequireSandboxed(t, os.Getenv("HOME"), os.Getenv("XDG_RUNTIME_DIR"))
}

func TestRequireSandboxedRefusesARealPerUserPath(t *testing.T) {
	sandbox.Lock()
	orig := sandbox.orig
	sandbox.Unlock()
	if len(orig) == 0 {
		t.Skip("process started with no per-user dirs")
	}
	ft := &fakeTB{TB: t}
	RequireSandboxed(ft, filepath.Join(orig[0], "tool", "control.sock"))
	if !ft.failed {
		t.Fatalf("RequireSandboxed accepted a path under %s", orig[0])
	}
}

func TestWithin(t *testing.T) {
	for _, c := range []struct {
		root, p string
		want    bool
	}{
		{"/a/b", "/a/b", true},
		{"/a/b", "/a/b/c", true},
		{"/a/b", "/a/bc", false},
		{"/a/b", "/a", false},
		{"/a/b", "/x/a/b", false},
	} {
		if got := within(c.root, c.p); got != c.want {
			t.Errorf("within(%q, %q) = %v, want %v", c.root, c.p, got, c.want)
		}
	}
}

// fakeTB records a failure instead of failing the real test.
type fakeTB struct {
	testing.TB
	failed bool
}

func (f *fakeTB) Helper()               {}
func (f *fakeTB) Errorf(string, ...any) { f.failed = true }
func (f *fakeTB) Fatal(...any)          { f.failed = true }
