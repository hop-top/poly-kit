package svc

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	confsvc "hop.top/kit/go/conformance/svc"
)

// storyBytes is the shared story fixture; its hash is pinned both in
// the scenario (server side) and the cassette manifest (upload side).
var storyBytes = []byte("story: hello\n")

func storyHash() string {
	sum := sha256.Sum256(storyBytes)
	return "sha256:" + hex.EncodeToString(sum[:])
}

// writeScenarioFixture lays out <root>/scenarios/acme/widget/v1 with a
// valid scenario that asserts step-1 exits 0.
func writeScenarioFixture(t *testing.T, root string) {
	t.Helper()
	dir := filepath.Join(root, "scenarios", "acme", "widget", "v1")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	scYAML := fmt.Sprintf(`schema_version: "1"
scenario_id: widget
binary: example
factor_coverage: [11]
tier: 3
story_ref:
  story_id: example.story
  story_path: stories/example.yaml
  content_hash: %q
steps:
  - id: step-1
    invoke: ["example", "run"]
assertions:
  - id: exits-zero
    kind: exit_code_equals
    on: step-1
    factor: 11
    value: 0
`, storyHash())
	if err := os.WriteFile(filepath.Join(dir, "scenario.yaml"), []byte(scYAML), 0o644); err != nil {
		t.Fatal(err)
	}
}

// buildCassetteTarGz packs a minimal svc-schema cassette whose single
// step recorded the given exit code.
func buildCassetteTarGz(t *testing.T, exitCode int) []byte {
	t.Helper()
	manifest := fmt.Sprintf(`schema_version: "1"
binary: example
recorder: xrr
recorder_version: 0.1.0
recorded_at: 2026-08-08T12:00:00Z
scenario_id: acme/widget
scenario_version: v1
story_ref:
  story_id: example.story
  content_hash: %q
steps:
  - id: step-1
    cassette_dir: steps/step-1/cassette
    captures: steps/step-1
`, storyHash())

	var gzBuf bytes.Buffer
	gw := gzip.NewWriter(&gzBuf)
	tw := tar.NewWriter(gw)
	write := func(name string, body []byte) {
		hdr := &tar.Header{Name: name, Mode: 0o644, Size: int64(len(body)), ModTime: time.Unix(0, 0)}
		if err := tw.WriteHeader(hdr); err != nil {
			t.Fatalf("tar header %q: %v", name, err)
		}
		if _, err := tw.Write(body); err != nil {
			t.Fatalf("tar body %q: %v", name, err)
		}
	}
	write("manifest.yaml", []byte(manifest))
	write("story.yaml", storyBytes)
	write("steps/step-1/result.json",
		fmt.Appendf(nil, `{"exit_code":%d,"duration_ms":5}`, exitCode))
	write("steps/step-1/stdout.txt", []byte("{}\n"))
	if err := tw.Close(); err != nil {
		t.Fatal(err)
	}
	if err := gw.Close(); err != nil {
		t.Fatal(err)
	}
	return gzBuf.Bytes()
}

// syncBuffer is a mutex-guarded bytes.Buffer safe for cross-goroutine
// write (serve) + read (test poll).
type syncBuffer struct {
	mu  sync.Mutex
	buf bytes.Buffer
}

func (b *syncBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.Write(p)
}

func (b *syncBuffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.String()
}

