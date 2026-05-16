package stage_test

import (
	"bytes"
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"hop.top/kit/go/console/stage"
	"hop.top/kit/go/core/projects"
	corestage "hop.top/kit/go/core/stage"
)

// setupXDG isolates each test under its own XDG_CONFIG_HOME.
func setupXDG(t *testing.T) {
	t.Helper()
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
}

// staticResolver returns the same scope for every call.
func staticResolver(s string) func() string {
	return func() string { return s }
}

func runCmd(t *testing.T, cfg stage.Config, args ...string) (string, string, error) {
	t.Helper()
	cmd := stage.New(cfg)
	out := &bytes.Buffer{}
	errOut := &bytes.Buffer{}
	cmd.SetOut(out)
	cmd.SetErr(errOut)
	cmd.SetArgs(args)
	err := cmd.Execute()
	return out.String(), errOut.String(), err
}

// vetoPub vetoes Propose by returning an error from Publish.
type vetoPub struct {
	on  string
	err error
}

func (v *vetoPub) Publish(_ context.Context, topic, _ string, _ any) error {
	if v.on == "" || topic == v.on {
		return v.err
	}
	return nil
}

func TestShow_NoEntry_PrintsActiveDefault(t *testing.T) {
	setupXDG(t)
	cfg := stage.Config{ProjectResolver: staticResolver("ops")}
	out, _, err := runCmd(t, cfg, "show")
	require.NoError(t, err)
	assert.Contains(t, out, "stage: active (default)")
}

func TestShow_WithStage_PrintsTable(t *testing.T) {
	setupXDG(t)
	require.NoError(t, projects.Write("ops", projects.Entry{Path: "/tmp/ops"}))
	require.NoError(t, corestage.Set("ops", corestage.State{
		Stage:  corestage.StageMaintenance,
		Reason: "legacy",
		Actor:  "alice",
	}))

	cfg := stage.Config{ProjectResolver: staticResolver("ops")}
	out, _, err := runCmd(t, cfg, "show")
	require.NoError(t, err)
	assert.Contains(t, out, "ops")
	assert.Contains(t, out, "maintenance")
	assert.Contains(t, out, "alice")
	assert.Contains(t, out, "legacy")
}

func TestShow_NoResolverNoArg_Errors(t *testing.T) {
	setupXDG(t)
	_, _, err := runCmd(t, stage.Config{}, "show")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "scope required")
}

func TestSet_HappyPath(t *testing.T) {
	setupXDG(t)
	require.NoError(t, projects.Write("ops", projects.Entry{Path: "/tmp/ops"}))

	cfg := stage.Config{ProjectResolver: staticResolver("ops")}
	out, _, err := runCmd(t, cfg, "set", "maintenance", "--reason", "going dark")
	require.NoError(t, err)
	assert.Contains(t, out, "ok: ops → maintenance")

	got, err := corestage.Read("ops")
	require.NoError(t, err)
	assert.Equal(t, corestage.StageMaintenance, got.Stage)
	assert.Equal(t, "going dark", got.Reason)
}

func TestSet_InvalidStage_Errors(t *testing.T) {
	setupXDG(t)
	cfg := stage.Config{ProjectResolver: staticResolver("ops")}
	_, _, err := runCmd(t, cfg, "set", "bogus", "--reason", "x")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "invalid mode")
}

func TestSet_NonActiveRequiresReason(t *testing.T) {
	setupXDG(t)
	cfg := stage.Config{ProjectResolver: staticResolver("ops")}
	_, _, err := runCmd(t, cfg, "set", "sunset")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "--reason required")
}

func TestSet_PastUntilRejected(t *testing.T) {
	setupXDG(t)
	require.NoError(t, projects.Write("ops", projects.Entry{Path: "/tmp/ops"}))
	cfg := stage.Config{ProjectResolver: staticResolver("ops")}
	past := time.Now().Add(-1 * time.Hour).UTC().Format(time.RFC3339)
	_, _, err := runCmd(t, cfg, "set", "maintenance", "--reason", "x", "--until", past)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "not in the future")
}

func TestSet_DurationUntilParsed(t *testing.T) {
	setupXDG(t)
	require.NoError(t, projects.Write("ops", projects.Entry{Path: "/tmp/ops"}))
	cfg := stage.Config{ProjectResolver: staticResolver("ops")}
	_, _, err := runCmd(t, cfg, "set", "maintenance", "--reason", "x", "--until", "720h")
	require.NoError(t, err)
	got, err := corestage.Read("ops")
	require.NoError(t, err)
	require.NotNil(t, got.Until)
	assert.True(t, got.Until.After(time.Now()))
}

func TestSet_VetoFromPublisher_PropagatesError(t *testing.T) {
	setupXDG(t)
	require.NoError(t, projects.Write("ops", projects.Entry{Path: "/tmp/ops"}))
	cfg := stage.Config{
		ProjectResolver: staticResolver("ops"),
		Publisher: &vetoPub{
			on:  string(corestage.DefaultTopics.Proposed),
			err: errors.New("denied by policy"),
		},
	}
	_, _, err := runCmd(t, cfg, "set", "archived", "--reason", "vault")
	require.Error(t, err)

	var pde *corestage.PolicyDeniedError
	assert.True(t, errors.As(err, &pde), "expected PolicyDeniedError, got %T", err)

	// Persisted state must NOT have changed.
	got, err := corestage.Read("ops")
	require.NoError(t, err)
	assert.Equal(t, corestage.StageActive, got.Stage)
}

func TestSet_ConfirmSkipsPropose(t *testing.T) {
	setupXDG(t)
	require.NoError(t, projects.Write("ops", projects.Entry{Path: "/tmp/ops"}))
	cfg := stage.Config{
		ProjectResolver: staticResolver("ops"),
		Publisher: &vetoPub{
			on:  string(corestage.DefaultTopics.Proposed),
			err: errors.New("would deny"),
		},
	}
	_, _, err := runCmd(t, cfg, "set", "archived", "--reason", "vault", "--confirm")
	require.NoError(t, err, "--confirm must skip Propose veto")
}

func TestWhy_PrintsRulesForStage(t *testing.T) {
	setupXDG(t)
	require.NoError(t, projects.Write("ops", projects.Entry{Path: "/tmp/ops"}))
	require.NoError(t, corestage.Set("ops", corestage.State{
		Stage:  corestage.StageArchived,
		Reason: "long gone",
	}))
	cfg := stage.Config{ProjectResolver: staticResolver("ops")}
	out, _, err := runCmd(t, cfg, "why")
	require.NoError(t, err)
	assert.Contains(t, out, "archived")
	assert.Contains(t, out, "archived-blocks-all-mutations")
}

func TestList_OneRowPerEntry(t *testing.T) {
	setupXDG(t)
	require.NoError(t, projects.Write("ops", projects.Entry{Path: "/tmp/ops"}))
	require.NoError(t, projects.Write("kit", projects.Entry{Path: "/tmp/kit"}))
	require.NoError(t, corestage.Set("ops", corestage.State{
		Stage:  corestage.StageSunset,
		Reason: "winding down",
	}))
	cfg := stage.Config{}
	out, _, err := runCmd(t, cfg, "list")
	require.NoError(t, err)

	// Both scopes should appear, sorted alphabetically.
	idxKit := strings.Index(out, "kit")
	idxOps := strings.Index(out, "ops")
	require.NotEqual(t, -1, idxKit)
	require.NotEqual(t, -1, idxOps)
	assert.Less(t, idxKit, idxOps, "list rows are sorted alphabetically")
	assert.Contains(t, out, "sunset")
}
