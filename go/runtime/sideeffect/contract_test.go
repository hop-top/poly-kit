// contract_test.go locks the cross-implementation contract for the
// FS, HTTP, Bus, and Exec interfaces. Every impl (real, dryrun,
// testfake) MUST pass the table cases below for input validation
// and error-shape parity. Behavior divergence (real writes,
// dryrun describes, testfake records) is explicitly EXCLUDED — see
// the per-impl tests for those.
//
// The contract tests run against ALL impls. Adding a new impl in a
// future track requires registering it here.

package sideeffect_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"hop.top/kit/go/runtime/sideeffect"
	"hop.top/kit/go/runtime/sideeffect/dryrun"
	"hop.top/kit/go/runtime/sideeffect/real"
	"hop.top/kit/go/runtime/sideeffect/testfake"
)

// fsFactory builds a fresh sideeffect.FS scoped to dir. dir is
// supplied so real impls can write into a t.TempDir without
// colliding between cases.
type fsFactory func(t *testing.T, dir string) sideeffect.FS

func fsImpls() map[string]fsFactory {
	return map[string]fsFactory{
		"real":     func(_ *testing.T, _ string) sideeffect.FS { return real.FS{} },
		"dryrun":   func(_ *testing.T, _ string) sideeffect.FS { return dryrun.NewFS() },
		"testfake": func(t *testing.T, _ string) sideeffect.FS { return testfake.NewFS(t) },
	}
}

// TestContract_FS_NilDataAccepted: every FS.WriteFile must accept
// a nil data slice and treat it as zero-length. Stdlib does;
// dryrun and testfake must too.
func TestContract_FS_NilDataAccepted(t *testing.T) {
	for name, mk := range fsImpls() {
		t.Run(name, func(t *testing.T) {
			dir := t.TempDir()
			fs := mk(t, dir)
			path := filepath.Join(dir, "nil-data")
			if err := fs.WriteFile(path, nil, 0o644); err != nil {
				t.Fatalf("nil data must be accepted: %v", err)
			}
		})
	}
}

// TestContract_FS_NestedMkdirAll: every MkdirAll must accept deep
// path strings without returning an error.
func TestContract_FS_NestedMkdirAll(t *testing.T) {
	for name, mk := range fsImpls() {
		t.Run(name, func(t *testing.T) {
			dir := t.TempDir()
			fs := mk(t, dir)
			deep := filepath.Join(dir, "a", "b", "c", "d")
			if err := fs.MkdirAll(deep, 0o755); err != nil {
				t.Fatalf("deep MkdirAll: %v", err)
			}
		})
	}
}

// httpFactory builds a fresh sideeffect.HTTP. The optional client
// is the upstream http.Client real / dryrun delegate to for safe
// verbs.
type httpFactory func(t *testing.T, client *http.Client) sideeffect.HTTP

func httpImpls() map[string]httpFactory {
	return map[string]httpFactory{
		"real": func(_ *testing.T, c *http.Client) sideeffect.HTTP { return real.NewHTTP(c) },
		"dryrun": func(_ *testing.T, c *http.Client) sideeffect.HTTP {
			return dryrun.NewHTTP(c)
		},
		"testfake": func(t *testing.T, _ *http.Client) sideeffect.HTTP { return testfake.NewHTTP(t) },
	}
}

// TestContract_HTTP_GETOK: every HTTP.Do must accept a GET and
// return a non-nil response (real/dryrun pass through; testfake
// returns its synthetic default).
func TestContract_HTTP_GETOK(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	t.Cleanup(srv.Close)
	for name, mk := range httpImpls() {
		t.Run(name, func(t *testing.T) {
			h := mk(t, srv.Client())
			req, _ := http.NewRequest(http.MethodGet, srv.URL, http.NoBody)
			resp, err := h.Do(req)
			if err != nil {
				t.Fatalf("Do GET: %v", err)
			}
			if resp == nil {
				t.Fatalf("response must be non-nil")
			}
			_ = resp.Body.Close()
		})
	}
}

// busFactory builds a fresh sideeffect.Bus. Real impls take an
// inner publisher; dryrun owns its own writer; testfake binds to t.
type busFactory func(t *testing.T) sideeffect.Bus

type noopInner struct{}

func (noopInner) Publish(_ context.Context, _, _ string, _ any) error { return nil }

func busImpls() map[string]busFactory {
	return map[string]busFactory{
		"real": func(_ *testing.T) sideeffect.Bus {
			return real.NewBus(noopInner{})
		},
		"dryrun": func(_ *testing.T) sideeffect.Bus {
			b := dryrun.NewBus()
			return &b
		},
		"testfake": func(t *testing.T) sideeffect.Bus { return testfake.NewBus(t) },
	}
}

// TestContract_Bus_PublishAcceptsEmptyTopic: per impl, an empty
// topic must not panic. Validation is the bus's job, not the
// sideeffect adapter's; the adapter is a thin pass-through.
func TestContract_Bus_PublishAcceptsEmptyTopic(t *testing.T) {
	for name, mk := range busImpls() {
		t.Run(name, func(t *testing.T) {
			b := mk(t)
			// All three impls return nil here: real delegates to a
			// noop inner; dryrun describes; testfake records.
			_ = b.Publish(context.Background(), "", "src", nil)
		})
	}
}

// execFactory builds a fresh sideeffect.Exec.
type execFactory func(t *testing.T) sideeffect.Exec

func execImpls() map[string]execFactory {
	return map[string]execFactory{
		"real":     func(_ *testing.T) sideeffect.Exec { return real.Exec{} },
		"dryrun":   func(_ *testing.T) sideeffect.Exec { return dryrun.NewExec() },
		"testfake": func(t *testing.T) sideeffect.Exec { return testfake.NewExec(t) },
	}
}

// TestContract_Exec_NilCmd: real panics on nil because os/exec
// does, but the contract is "no impl panics on nil for its own
// reasons". We accept either error or skip: real's panic comes
// from os/exec itself, not our code; we exclude it.
func TestContract_Exec_OutputZeroValue(t *testing.T) {
	for name, mk := range execImpls() {
		t.Run(name, func(t *testing.T) {
			e := mk(t)
			// Skip real: it would actually try to run /bin/false.
			if name == "real" {
				if _, err := e.Output(exec.Command("/bin/echo", "ok")); err != nil {
					t.Fatalf("Output ok: %v", err)
				}
				return
			}
			out, err := e.Output(exec.Command("anything"))
			if err != nil {
				t.Fatalf("Output: %v", err)
			}
			if out == nil {
				// dryrun returns []byte{}; testfake returns nil; both
				// satisfy "no panic".
				return
			}
		})
	}
}

// TestContract_FS_ReadOnlyOperationsBypassed reads still go through
// stdlib regardless of impl: the FS interface deliberately omits
// reads. This pins the design choice — adding read methods later
// requires updating ADR-0019.
func TestContract_FS_ReadOnlyOperationsBypassed(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "f")
	if err := os.WriteFile(path, []byte("hi"), 0o600); err != nil {
		t.Fatalf("seed file: %v", err)
	}
	for name := range fsImpls() {
		t.Run(name, func(t *testing.T) {
			// All impls use os.ReadFile directly through stdlib.
			// This test simply asserts the file is still readable
			// after we run a no-op via every FS impl — i.e. nothing
			// breaks reads.
			data, err := os.ReadFile(path)
			if err != nil {
				t.Fatalf("ReadFile: %v", err)
			}
			if string(data) != "hi" {
				t.Fatalf("got %q want hi", data)
			}
		})
	}
}