// startServe boots "kit conformance svc serve" on an ephemeral port
// and returns the base URL, the parsed startup line, and a bearer
// token with grade:acme scope.
func startServe(t *testing.T) (baseURL string, startup map[string]any, token string) {
	t.Helper()
	root := t.TempDir()
	writeScenarioFixture(t, root)
	claimsDB := filepath.Join(t.TempDir(), "claims.sqlite")

	claims, err := confsvc.OpenSQLClaimStore(claimsDB)
	if err != nil {
		t.Fatalf("OpenSQLClaimStore: %v", err)
	}
	_, token, err = claims.Mint(context.Background(), confsvc.MintInput{
		Tenant:  "acme",
		Scopes:  []string{"grade:acme", "meta:acme"},
		TierMax: 3,
	})
	if err != nil {
		t.Fatalf("Mint: %v", err)
	}
	if err := claims.Close(); err != nil {
		t.Fatalf("close claim store: %v", err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	out := &syncBuffer{}
	cmd := serveCmd()
	cmd.SetOut(out)
	cmd.SetErr(out)
	cmd.SetContext(ctx)
	cmd.SetArgs([]string{
		"--port", "0",
		"--addr", "127.0.0.1",
		"--scenarios-root", root,
		"--claims-db", claimsDB,
	})
	done := make(chan error, 1)
	go func() { done <- cmd.Execute() }()
	t.Cleanup(func() {
		cancel()
		select {
		case err := <-done:
			if err != nil {
				t.Errorf("serve exited with error: %v", err)
			}
		case <-time.After(5 * time.Second):
			t.Errorf("serve did not shut down within 5s")
		}
	})

	deadline := time.Now().Add(5 * time.Second)
	for {
		if line := out.String(); strings.Contains(line, "\n") {
			first := strings.SplitN(line, "\n", 2)[0]
			if err := json.Unmarshal([]byte(first), &startup); err != nil {
				t.Fatalf("startup line not JSON: %q: %v", first, err)
			}
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("serve never printed a startup line; output: %q", out.String())
		}
		time.Sleep(10 * time.Millisecond)
	}
	port, ok := startup["port"].(float64)
	if !ok || port <= 0 {
		t.Fatalf("startup line missing port: %v", startup)
	}
	return fmt.Sprintf("http://127.0.0.1:%d", int(port)), startup, token
}

// postGrade uploads the cassette and returns the decoded result body.
func postGrade(t *testing.T, baseURL, token string, cassette []byte, tier string) (int, map[string]any) {
	t.Helper()
	req, err := http.NewRequest(http.MethodPost, baseURL+"/v1/grade", bytes.NewReader(cassette))
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Content-Type", "application/vnd.kit.cassette+tar+gzip")
	req.Header.Set("X-Kit-Scenario-Ref", "acme/widget@v1")
	if tier != "" {
		req.Header.Set("X-Kit-Tier", tier)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("POST /v1/grade: %v", err)
	}
	defer resp.Body.Close()
	var body map[string]any
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		t.Fatalf("decode grade response: %v", err)
	}
	return resp.StatusCode, body
}

func resultOf(t *testing.T, body map[string]any) map[string]any {
	t.Helper()
	res, ok := body["result"].(map[string]any)
	if !ok {
		t.Fatalf("response has no result object: %v", body)
	}
	return res
}

// TestServe_FailingCassetteDoesNotPass is the hardcoded-pass
// regression gate: a cassette that violates the scenario's assertions
// must never grade "pass". Under a fabricated grader this fails.
func TestServe_FailingCassetteDoesNotPass(t *testing.T) {
	baseURL, _, token := startServe(t)

	status, body := postGrade(t, baseURL, token, buildCassetteTarGz(t, 3), "3")
	if status != http.StatusOK {
		t.Fatalf("grade status: got %d, body %v", status, body)
	}
	res := resultOf(t, body)
	if res["verdict"] == "pass" {
		t.Fatalf("failing cassette graded pass — grader is fabricating verdicts: %v", res)
	}
	if res["verdict"] != "fail" {
		t.Errorf("verdict: got %v, want fail", res["verdict"])
	}
	asserts, _ := res["assertions"].([]any)
	if len(asserts) == 0 {
		t.Errorf("tier-3 result missing assertion trace: %v", res)
	}
}

// TestServe_PassingCassetteGradedByRealGrader asserts the happy path
// flows through the scenario library (real grader version, real
// per-assertion trace), not a stub.
func TestServe_PassingCassetteGradedByRealGrader(t *testing.T) {
	baseURL, startup, token := startServe(t)

	if got, _ := startup["scenarios_loaded"].(float64); int(got) != 1 {
		t.Errorf("scenarios_loaded: got %v, want 1", startup["scenarios_loaded"])
	}

	status, body := postGrade(t, baseURL, token, buildCassetteTarGz(t, 0), "3")
	if status != http.StatusOK {
		t.Fatalf("grade status: got %d, body %v", status, body)
	}
	res := resultOf(t, body)
	if res["verdict"] != "pass" {
		t.Fatalf("verdict: got %v, want pass; result %v", res["verdict"], res)
	}
	gv, _ := res["grader_version"].(string)
	if gv == "" || strings.Contains(gv, "stub") {
		t.Errorf("grader_version %q looks fabricated", gv)
	}
	if reason, _ := res["reason"].(string); strings.Contains(reason, "stub") {
		t.Errorf("reason %q mentions stub grader", reason)
	}
	asserts, _ := res["assertions"].([]any)
	if len(asserts) != 1 {
		t.Fatalf("want 1 assertion in tier-3 trace, got %v", res["assertions"])
	}
	a0, _ := asserts[0].(map[string]any)
	if a0["status"] != "pass" || a0["kind"] != "exit_code_equals" {
		t.Errorf("assertion trace not from real evaluator: %v", a0)
	}
}
