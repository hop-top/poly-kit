package real_test

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"hop.top/kit/go/runtime/sideeffect"
	"hop.top/kit/go/runtime/sideeffect/real"
)

// Compile-time interface conformance checks.
var (
	_ sideeffect.FS   = real.FS{}
	_ sideeffect.HTTP = real.HTTP{}
	_ sideeffect.Bus  = real.Bus{}
	_ sideeffect.Exec = real.Exec{}
)

func TestRealFS_WriteFileMkdirAllRenameRemove(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	fs := real.FS{}

	if err := fs.MkdirAll(filepath.Join(dir, "a", "b"), 0o755); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}
	src := filepath.Join(dir, "a", "b", "src.txt")
	if err := fs.WriteFile(src, []byte("hi"), 0o600); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	dst := filepath.Join(dir, "a", "b", "dst.txt")
	if err := fs.Rename(src, dst); err != nil {
		t.Fatalf("Rename: %v", err)
	}
	if _, err := os.Stat(dst); err != nil {
		t.Fatalf("dst missing after Rename: %v", err)
	}
	if err := fs.Remove(dst); err != nil {
		t.Fatalf("Remove: %v", err)
	}
	if _, err := os.Stat(dst); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("dst still present after Remove: err=%v", err)
	}
}

func TestRealHTTP_Do_DefaultClient(t *testing.T) {
	t.Parallel()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusTeapot)
	}))
	t.Cleanup(srv.Close)

	req, err := http.NewRequest(http.MethodGet, srv.URL, http.NoBody)
	if err != nil {
		t.Fatalf("NewRequest: %v", err)
	}
	resp, err := real.HTTP{}.Do(req)
	if err != nil {
		t.Fatalf("Do: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusTeapot {
		t.Fatalf("status: got %d want %d", resp.StatusCode, http.StatusTeapot)
	}
}

type stubPublisher struct {
	gotTopic   string
	gotSource  string
	gotPayload any
}

func (s *stubPublisher) Publish(_ context.Context, topic, source string, payload any) error {
	s.gotTopic = topic
	s.gotSource = source
	s.gotPayload = payload
	return nil
}

func TestRealBus_Publish_Delegates(t *testing.T) {
	t.Parallel()
	stub := &stubPublisher{}
	b := real.NewBus(stub)
	if err := b.Publish(context.Background(), "kit.runtime.entity.created", "test", 42); err != nil {
		t.Fatalf("Publish: %v", err)
	}
	if stub.gotTopic != "kit.runtime.entity.created" || stub.gotSource != "test" || stub.gotPayload != 42 {
		t.Fatalf("delegation mismatch: %+v", stub)
	}
}

func TestRealBus_Publish_NilPublisher(t *testing.T) {
	t.Parallel()
	b := real.Bus{}
	err := b.Publish(context.Background(), "k.r.e.created", "test", nil)
	if !errors.Is(err, real.ErrNilPublisher) {
		t.Fatalf("want ErrNilPublisher, got %v", err)
	}
}

func TestRealExec_RunOutput(t *testing.T) {
	t.Parallel()
	e := real.Exec{}
	out, err := e.Output(exec.Command("echo", "hello"))
	if err != nil {
		t.Fatalf("Output: %v", err)
	}
	if string(out) != "hello\n" {
		t.Fatalf("got %q want %q", out, "hello\n")
	}
	if err := e.Run(exec.Command("true")); err != nil {
		t.Fatalf("Run true: %v", err)
	}
}
