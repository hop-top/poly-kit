package dryrun_test

import (
	"bytes"
	"context"
	"net/http"
	"net/http/httptest"
	"os/exec"
	"strings"
	"testing"

	"hop.top/kit/go/runtime/bus"
	"hop.top/kit/go/runtime/sideeffect"
	"hop.top/kit/go/runtime/sideeffect/dryrun"
)

// Compile-time interface conformance.
var (
	_ sideeffect.FS   = dryrun.FS{}
	_ sideeffect.HTTP = dryrun.HTTP{}
	_ sideeffect.Bus  = (*dryrun.Bus)(nil)
	_ sideeffect.Exec = dryrun.Exec{}
)

func TestDryrunFS_AllMethodsDescribeAndReturnNil(t *testing.T) {
	t.Parallel()
	var buf bytes.Buffer
	fs := dryrun.NewFS(dryrun.WithWriter(&buf))

	if err := fs.WriteFile("/etc/foo", []byte("data"), 0o644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	if err := fs.MkdirAll("/etc/foo.d", 0o755); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}
	if err := fs.Rename("/a", "/b"); err != nil {
		t.Fatalf("Rename: %v", err)
	}
	if err := fs.Remove("/c"); err != nil {
		t.Fatalf("Remove: %v", err)
	}

	out := buf.String()
	for _, want := range []string{
		"[dry-run] would write /etc/foo (4 bytes, mode 0644)",
		"[dry-run] would mkdir -p /etc/foo.d (mode 0755)",
		"[dry-run] would rename /a -> /b",
		"[dry-run] would remove /c",
	} {
		if !strings.Contains(out, want) {
			t.Fatalf("missing line %q in:\n%s", want, out)
		}
	}
}

func TestDryrunHTTP_GetPassesThrough(t *testing.T) {
	t.Parallel()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("real"))
	}))
	t.Cleanup(srv.Close)

	var buf bytes.Buffer
	h := dryrun.NewHTTP(http.DefaultClient, dryrun.WithWriter(&buf))
	req, _ := http.NewRequest(http.MethodGet, srv.URL, http.NoBody)
	resp, err := h.Do(req)
	if err != nil {
		t.Fatalf("Do GET: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status: got %d want 200", resp.StatusCode)
	}
	if buf.Len() != 0 {
		t.Fatalf("safe verbs must not log: %q", buf.String())
	}
}

func TestDryrunHTTP_PostIntercepted(t *testing.T) {
	t.Parallel()
	hit := false
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		hit = true
		w.WriteHeader(http.StatusOK)
	}))
	t.Cleanup(srv.Close)

	var buf bytes.Buffer
	h := dryrun.NewHTTP(http.DefaultClient, dryrun.WithWriter(&buf))
	req, _ := http.NewRequest(http.MethodPost, srv.URL, strings.NewReader("payload"))
	req.ContentLength = 7
	resp, err := h.Do(req)
	if err != nil {
		t.Fatalf("Do POST: %v", err)
	}
	defer resp.Body.Close()
	if hit {
		t.Fatalf("real server should not have been hit")
	}
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("status: got %d want 201", resp.StatusCode)
	}
	if !strings.Contains(buf.String(), "[dry-run] would POST") {
		t.Fatalf("missing POST log line in:\n%s", buf.String())
	}
	if !strings.Contains(buf.String(), "(7-byte body)") {
		t.Fatalf("missing body length in:\n%s", buf.String())
	}
}

type qualifiedPayload struct {
	bus.Qualifiers
	ID string
}

type plainPayload struct {
	ID string
}

func TestDryrunBus_AugmentsQualifiersWhenEmbedded(t *testing.T) {
	t.Parallel()
	var buf bytes.Buffer
	b := dryrun.NewBus(dryrun.WithWriter(&buf))
	p := &qualifiedPayload{ID: "x"}
	if err := b.Publish(context.Background(), "kit.runtime.entity.created", "test", p); err != nil {
		t.Fatalf("Publish: %v", err)
	}
	if p.Mechanism != "dry_run" {
		t.Fatalf("Mechanism not augmented; got %q", p.Mechanism)
	}
	if !strings.Contains(buf.String(), "mechanism=dry_run") {
		t.Fatalf("output missing mechanism marker:\n%s", buf.String())
	}
}

func TestDryrunBus_PreservesExistingMechanism(t *testing.T) {
	t.Parallel()
	var buf bytes.Buffer
	b := dryrun.NewBus(dryrun.WithWriter(&buf))
	p := &qualifiedPayload{Qualifiers: bus.Qualifiers{Mechanism: "signal"}, ID: "x"}
	if err := b.Publish(context.Background(), "kit.runtime.entity.created", "test", p); err != nil {
		t.Fatalf("Publish: %v", err)
	}
	if p.Mechanism != "signal" {
		t.Fatalf("existing Mechanism overwritten; got %q", p.Mechanism)
	}
}

func TestDryrunBus_NoQualifiersEmbed(t *testing.T) {
	t.Parallel()
	var buf bytes.Buffer
	b := dryrun.NewBus(dryrun.WithWriter(&buf))
	if err := b.Publish(context.Background(), "kit.runtime.entity.created", "test", &plainPayload{}); err != nil {
		t.Fatalf("Publish: %v", err)
	}
	if !strings.Contains(buf.String(), "would publish") {
		t.Fatalf("missing publish log:\n%s", buf.String())
	}
	if !strings.Contains(buf.String(), "does not embed bus.Qualifiers") {
		t.Fatalf("missing embed-warning notice:\n%s", buf.String())
	}
}

func TestDryrunExec_DescribesAndReturnsZero(t *testing.T) {
	t.Parallel()
	var buf bytes.Buffer
	e := dryrun.NewExec(dryrun.WithWriter(&buf))

	if err := e.Run(exec.Command("/bin/this-does-not-exist", "--flag", "with space")); err != nil {
		t.Fatalf("Run: %v", err)
	}
	out, err := e.Output(exec.Command("rm", "-rf", "/"))
	if err != nil {
		t.Fatalf("Output: %v", err)
	}
	if len(out) != 0 {
		t.Fatalf("Output bytes should be empty; got %d", len(out))
	}
	s := buf.String()
	if !strings.Contains(s, `would exec: /bin/this-does-not-exist --flag "with space"`) {
		t.Fatalf("missing Run line:\n%s", s)
	}
	if !strings.Contains(s, "would exec (capture): rm -rf /") {
		t.Fatalf("missing Output line:\n%s", s)
	}
}
