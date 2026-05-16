// Package kitinit — tlc.go wires `tlc init` as a best-effort post-scaffold
// step. Mirrors the github.Create pattern: missing binary on PATH skips
// silently (no error, no side effect); a runtime failure from tlc itself
// surfaces as an error to the caller.
//
// `tlc init` is idempotent on existing scopes, so callers can invoke it
// from both bootstrap (fresh project) and augment (existing project)
// flows without guarding for prior runs.
package kitinit

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

// runTLCInit runs `tlc init` in dir if `tlc` is on PATH. Best-effort:
// when the binary is missing the call returns nil so the surrounding
// flow continues without error. Runtime failures from tlc surface as
// wrapped errors, matching the github.Create pattern.
//
// Returns (skipped, err). skipped=true indicates the call was a no-op:
// either the binary was not found OR a .tlc directory already exists at
// dir (we treat re-init as a successful noop, matching the user-facing
// "idempotent on existing scopes" contract — `tlc init` itself errors
// out without --force, so we elide the call rather than passing --force
// blindly which would clobber the user's config).
func runTLCInit(ctx context.Context, dir string) (bool, error) {
	if _, err := exec.LookPath("tlc"); err != nil {
		return true, nil
	}
	// Existing scope → noop. Stat-only check; we don't read the file.
	if fi, err := os.Stat(filepath.Join(dir, ".tlc")); err == nil && fi.IsDir() {
		return true, nil
	}
	cmd := exec.CommandContext(ctx, "tlc", "init")
	cmd.Dir = dir
	if out, err := cmd.CombinedOutput(); err != nil {
		return false, fmt.Errorf("tlc init: %s: %w", strings.TrimSpace(string(out)), err)
	}
	return false, nil
}
