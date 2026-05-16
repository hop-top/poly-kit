package rules_test

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/stretchr/testify/require"
)

// TestEmbeddedJSONInSyncWithContracts protects against drift between
// contracts/scenario-rules.json (the canonical wire-format file) and
// scenario_rules_embedded.json (the build-time vendored copy).
//
// On drift, the fix is documented in doc.go.
func TestEmbeddedJSONInSyncWithContracts(t *testing.T) {
	_, thisFile, _, ok := runtime.Caller(0)
	require.True(t, ok, "runtime.Caller failed; can't locate package dir")
	pkgDir := filepath.Dir(thisFile)
	embeddedPath := filepath.Join(pkgDir, "scenario_rules_embedded.json")
	canonicalPath := filepath.Join(pkgDir, "..", "..", "..", "..", "..", "..", "contracts", "scenario-rules.json")

	embedded, err := os.ReadFile(embeddedPath)
	require.NoError(t, err, "embedded copy missing — see rules/doc.go")
	canonical, err := os.ReadFile(canonicalPath)
	if err != nil {
		// Running from outside the kit checkout (e.g. installed module).
		// In that case the canonical file isn't reachable; skip the drift
		// check. Inside the repo, the check protects against drift.
		t.Skipf("canonical contracts/scenario-rules.json not reachable from %s: %v", pkgDir, err)
	}

	// Compare semantically (not byte-for-byte) so trailing newlines or
	// JSON re-indentation don't trigger false positives. The contract
	// is the parsed structure, not the literal bytes.
	var embObj, canObj any
	require.NoError(t, json.Unmarshal(embedded, &embObj))
	require.NoError(t, json.Unmarshal(canonical, &canObj))

	embNorm, err := json.Marshal(embObj)
	require.NoError(t, err)
	canNorm, err := json.Marshal(canObj)
	require.NoError(t, err)

	if !bytes.Equal(embNorm, canNorm) {
		t.Fatalf(`scenario_rules_embedded.json is out of sync with contracts/scenario-rules.json
fix:
  cp contracts/scenario-rules.json \
    go/console/cli/conformance/verifynoleak/rules/scenario_rules_embedded.json
`)
	}
}
