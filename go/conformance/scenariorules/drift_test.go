package scenariorules_test

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
// contracts/scenario-rules.json (the canonical wire-format file
// 12fcc-scen owns) and the shared loader's vendored copy.
//
// On drift, the fix is documented in doc.go.
func TestEmbeddedJSONInSyncWithContracts(t *testing.T) {
	_, thisFile, _, ok := runtime.Caller(0)
	require.True(t, ok, "runtime.Caller failed; can't locate package dir")
	pkgDir := filepath.Dir(thisFile)
	embeddedPath := filepath.Join(pkgDir, "scenario_rules_embedded.json")
	// scenariorules is at go/conformance/scenariorules/ — three levels up
	// to repo root.
	canonicalPath := filepath.Join(pkgDir, "..", "..", "..", "contracts", "scenario-rules.json")

	embedded, err := os.ReadFile(embeddedPath)
	require.NoError(t, err, "embedded copy missing — see doc.go")
	canonical, err := os.ReadFile(canonicalPath)
	if err != nil {
		t.Skipf("canonical contracts/scenario-rules.json not reachable from %s: %v", pkgDir, err)
	}

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
    go/conformance/scenariorules/scenario_rules_embedded.json
`)
	}
}
