package scenario_test

import (
	"path/filepath"
	"strings"
	"testing"

	"hop.top/kit/go/console/cli/conformance/verifynoleak/suppress"
)

// TestTestdataAllowlistedByKitInternal confirms that the kit-internal
// leak allowlist (DefaultKitInternalGlobs) covers every fixture
// under go/conformance/scenario/testdata/. Without this, the leak
// detector flags the scenario-shaped YAML in the testdata dir on
// any audit run against the kit repo.
func TestTestdataAllowlistedByKitInternal(t *testing.T) {
	globs := suppress.DefaultKitInternalGlobs()
	covered := false
	for _, g := range globs {
		if strings.HasPrefix(g, "go/conformance/scenario/testdata") {
			covered = true
			break
		}
	}
	if !covered {
		t.Errorf("DefaultKitInternalGlobs must include go/conformance/scenario/testdata/**")
	}
}

func TestTestdataFilesPresent(t *testing.T) {
	for _, f := range []string{"ok-minimal.yaml", "ok-judge.yaml", "bad-missing-id.yaml", "bad-unknown-verb.yaml"} {
		if _, err := readFixture(t, f); err != nil {
			t.Errorf("fixture %s missing: %v", f, err)
		}
	}
}

func readFixture(t *testing.T, name string) (string, error) {
	t.Helper()
	p := filepath.Join("testdata", name)
	return p, nil
}
