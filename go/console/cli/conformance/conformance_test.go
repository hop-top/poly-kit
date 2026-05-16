package conformance_test

import (
	"bytes"
	"errors"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"hop.top/kit/go/console/cli/conformance"
)

func runCmd(t *testing.T, args ...string) (string, error) {
	t.Helper()
	cmd := conformance.Cmd()
	buf := &bytes.Buffer{}
	cmd.SetOut(buf)
	cmd.SetErr(buf)
	cmd.SetArgs(args)
	err := cmd.Execute()
	return buf.String(), err
}

func TestCmd_HelpListsAllChildren(t *testing.T) {
	out, err := runCmd(t, "--help")
	require.NoError(t, err)
	for _, want := range []string{"verify-no-leak", "install-hooks", "static", "harness"} {
		assert.Contains(t, out, want, "help should advertise reserved child %q", want)
	}
}

func TestCmd_AliasConIsRegistered(t *testing.T) {
	cmd := conformance.Cmd()
	assert.Contains(t, cmd.Aliases, "con", "conformance should expose 'con' as a shorter alias")
}

func TestVerifyNoLeak_MutualExclusion_StagedAndAudit(t *testing.T) {
	// design.md §6: scan-source flags are mutually exclusive.
	_, err := runCmd(t, "verify-no-leak", "--staged", "--audit")
	require.Error(t, err)
	assert.True(t, errors.Is(err, conformance.ErrUsage), "mutual-exclusion violation must map to ErrUsage")
}

func TestVerifyNoLeak_PRBody_NegativeIsUsageError(t *testing.T) {
	// --pr-body N where N<0 is a usage error. The happy path
	// (N>0 with `gh` on PATH) is exercised via source/prbody_test.go
	// where we can stub the gh binary.
	_, err := runCmd(t, "verify-no-leak", "--pr-body", "-1")
	require.Error(t, err)
	assert.True(t, errors.Is(err, conformance.ErrUsage))
	assert.Contains(t, err.Error(), "positive PR number")
}

func TestVerifyNoLeak_PRBody_ZeroIsNoOp(t *testing.T) {
	// --pr-body=0 is the no-op default; install-hooks and other
	// scan-only flows should not be broken by the new wiring.
	// Pass --paths pointing at a known-empty list; we accept any
	// non-usage error class (--paths inside a tempdir may surface
	// io_error or succeed depending on resolution). The point is
	// that --pr-body=0 alone must not trip the usage guard.
	_, err := runCmd(t, "verify-no-leak", "--pr-body", "0", "--quiet-on-clean", "--paths", "nonexistent-but-ok")
	if err != nil {
		// Any error class is fine here EXCEPT a usage error about
		// --pr-body — that would mean the no-op default broke.
		assert.NotContains(t, err.Error(), "--pr-body", "--pr-body=0 must be a no-op")
	}
}

func TestVerifyNoLeak_HasAllDesignedFlags(t *testing.T) {
	// Lock in the flag surface from design.md §6 so future PRs
	// cannot silently drop one. We probe each flag by attempting
	// --help and asserting it appears in the output.
	out, err := runCmd(t, "verify-no-leak", "--help")
	require.NoError(t, err)
	for _, want := range []string{
		"--staged", "--audit", "--diff", "--paths",
		"--commit-range", "--commit-msg-file", "--pr-body",
		"--rules-file", "--max-file-size",
		"--format", "--quiet-on-clean",
	} {
		assert.Contains(t, out, want, "help should advertise %q", want)
	}
}

func TestInstallHooks_AcceptsAllDesignedFlags(t *testing.T) {
	// Sanity-check that the documented flag surface is wired. We pass
	// --dry-run to guarantee we don't mutate the host repo when the
	// test binary runs from one. Full behavioral coverage lives in
	// install_hooks_test.go (uses t.TempDir()).
	dir := t.TempDir()
	for _, args := range [][]string{
		{"install-hooks", "--root", dir, "--dry-run"},
		{"install-hooks", "--root", dir, "--dry-run", "--force"},
		{"install-hooks", "--root", dir, "--dry-run", "--format", "json"},
	} {
		_, err := runCmd(t, args...)
		assert.NoErrorf(t, err, "flags %v should be accepted", args)
	}
}

func TestReservedChild_StaticExits3(t *testing.T) {
	_, err := runCmd(t, "static")
	require.Error(t, err)
	assert.True(t, errors.Is(err, conformance.ErrUsage), "reserved subcommand should return ErrUsage")
	assert.Contains(t, err.Error(), "12fcc-static")

	code, ok := conformance.ExitCode(err)
	require.True(t, ok)
	assert.Equal(t, 3, code, "ErrUsage should map to exit code 3")
}

func TestReservedChild_HarnessExits3(t *testing.T) {
	_, err := runCmd(t, "harness")
	require.Error(t, err)
	assert.True(t, errors.Is(err, conformance.ErrUsage))
	assert.Contains(t, err.Error(), "12fcc-harness")
}

func TestExitCode_Mapping(t *testing.T) {
	cases := []struct {
		name string
		err  error
		want int
		ok   bool
	}{
		{"nil maps to 0", nil, 0, true},
		{"leak detected", conformance.ErrLeakDetected, 2, true},
		{"usage", conformance.ErrUsage, 3, true},
		{"io", conformance.ErrIO, 4, true},
		{"config", conformance.ErrConfig, 5, true},
		{"unknown returns not-ok", errors.New("random"), 0, false},
		{"wrapped leak still maps", wrap(conformance.ErrLeakDetected), 2, true},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			code, ok := conformance.ExitCode(c.err)
			assert.Equal(t, c.want, code)
			assert.Equal(t, c.ok, ok)
		})
	}
}

func TestReservedChild_HelpLine(t *testing.T) {
	out, err := runCmd(t, "--help")
	require.NoError(t, err)
	// The help summary for a reserved name should signal that it
	// belongs to a sibling track so contributors don't try to
	// re-implement it here.
	lower := strings.ToLower(out)
	assert.Contains(t, lower, "reserved for 12fcc-static")
	assert.Contains(t, lower, "reserved for 12fcc-harness")
}

func wrap(err error) error { return errors.Join(err, errors.New("context")) }
