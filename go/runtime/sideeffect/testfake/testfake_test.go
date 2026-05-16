package testfake_test

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"os/exec"
	"strings"
	"testing"

	"hop.top/kit/go/runtime/sideeffect"
	"hop.top/kit/go/runtime/sideeffect/testfake"
)

// Compile-time interface conformance.
var (
	_ sideeffect.FS   = (*testfake.FS)(nil)
	_ sideeffect.HTTP = (*testfake.HTTP)(nil)
	_ sideeffect.Bus  = (*testfake.Bus)(nil)
	_ sideeffect.Exec = (*testfake.Exec)(nil)
)

func TestFS_RecordsAllCalls(t *testing.T) {
	t.Parallel()
	f := testfake.NewFS(t)
	_ = f.WriteFile("/a", []byte("hi"), 0o600)
	_ = f.MkdirAll("/b", 0o755)
	_ = f.Rename("/c", "/d")
	_ = f.Remove("/e")
	calls := f.Calls()
	if len(calls) != 4 {
		t.Fatalf("got %d calls want 4", len(calls))
	}
	wantMethods := []string{"FS.WriteFile", "FS.MkdirAll", "FS.Rename", "FS.Remove"}
	for i, want := range wantMethods {
		if calls[i].Method != want {
			t.Fatalf("call[%d].Method=%q want %q", i, calls[i].Method, want)
		}
	}
}

func TestFS_AllowListRejects(t *testing.T) {
	t.Parallel()
	// Use a sub-test via t.Run with a custom TB to capture the
	// would-be Fatal without aborting the parent test.
	tb := &capturingTB{TB: t}
	f := testfake.NewFS(tb).Allow(func(c testfake.Call) bool {
		return c.Method == "FS.WriteFile"
	})
	_ = f.WriteFile("/ok", []byte("data"), 0o600)
	if tb.fatal {
		t.Fatalf("WriteFile should be allowed")
	}
	_ = f.MkdirAll("/nope", 0o755)
	if !tb.fatal {
		t.Fatalf("MkdirAll outside allowlist should have triggered Fatalf")
	}
}

func TestHTTP_DefaultAndExpect(t *testing.T) {
	t.Parallel()
	h := testfake.NewHTTP(t)
	// Queue one expectation.
	rec := httptest.NewRecorder()
	rec.WriteHeader(http.StatusTeapot)
	h.Expect(rec.Result(), nil)

	req, _ := http.NewRequest(http.MethodPost, "https://example.test", http.NoBody)
	resp, err := h.Do(req)
	if err != nil {
		t.Fatalf("Do queued: %v", err)
	}
	if resp.StatusCode != http.StatusTeapot {
		t.Fatalf("queued status: got %d want 418", resp.StatusCode)
	}

	// After queue exhaustion, default 200.
	resp2, err := h.Do(req)
	if err != nil {
		t.Fatalf("Do default: %v", err)
	}
	if resp2.StatusCode != http.StatusOK {
		t.Fatalf("default status: got %d want 200", resp2.StatusCode)
	}
}

func TestBus_PublishRecordsAndReturnsExpect(t *testing.T) {
	t.Parallel()
	b := testfake.NewBus(t)
	want := errors.New("bad")
	b.Expect(want)
	got := b.Publish(context.Background(), "k.r.e.created", "src", 1)
	if !errors.Is(got, want) {
		t.Fatalf("Publish err: got %v want %v", got, want)
	}
	calls := b.Calls()
	if len(calls) != 1 || calls[0].Method != "Bus.Publish" {
		t.Fatalf("calls: %v", calls)
	}
}

func TestExec_RunOutputDefaults(t *testing.T) {
	t.Parallel()
	e := testfake.NewExec(t)
	if err := e.Run(exec.Command("ls")); err != nil {
		t.Fatalf("Run default: %v", err)
	}
	out, err := e.Output(exec.Command("ls"))
	if err != nil {
		t.Fatalf("Output default: %v", err)
	}
	if len(out) != 0 {
		t.Fatalf("default Output bytes: got %v want empty", out)
	}
	e.ExpectOutput([]byte("hello"), nil)
	out, _ = e.Output(exec.Command("ls"))
	if string(out) != "hello" {
		t.Fatalf("queued Output: got %q", out)
	}
}

func TestAssertCalledNotCalled(t *testing.T) {
	t.Parallel()
	f := testfake.NewFS(t)
	_ = f.WriteFile("/hello", []byte("hi"), 0o600)
	calls := f.Calls()
	testfake.AssertCalled(t, calls, func(c testfake.Call) bool {
		return c.Method == "FS.WriteFile"
	})
	testfake.AssertNotCalled(t, calls, func(c testfake.Call) bool {
		return c.Method == "FS.Remove"
	})
}

// capturingTB stands in for testing.TB so we can probe Fatalf
// without aborting the surrounding test.
type capturingTB struct {
	testing.TB
	fatal bool
	msg   string
}

func (c *capturingTB) Helper() {}
func (c *capturingTB) Fatalf(format string, args ...any) {
	c.fatal = true
	c.msg = strings.TrimSpace(format)
	_ = args
}
func (c *capturingTB) Errorf(format string, args ...any) {
	c.fatal = true
	c.msg = strings.TrimSpace(format)
	_ = args
}
func (c *capturingTB) FailNow() { c.fatal = true }
