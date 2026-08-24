package record_test

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"hop.top/kit/go/conformance/svc"
	"hop.top/kit/go/console/cli/conformance/grade"
	"hop.top/kit/go/transport/api"
)

// failingStub responds to the same surface as stubScript but exits 7
// where the scenario demands 0 — the recorded cassette must grade to
// an honest fail.
const failingStub = `#!/bin/sh
case "$1" in
  status)
    printf '{"state":"degraded"}\n'
    exit 7
    ;;
esac
exit 0
`

// startSvc boots the real grading service (FSStore + SQL claim store
// + LibGrader) over HTTP against the fixture's scenario library and
// returns the base URL plus a minted grade token.
func startSvc(t *testing.T, scenariosRoot string) (string, string) {
	t.Helper()
	ctx := context.Background()

	store, err := svc.NewFSStore(ctx, scenariosRoot)
	require.NoError(t, err)

	claims, err := svc.OpenSQLClaimStore(filepath.Join(t.TempDir(), "claims.db"))
	require.NoError(t, err)
	t.Cleanup(func() { _ = claims.Close() })

	_, token, err := claims.Mint(ctx, svc.MintInput{
		Tenant:  "roundtrip-test",
		Scopes:  []string{"grade:stub"},
		TierMax: 3,
	})
	require.NoError(t, err)

	service := svc.NewService(store, claims, svc.LibGrader{})
	router := api.NewRouter()
	service.Mount(router)
	ts := httptest.NewServer(router)
	t.Cleanup(ts.Close)

	return ts.URL, token
}

// gradeCassette drives the real grade CLI leaf against the served
// scenario library and returns the parsed verdict document plus the
// leaf's error (nil on pass, the fail sentinel on fail).
func gradeCassette(t *testing.T, cassetteDir, service, token string) (map[string]any, error) {
	t.Helper()
	verdictFile := filepath.Join(t.TempDir(), "verdict.json")
	cmd := grade.Cmd()
	buf := &bytes.Buffer{}
	cmd.SetOut(buf)
	cmd.SetErr(buf)
	cmd.SetArgs([]string{
		cassetteDir,
		"--service", service,
		"--token", token,
		"--tier", "3",
		"--format", "json",
		"-o", verdictFile,
	})
	execErr := cmd.Execute()

	raw, readErr := os.ReadFile(verdictFile)
	require.NoError(t, readErr, "grade must write a verdict document even on fail (stderr: %s)", buf.String())
	var doc map[string]any
	require.NoError(t, json.Unmarshal(raw, &doc))
	return doc, execErr
}

// TestRoundTrip_RecordedCassetteGrades proves the full loop: the
// record leaf produces a cassette the real grading service accepts,
// and the grade leaf returns a genuine verdict computed from the
// recorded captures.
func TestRoundTrip_RecordedCassetteGrades(t *testing.T) {
	scPath, _, bin := fixture(t)
	// The fixture tree doubles as the scenario library root
	// (<root>/scenarios/stub/status-json/1.0.0/scenario.yaml).
	root := filepath.Dir(filepath.Dir(filepath.Dir(filepath.Dir(filepath.Dir(scPath)))))
	service, token := startSvc(t, root)

	// Pass case: the stub honors the scenario contract.
	passDir := filepath.Join(t.TempDir(), "pass-cassette")
	_, err := runLeaf(t, "--scenario", scPath, "--binary", bin, "--out", passDir, "--format", "json")
	require.NoError(t, err)

	doc, gradeErr := gradeCassette(t, passDir, service, token)
	assert.NoError(t, gradeErr, "a conforming recording must grade pass")
	assert.Equal(t, "pass", doc["verdict"], "verdict doc: %v", doc)

	// Fail case: a binary that violates the contract must produce a
	// cassette that grades to an honest fail — recorded captures are
	// graded, never massaged.
	failBin := filepath.Join(t.TempDir(), "stubcli")
	require.NoError(t, os.WriteFile(failBin, []byte(failingStub), 0o755))
	failDir := filepath.Join(t.TempDir(), "fail-cassette")
	_, err = runLeaf(t, "--scenario", scPath, "--binary", failBin, "--out", failDir, "--format", "json")
	require.NoError(t, err, "recording a misbehaving binary succeeds; grading is where it fails")

	doc, gradeErr = gradeCassette(t, failDir, service, token)
	require.Error(t, gradeErr, "a violating recording must not grade pass")
	assert.Equal(t, "fail", doc["verdict"], "verdict doc: %v", doc)

	// Tier-3 facets pin the failure to the asserted factor (F11, the
	// exit-code contract).
	facets, _ := doc["facets"].([]any)
	require.NotEmpty(t, facets, "tier-3 verdict must carry facets")
	found := false
	for _, f := range facets {
		m, _ := f.(map[string]any)
		if m["factor"] == float64(11) {
			assert.Equal(t, "fail", m["status"], "factor 11 must fail for exit 7 != 0")
			found = true
		}
	}
	assert.True(t, found, "facets must cover factor 11: %v", facets)
}
