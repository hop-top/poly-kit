// Tests for the managed-block refresh orchestrator.
//
// These tests drive RunManaged directly rather than via the cobra
// command — that keeps the surface tight and avoids reaching into
// the full detect/Gather flow which has its own coverage. Each test
// builds a throwaway project in t.TempDir(), invokes RunManaged with
// a constructed ManagedOptions, and asserts on filesystem state +
// stdout/stderr.
//
// All tests require `bash` on PATH (the orchestrator shells out to
// the embedded emitter scripts). CI runners always have bash; we
// skip cleanly when it isn't found rather than failing.
package kitinit

import (
	"bytes"
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func requireBash(t *testing.T) {
	t.Helper()
	if _, err := exec.LookPath("bash"); err != nil {
		t.Skip("bash not on PATH; managed-block tests require bash")
	}
}

// TestRunManaged_UpdateGoOnly verifies that running --update in a
// minimal Go project (only go.mod present) emits a mise.toml whose
// managed block pins `go` but omits node/python/rust.
func TestRunManaged_UpdateGoOnly(t *testing.T) {
	requireBash(t)
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "go.mod"), []byte("module test\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	var stdout, stderr bytes.Buffer
	err := RunManaged(context.Background(), ManagedOptions{
		Cwd:    dir,
		Name:   "test",
		Stdout: &stdout,
		Stderr: &stderr,
	})
	if err != nil {
		t.Fatalf("RunManaged: %v\nstderr: %s", err, stderr.String())
	}

	mise, err := os.ReadFile(filepath.Join(dir, "mise.toml"))
	if err != nil {
		t.Fatalf("mise.toml not written: %v", err)
	}
	s := string(mise)
	if !strings.Contains(s, "go ") && !strings.Contains(s, "go=") {
		t.Errorf("mise.toml missing `go` pin:\n%s", s)
	}
	for _, banned := range []string{"node ", "node=", "python ", "python=", "rust ", "rust="} {
		if strings.Contains(s, banned) {
			t.Errorf("mise.toml unexpectedly contains %q:\n%s", banned, s)
		}
	}

	// Other managed files present too.
	for _, rel := range []string{".devcontainer/devcontainer.json", ".devcontainer/docker-compose.yml", ".env.example"} {
		if _, err := os.Stat(filepath.Join(dir, rel)); err != nil {
			t.Errorf("expected %s to be emitted: %v", rel, err)
		}
	}
}

// TestRunManaged_Idempotent verifies that running --update twice
// against the same dir produces byte-identical output (the central
// invariant of the managed-block pattern).
func TestRunManaged_Idempotent(t *testing.T) {
	requireBash(t)
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "go.mod"), []byte("module test\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	for i := 0; i < 2; i++ {
		var stdout, stderr bytes.Buffer
		if err := RunManaged(context.Background(), ManagedOptions{
			Cwd:    dir,
			Name:   "test",
			Stdout: &stdout,
			Stderr: &stderr,
		}); err != nil {
			t.Fatalf("run %d: %v\nstderr: %s", i, err, stderr.String())
		}
	}

	// Snapshot then run --check; expect no drift.
	var checkOut, checkErr bytes.Buffer
	err := RunManaged(context.Background(), ManagedOptions{
		Cwd:    dir,
		Name:   "test",
		Check:  true,
		Stdout: &checkOut,
		Stderr: &checkErr,
	})
	if err != nil {
		t.Fatalf("--check after idempotent update should pass: %v\nstdout: %s\nstderr: %s",
			err, checkOut.String(), checkErr.String())
	}
}

// TestRunManaged_CheckDetectsDrift verifies that after a clean
// --update, touching the manifest causes --check to surface drift
// and exit non-zero (via ErrManagedDrift).
func TestRunManaged_CheckDetectsDrift(t *testing.T) {
	requireBash(t)
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "go.mod"), []byte("module test\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	// First, a clean update.
	if err := RunManaged(context.Background(), ManagedOptions{Cwd: dir, Name: "test"}); err != nil {
		t.Fatalf("initial update: %v", err)
	}

	// Now corrupt one managed file inside its markers so the emitter
	// will produce different content on the next pass.
	misePath := filepath.Join(dir, "mise.toml")
	data, err := os.ReadFile(misePath)
	if err != nil {
		t.Fatal(err)
	}
	corrupted := strings.Replace(string(data), "[tools]", "[tools]\n# user-injected drift line", 1)
	if corrupted == string(data) {
		t.Fatalf("mise.toml lacks the expected [tools] marker; emitter output drifted:\n%s", data)
	}
	if err := os.WriteFile(misePath, []byte(corrupted), 0o644); err != nil {
		t.Fatal(err)
	}

	var stdout, stderr bytes.Buffer
	err = RunManaged(context.Background(), ManagedOptions{
		Cwd:    dir,
		Name:   "test",
		Check:  true,
		Stdout: &stdout,
		Stderr: &stderr,
	})
	if !errors.Is(err, ErrManagedDrift) {
		t.Fatalf("expected ErrManagedDrift after manifest drift; got %v\nstdout: %s\nstderr: %s",
			err, stdout.String(), stderr.String())
	}
	if !strings.Contains(stderr.String(), "drift detected") {
		t.Errorf("expected `drift detected` in stderr; got: %s", stderr.String())
	}
}

