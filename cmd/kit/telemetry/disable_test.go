// disable_test.go covers `kit telemetry disable` at the runDisable
// seam. Mirrors enable_test.go in structure; the contrast cases assert
// that disable does NOT refuse on env kill switches (a denied write on
// top of DO_NOT_TRACK=1 is the desired posture).

package telemetry

import (
	"bytes"
	"context"
	"os"
	"strings"
	"testing"

	"hop.top/kit/go/core/consent"
)

// writeSeedFile creates the parent directory (perms 0700, matching the
// FileStore dirPerm) and writes the seed bytes at 0600. Used by tests
// that need to pre-populate the consent YAML before the command runs.
func writeSeedFile(path, body string) error {
	if err := os.MkdirAll(stripBase(path), 0o700); err != nil {
		return err
	}
	return os.WriteFile(path, []byte(body), 0o600)
}

// readFile is a thin os.ReadFile wrapper that returns the file body as
// a string for cheap substring assertions.
func readFile(path string) (string, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return "", err
	}
	return string(b), nil
}

// stripBase returns the directory of path (filepath.Dir without the
// import — kept tiny to avoid duplicating the helper across both test
// files).
func stripBase(path string) string {
	for i := len(path) - 1; i >= 0; i-- {
		if path[i] == '/' {
			return path[:i]
		}
	}
	return "."
}

func TestDisable_PersistsDeniedDecision(t *testing.T) {
	freshXDGForEnable(t)

	var stdout bytes.Buffer
	if err := runDisable(context.Background(), &stdout, false); err != nil {
		t.Fatalf("runDisable: %v", err)
	}

	d := readBackDecision(t)
	if d.State != consent.StateDenied {
		t.Errorf("State = %q, want %q", d.State, consent.StateDenied)
	}
	if d.DecisionSource != consent.SourceFlag {
		t.Errorf("DecisionSource = %q, want %q", d.DecisionSource, consent.SourceFlag)
	}
	if d.PromptVersion != PromptVersion {
		t.Errorf("PromptVersion = %d, want %d", d.PromptVersion, PromptVersion)
	}
	if d.DecidedAt.IsZero() {
		t.Errorf("DecidedAt is zero; expected a stamped time")
	}
	if !strings.Contains(stdout.String(), "Telemetry disabled") {
		t.Errorf("stdout missing confirmation: %q", stdout.String())
	}
}

// TestDisable_AlwaysAllowedEvenWithEnvBlocks documents the asymmetric
// posture vs enable: a denied state IS the env-blocked outcome, so
// disable does not refuse on DO_NOT_TRACK / *_TELEMETRY_MODE=off.
func TestDisable_AlwaysAllowedEvenWithEnvBlocks(t *testing.T) {
	freshXDGForEnable(t)
	t.Setenv("DO_NOT_TRACK", "1")

	var stdout bytes.Buffer
	if err := runDisable(context.Background(), &stdout, false); err != nil {
		t.Fatalf("runDisable with DO_NOT_TRACK=1: %v", err)
	}

	d := readBackDecision(t)
	if d.State != consent.StateDenied {
		t.Errorf("State = %q, want %q", d.State, consent.StateDenied)
	}
}

func TestDisable_DryRunDoesNotWrite(t *testing.T) {
	freshXDGForEnable(t)

	var stdout bytes.Buffer
	if err := runDisable(context.Background(), &stdout, true); err != nil {
		t.Fatalf("runDisable dry-run: %v", err)
	}

	d := readBackDecision(t)
	if d.State != consent.StateUnknown {
		t.Errorf("dry-run wrote to store: state = %q", d.State)
	}
	if !strings.Contains(stdout.String(), "dry-run") {
		t.Errorf("stdout missing dry-run marker: %q", stdout.String())
	}
}
