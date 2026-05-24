// Package kitinit — managed.go orchestrates the kit-managed-block
// emitters under `templates/shared/` so existing projects can
// idempotently refresh their mise.toml, .devcontainer/devcontainer.json,
// .devcontainer/docker-compose.yml, and .env.example.
//
// The bash emitters are embedded into the kit binary via go:embed and
// extracted to a tempdir on demand. This makes `kit init --update`
// work in any cwd without requiring poly-kit to be checked out next to
// the project — the binary ships its own copy.
//
// Subcommand surface added to `kit init` (see init.go for wiring):
//
//	--update            Non-interactive refresh of all managed blocks
//	--check             Drift detector; exits non-zero with a diff
//	--add-service NAME  Append a curated service to docker-compose
//	--remove-service N  Inverse of --add-service
//
// Track: scaffold-emits-mise-toml-devcontainer-compose (spec §4).
//
// IMPORTANT: managed_assets/ is a copy of templates/shared/*.sh and
// templates/shared/tool-versions.toml. Keep it in sync via `make
// sync-managed-assets` (see Makefile) or by re-running the copy.
package kitinit

import (
	"bytes"
	"context"
	"embed"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
)

//go:embed managed_assets/managed-block.sh
//go:embed managed_assets/emit-mise.sh
//go:embed managed_assets/emit-devcontainer-json.sh
//go:embed managed_assets/emit-docker-compose.sh
//go:embed managed_assets/emit-env-example.sh
//go:embed managed_assets/tool-versions.toml
var managedAssets embed.FS

// ManagedFiles lists the project-relative paths the emitters write.
// Used by --check to snapshot before/after content and emit diffs.
var ManagedFiles = []string{
	"mise.toml",
	".devcontainer/devcontainer.json",
	".devcontainer/docker-compose.yml",
	".devcontainer/otel-config.yaml",
	".env.example",
}

// ManagedOptions configures a managed-block refresh run.
type ManagedOptions struct {
	// Cwd is the project root the emitters operate on.
	Cwd string
	// Name is the project name (used by devcontainer + docker-compose
	// + .env emitters). Falls back to filepath.Base(Cwd) when empty.
	Name string
	// Langs is a comma-separated lang subset of "go,ts,py,rs". When
	// empty, langs are auto-detected from cwd (go.mod, package.json,
	// pyproject.toml/requirements.txt, Cargo.toml).
	Langs string
	// Check, when true, runs the emitters in a sandbox copy of Cwd
	// and reports drift to Stdout; the real project is untouched.
	Check bool
	// AddService / RemoveService are service catalog operations
	// handled by the T-0808 apply-services.sh helper. If the helper
	// is not embedded (because T-0808 hasn't merged), these return
	// a descriptive error.
	AddService    string
	RemoveService string
	// Stdout / Stderr receive emitter output and diff content.
	Stdout io.Writer
	Stderr io.Writer
}

