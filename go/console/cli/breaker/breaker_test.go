package breaker_test

import (
	"bytes"
	"strings"
	"testing"

	"github.com/spf13/viper"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	bcmd "hop.top/kit/go/console/cli/breaker"
	bpkg "hop.top/kit/go/core/breaker"
)

func runCmd(t *testing.T, args ...string) (string, error) {
	t.Helper()
	viper.Reset()
	cmd := bcmd.Cmd()
	buf := &bytes.Buffer{}
	cmd.SetOut(buf)
	cmd.SetErr(buf)
	cmd.SetArgs(args)
	err := cmd.Execute()
	return buf.String(), err
}

func TestList_NoErrorEvenIfEmpty(t *testing.T) {
	_, err := runCmd(t, "list")
	require.NoError(t, err)
}

func TestList_PrintsRegisteredBreaker(t *testing.T) {
	const name = "cli-list-test"
	t.Cleanup(func() { bpkg.Unregister(name) })
	bpkg.New(name)

	out, err := runCmd(t, "list")
	require.NoError(t, err)
	assert.Contains(t, out, name)
	assert.Contains(t, strings.ToLower(out), "closed")
}

func TestShow_NotFoundExits1(t *testing.T) {
	_, err := runCmd(t, "show", "does-not-exist")
	require.Error(t, err)
}

func TestShow_PrintsStats(t *testing.T) {
	const name = "cli-show-test"
	t.Cleanup(func() { bpkg.Unregister(name) })
	bpkg.New(name)

	out, err := runCmd(t, "show", name)
	require.NoError(t, err)
	assert.Contains(t, out, name)
}

func TestReset_SingleBreaker(t *testing.T) {
	const name = "cli-reset-test"
	t.Cleanup(func() { bpkg.Unregister(name) })
	b := bpkg.New(name)
	b.Trip("test")
	require.Equal(t, bpkg.Open, b.State())

	_, err := runCmd(t, "reset", name)
	require.NoError(t, err)
	assert.Equal(t, bpkg.Closed, b.State())
}

func TestReset_AllRequiresYes(t *testing.T) {
	const name = "cli-reset-all-test"
	t.Cleanup(func() { bpkg.Unregister(name) })
	b := bpkg.New(name)
	b.Trip("test")
	require.Equal(t, bpkg.Open, b.State())

	// without --yes the command should error out (refuses to reset all)
	_, err := runCmd(t, "reset", "--all")
	require.Error(t, err)
	// breaker still tripped
	assert.Equal(t, bpkg.Open, b.State())

	_, err = runCmd(t, "reset", "--all", "--yes")
	require.NoError(t, err)
	assert.Equal(t, bpkg.Closed, b.State())
}

func TestReset_NotFoundExits1(t *testing.T) {
	_, err := runCmd(t, "reset", "missing-name")
	require.Error(t, err)
}