// TestRunManaged_PreservesUserContentAboveMarkers asserts that
// user-owned content sitting above the kit-managed block in
// mise.toml is preserved across --update. The bash emitter is the
// source of truth for this guarantee; this test exists to flag
// regressions in the Go orchestration (e.g., accidentally calling
// the emitter against a fresh file instead of the user's existing
// one).
func TestRunManaged_PreservesUserContentAboveMarkers(t *testing.T) {
	requireBash(t)
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "go.mod"), []byte("module test\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	// Seed mise.toml with user content above the markers, then run
	// the emitter — managed-block.sh should write its block below
	// the existing content rather than truncating.
	userPrefix := "# user-owned config\n[my_section]\nfoo = \"bar\"\n\n"
	misePath := filepath.Join(dir, "mise.toml")
	if err := os.WriteFile(misePath, []byte(userPrefix), 0o644); err != nil {
		t.Fatal(err)
	}

	if err := RunManaged(context.Background(), ManagedOptions{Cwd: dir, Name: "test"}); err != nil {
		t.Fatalf("RunManaged: %v", err)
	}

	got, err := os.ReadFile(misePath)
	if err != nil {
		t.Fatal(err)
	}
	s := string(got)
	if !strings.Contains(s, "# user-owned config") || !strings.Contains(s, "[my_section]") {
		t.Errorf("user content stripped from mise.toml:\n%s", s)
	}
	if !strings.Contains(s, "kit-managed") {
		t.Errorf("kit-managed markers missing from mise.toml:\n%s", s)
	}
}

// TestRunManaged_AddServiceWiresRedis verifies the happy-path
// integration with the embedded apply-services.sh: a project
// with a fresh managed scaffold gets the redis compose service
// added and KIT_QUEUE_DRIVER flipped to redis.
func TestRunManaged_AddServiceWiresRedis(t *testing.T) {
	requireBash(t)
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "go.mod"), []byte("module test\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	// Bootstrap managed blocks.
	if err := RunManaged(context.Background(), ManagedOptions{
		Cwd: dir, Langs: "go",
	}); err != nil {
		t.Fatalf("bootstrap: %v", err)
	}

	// Now add redis.
	if err := RunManaged(context.Background(), ManagedOptions{
		Cwd: dir, AddService: "redis",
	}); err != nil {
		t.Fatalf("add-service redis: %v", err)
	}

	compose, err := os.ReadFile(filepath.Join(dir, ".devcontainer/docker-compose.yml"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(compose), "redis:") {
		t.Errorf("expected redis service in compose:\n%s", compose)
	}

	envExample, err := os.ReadFile(filepath.Join(dir, ".env.example"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(envExample), "KIT_QUEUE_DRIVER=redis") {
		t.Errorf("expected KIT_QUEUE_DRIVER=redis in .env.example:\n%s", envExample)
	}
}

// TestRunManaged_RemoveServiceErrors locks the user-facing contract on
// --remove-service: it is intentionally unsupported and must surface a
// hint-rich error instead of silently corrupting the opted-in services
// block. A future implementer wiring real removal will trigger this
// test and remember to drop the early return.
func TestRunManaged_RemoveServiceErrors(t *testing.T) {
	requireBash(t)
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "go.mod"), []byte("module test\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	// Seed the managed blocks so RunManaged reaches the service-op leg
	// without tripping any earlier bootstrap requirement.
	if err := RunManaged(context.Background(), ManagedOptions{Cwd: dir, Langs: "go"}); err != nil {
		t.Fatalf("seed bootstrap: %v", err)
	}

	var stdout, stderr bytes.Buffer
	err := RunManaged(context.Background(), ManagedOptions{
		Cwd:           dir,
		RemoveService: "redis",
		Stdout:        &stdout,
		Stderr:        &stderr,
	})
	if err == nil {
		t.Fatalf("expected error from --remove-service, got nil\nstdout: %s\nstderr: %s",
			stdout.String(), stderr.String())
	}
	msg := err.Error()
	if !strings.Contains(msg, "remove-service") {
		t.Errorf("error should mention remove-service flag; got: %s", msg)
	}
	if !strings.Contains(msg, "not yet supported") {
		t.Errorf("error should mention `not yet supported` hint; got: %s", msg)
	}
}

// TestDetectLangs covers the lang autodetection rules.
func TestDetectLangs(t *testing.T) {
	cases := []struct {
		name  string
		files []string
		want  string
	}{
		{"empty", nil, ""},
		{"go only", []string{"go.mod"}, "go"},
		{"ts only", []string{"package.json"}, "ts"},
		{"py via pyproject", []string{"pyproject.toml"}, "py"},
		{"py via requirements", []string{"requirements.txt"}, "py"},
		{"rs only", []string{"Cargo.toml"}, "rs"},
		{"go+ts", []string{"go.mod", "package.json"}, "go,ts"},
		{"all four", []string{"go.mod", "package.json", "pyproject.toml", "Cargo.toml"}, "go,ts,py,rs"},
		{"py double", []string{"pyproject.toml", "requirements.txt"}, "py"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			for _, f := range tc.files {
				if err := os.WriteFile(filepath.Join(dir, f), []byte("x"), 0o644); err != nil {
					t.Fatal(err)
				}
			}
			got := DetectLangs(dir)
			if got != tc.want {
				t.Errorf("DetectLangs(%v) = %q, want %q", tc.files, got, tc.want)
			}
		})
	}
}