// RunManaged executes the requested managed-block refresh.
//
// Flow:
//  1. Extract embedded bash scripts to a tempdir.
//  2. Resolve project name and lang list (auto-detect if unset).
//  3. If Check: snapshot ManagedFiles in Cwd, run emitters into a
//     sandbox dir, diff sandbox-vs-cwd, exit non-zero on drift.
//  4. Otherwise: run emitters in-place against Cwd.
//  5. If AddService/RemoveService: delegate to apply-services.sh
//     (gracefully error when the helper is not embedded).
//
// Returns ErrManagedDrift when Check finds drift; callers should map
// it to a non-zero exit. All other errors are wrapped with context.
func RunManaged(ctx context.Context, opts ManagedOptions) error {
	if opts.Stdout == nil {
		opts.Stdout = os.Stdout
	}
	if opts.Stderr == nil {
		opts.Stderr = os.Stderr
	}
	if opts.Cwd == "" {
		return errors.New("kit init: managed: Cwd is required")
	}
	if _, err := exec.LookPath("bash"); err != nil {
		return fmt.Errorf("kit init: managed: bash not on PATH: %w", err)
	}

	// 1. Extract embedded assets.
	scriptDir, cleanup, err := extractManagedAssets()
	if err != nil {
		return fmt.Errorf("kit init: extract embedded scripts: %w", err)
	}
	defer cleanup()

	// 2. Resolve name + langs.
	name := opts.Name
	if name == "" {
		name = filepath.Base(opts.Cwd)
	}
	langs := opts.Langs
	if langs == "" {
		langs = DetectLangs(opts.Cwd)
	}

	// Service ops short-circuit emitter flow.
	if opts.AddService != "" || opts.RemoveService != "" {
		return runServiceOp(scriptDir, opts)
	}

	// 3/4. Run emitters either in-place or in a sandbox copy.
	target := opts.Cwd
	var snapshot map[string][]byte
	if opts.Check {
		snapshot = snapshotManagedFiles(opts.Cwd)
		sandbox, err := os.MkdirTemp("", "kit-init-check-*")
		if err != nil {
			return fmt.Errorf("kit init: sandbox: %w", err)
		}
		defer func() { _ = os.RemoveAll(sandbox) }()
		// Seed the sandbox with the current managed-file content so
		// the emitters see existing markers and refresh in place
		// rather than recreating the scaffold from scratch.
		if err := seedSandbox(sandbox, opts.Cwd); err != nil {
			return fmt.Errorf("kit init: seed sandbox: %w", err)
		}
		target = sandbox
	}

	if err := runEmitters(ctx, scriptDir, target, name, langs, opts); err != nil {
		return err
	}

	// 5. Drift detection.
	if opts.Check {
		drifted := []string{}
		var diffs bytes.Buffer
		for _, rel := range ManagedFiles {
			before := snapshot[rel]
			after, _ := os.ReadFile(filepath.Join(target, rel))
			if !bytes.Equal(before, after) {
				drifted = append(drifted, rel)
				writeUnifiedDiff(&diffs, rel, before, after)
			}
		}
		if len(drifted) > 0 {
			fmt.Fprintln(opts.Stderr, "kit init --check: drift detected in:")
			for _, p := range drifted {
				fmt.Fprintln(opts.Stderr, "  -", p)
			}
			fmt.Fprintln(opts.Stdout)
			_, _ = diffs.WriteTo(opts.Stdout)
			return ErrManagedDrift
		}
		fmt.Fprintln(opts.Stdout, "kit init --check: no drift.")
		return nil
	}

	// Non-check happy path: print a one-line summary.
	updated := []string{}
	for _, rel := range ManagedFiles {
		if _, err := os.Stat(filepath.Join(opts.Cwd, rel)); err == nil {
			updated = append(updated, rel)
		}
	}
	fmt.Fprintf(opts.Stdout, "kit init --update: refreshed %d managed file(s):\n", len(updated))
	for _, p := range updated {
		fmt.Fprintln(opts.Stdout, "  -", p)
	}
	return nil
}

// ErrManagedDrift signals that `kit init --check` found drift between
// the emitted output and the on-disk managed files. Callers map this
// to a non-zero exit so CI / pre-merge gates can detect un-refreshed
// projects.
var ErrManagedDrift = errors.New("kit init --check: managed-block drift detected")

// DetectLangs inspects cwd for ecosystem manifests and returns a
// comma-separated subset of "go,ts,py,rs". Empty when no recognized
// manifests are present. Detection rules:
//
//	go.mod                                 → go
//	package.json                           → ts
//	pyproject.toml OR requirements.txt     → py
//	Cargo.toml                             → rs
func DetectLangs(cwd string) string {
	checks := []struct {
		file string
		lang string
	}{
		{"go.mod", "go"},
		{"package.json", "ts"},
		{"pyproject.toml", "py"},
		{"requirements.txt", "py"},
		{"Cargo.toml", "rs"},
	}
	seen := map[string]bool{}
	out := []string{}
	for _, c := range checks {
		if seen[c.lang] {
			continue
		}
		if _, err := os.Stat(filepath.Join(cwd, c.file)); err == nil {
			seen[c.lang] = true
			out = append(out, c.lang)
		}
	}
	return strings.Join(out, ",")
}

// extractManagedAssets copies the embedded managed_assets/*.sh and
// tool-versions.toml into a fresh tempdir flattened to its basenames
// (so the emitters can `source ./managed-block.sh` without path
// gymnastics) and returns the dir + a cleanup func.
func extractManagedAssets() (string, func(), error) {
	dir, err := os.MkdirTemp("", "kit-init-assets-*")
	if err != nil {
		return "", func() {}, err
	}
	cleanup := func() { _ = os.RemoveAll(dir) }
	entries, err := fs.ReadDir(managedAssets, "managed_assets")
	if err != nil {
		cleanup()
		return "", func() {}, err
	}
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		data, err := managedAssets.ReadFile("managed_assets/" + e.Name())
		if err != nil {
			cleanup()
			return "", func() {}, err
		}
		mode := os.FileMode(0o644)
		if strings.HasSuffix(e.Name(), ".sh") {
			mode = 0o755
		}
		if err := os.WriteFile(filepath.Join(dir, e.Name()), data, mode); err != nil {
			cleanup()
			return "", func() {}, err
		}
	}
	return dir, cleanup, nil
}

// runEmitters sources each emitter script and invokes its public
// function in a single bash subshell. Errors from any emitter abort
// the run with full bash stderr in the wrapped error.
func runEmitters(ctx context.Context, scriptDir, projectDir, name, langs string, opts ManagedOptions) error {
	// Build a tiny driver that sources the emitters and calls their
	// public functions in order. The driver lives next to the emitters
	// so its KIT_TEMPLATES_DIR/manifest paths resolve identically to
	// scaffold.sh's invocation pattern.
	driver := fmt.Sprintf(`#!/usr/bin/env bash
set -euo pipefail
cd %q
SCRIPT_DIR=%q
# The emitters source managed-block.sh from their sibling location;
# they also read tool-versions.toml from $SCRIPT_DIR by default.
KIT_TOOL_VERSIONS_TOML="${SCRIPT_DIR}/tool-versions.toml"
export KIT_TOOL_VERSIONS_TOML
# shellcheck source=/dev/null
source "${SCRIPT_DIR}/managed-block.sh"
# shellcheck source=/dev/null
source "${SCRIPT_DIR}/emit-mise.sh"
# shellcheck source=/dev/null
source "${SCRIPT_DIR}/emit-devcontainer-json.sh"
# shellcheck source=/dev/null
source "${SCRIPT_DIR}/emit-docker-compose.sh"
# shellcheck source=/dev/null
source "${SCRIPT_DIR}/emit-env-example.sh"

NAME=%q
LANGS=%q
PROJECT=%q

if [ -n "${LANGS}" ]; then
  emit_mise           "${PROJECT}" "${LANGS}"
  emit_devcontainer_json "${PROJECT}" "${NAME}" "${LANGS}"
else
  # No lang manifests detected: still emit the devcontainer + compose
  # + env scaffolding so the project gets telemetry; skip mise.toml
  # since there are no runtimes to pin.
  emit_devcontainer_json "${PROJECT}" "${NAME}" ""
fi
emit_docker_compose "${PROJECT}" "${NAME}"
emit_env_example    "${PROJECT}" "${NAME}"
`, projectDir, scriptDir, name, langs, projectDir)

	driverPath := filepath.Join(scriptDir, "driver.sh")
	if err := os.WriteFile(driverPath, []byte(driver), 0o755); err != nil {
		return fmt.Errorf("write driver: %w", err)
	}

	cmd := exec.CommandContext(ctx, "bash", driverPath)
	var outBuf, errBuf bytes.Buffer
	cmd.Stdout = &outBuf
	cmd.Stderr = &errBuf
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("kit init: emitter run failed: %w\nstdout:\n%s\nstderr:\n%s",
			err, outBuf.String(), errBuf.String())
	}
	// Surface emitter stdout (warnings, info lines) but suppress in
	// --check mode so the diff is the only thing on stdout.
	if !opts.Check {
		_, _ = io.Copy(opts.Stdout, &outBuf)
		_, _ = io.Copy(opts.Stderr, &errBuf)
	}
	return nil
}

// runServiceOp invokes apply-services.sh when present in the
// extracted asset bundle. Returns a descriptive error when the
// helper is not embedded (T-0808 hasn't merged into the worktree).
func runServiceOp(scriptDir string, opts ManagedOptions) error {
	applier := filepath.Join(scriptDir, "apply-services.sh")
	if _, err := os.Stat(applier); errors.Is(err, fs.ErrNotExist) {
		op := "--add-service"
		if opts.RemoveService != "" {
			op = "--remove-service"
		}
		return fmt.Errorf(
			"%s requires T-0808's apply-services.sh; "+
				"rebase onto a branch with T-0808 merged", op)
	}
	// Reserved for the T-0808 wire-up: source the applier and call
	// its public function. The shape is documented in the T-0808
	// spec; if it diverges, this branch needs a refresh.
	verb, name := "apply_service", opts.AddService
	if opts.RemoveService != "" {
		verb, name = "remove_service", opts.RemoveService
	}
	driver := fmt.Sprintf(`#!/usr/bin/env bash
set -euo pipefail
SCRIPT_DIR=%q
PROJECT=%q
# shellcheck source=/dev/null
source "${SCRIPT_DIR}/managed-block.sh"
# shellcheck source=/dev/null
source "${SCRIPT_DIR}/apply-services.sh"
%s "${PROJECT}" %q
`, scriptDir, opts.Cwd, verb, name)
	driverPath := filepath.Join(scriptDir, "services-driver.sh")
	if err := os.WriteFile(driverPath, []byte(driver), 0o755); err != nil {
		return fmt.Errorf("write services driver: %w", err)
	}
	cmd := exec.Command("bash", driverPath)
	cmd.Stdout = opts.Stdout
	cmd.Stderr = opts.Stderr
	return cmd.Run()
}

// snapshotManagedFiles returns the byte content of each managed file
// keyed by its project-relative path. Missing files map to nil.
func snapshotManagedFiles(cwd string) map[string][]byte {
	out := make(map[string][]byte, len(ManagedFiles))
	for _, rel := range ManagedFiles {
		data, err := os.ReadFile(filepath.Join(cwd, rel))
		if err != nil {
			out[rel] = nil
			continue
		}
		out[rel] = data
	}
	return out
}

// seedSandbox copies the current managed files (and any user-owned
// content above markers in the same files) into the sandbox so the
// emitters refresh in place. Missing files are simply absent in the
// sandbox — the emitters will scaffold them from zero, which is the
// same behavior as running --update in a fresh project.
func seedSandbox(sandbox, cwd string) error {
	for _, rel := range ManagedFiles {
		src := filepath.Join(cwd, rel)
		data, err := os.ReadFile(src)
		if err != nil {
			continue
		}
		dst := filepath.Join(sandbox, rel)
		if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
			return err
		}
		if err := os.WriteFile(dst, data, 0o644); err != nil {
			return err
		}
	}
	return nil
}

// writeUnifiedDiff emits a minimal unified-diff-like header + the two
// file contents inline. We avoid shelling out to `diff` (not always
// available on Windows CI runners) and avoid a heavyweight diff lib;
// the goal is human-readable drift, not a strict unified-diff parser.
func writeUnifiedDiff(w io.Writer, path string, before, after []byte) {
	fmt.Fprintf(w, "--- a/%s\n", path)
	fmt.Fprintf(w, "+++ b/%s\n", path)
	beforeLines := splitLines(before)
	afterLines := splitLines(after)
	// Cheap line-by-line diff: walk both slices, mark mismatches.
	// Doesn't compute an LCS; that's deliberate — drift reports are
	// for CI gates, and a small false-positive on insert ordering is
	// acceptable. If we ever need a true unified diff we can swap in
	// github.com/pmezard/go-difflib (transitive dep already).
	max := len(beforeLines)
	if len(afterLines) > max {
		max = len(afterLines)
	}
	for i := 0; i < max; i++ {
		var b, a string
		if i < len(beforeLines) {
			b = beforeLines[i]
		}
		if i < len(afterLines) {
			a = afterLines[i]
		}
		if b == a {
			fmt.Fprintln(w, " ", b)
			continue
		}
		if i < len(beforeLines) {
			fmt.Fprintln(w, "-", b)
		}
		if i < len(afterLines) {
			fmt.Fprintln(w, "+", a)
		}
	}
	fmt.Fprintln(w)
}

func splitLines(b []byte) []string {
	if len(b) == 0 {
		return nil
	}
	s := string(b)
	lines := strings.Split(s, "\n")
	// Drop the trailing empty element produced by a final newline so
	// the diff doesn't render a phantom blank line at the end.
	if len(lines) > 0 && lines[len(lines)-1] == "" {
		lines = lines[:len(lines)-1]
	}
	return lines
}

// SortedManagedFiles returns ManagedFiles sorted lexically — useful
// when callers want deterministic iteration without mutating the
// package-level slice.
func SortedManagedFiles() []string {
	out := append([]string(nil), ManagedFiles...)
	sort.Strings(out)
	return out
}
