// Offline-override tests for `kit init`. White-box (package kitinit)
// to reach applyOfflineOverride and the fixture helpers from
// init_test.go.
package kitinit

import (
	"context"
	"io"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"hop.top/kit/go/console/cli"
)

// TestApplyOfflineOverride_ForcesOptOuts locks the precedence
// contract: an offline-tagged context forces NoGitHub and NoPush on;
// an untagged context leaves the gathered values alone; already-true
// values (explicit --no-github/--no-push) are never un-set.
func TestApplyOfflineOverride_ForcesOptOuts(t *testing.T) {
	offline := cli.WithOffline(context.Background(), true)

	in := Inputs{NoGitHub: false, NoPush: false}
	applyOfflineOverride(offline, &in)
	assert.True(t, in.NoGitHub, "--offline must force NoGitHub")
	assert.True(t, in.NoPush, "--offline must force NoPush")

	in = Inputs{NoGitHub: false, NoPush: false}
	applyOfflineOverride(context.Background(), &in)
	assert.False(t, in.NoGitHub, "untagged ctx must not touch NoGitHub")
	assert.False(t, in.NoPush, "untagged ctx must not touch NoPush")

	in = Inputs{NoGitHub: true, NoPush: true}
	applyOfflineOverride(offline, &in)
	assert.True(t, in.NoGitHub, "explicit --no-github must survive --offline")
	assert.True(t, in.NoPush, "explicit --no-push must survive --offline")
}

// stubGh shadows any real gh binary with a script that records its
// invocation and fails. If the offline override regresses, bootstrap
// step 12 invokes gh — the marker file appears and the run errors —
// instead of silently reaching the real GitHub CLI.
func stubGh(t *testing.T) (markerPath string) {
	t.Helper()
	stubDir := t.TempDir()
	markerPath = filepath.Join(stubDir, "gh-was-called")
	script := "#!/bin/sh\ntouch \"" + markerPath + "\"\nexit 1\n"
	require.NoError(t, os.WriteFile(filepath.Join(stubDir, "gh"), []byte(script), 0o755))
	t.Setenv("PATH", stubDir+string(os.PathListSeparator)+os.Getenv("PATH"))
	return markerPath
}

// TestInit_Bootstrap_Offline_SkipsGitHubAndPush drives the full RunE
// flow with an offline-tagged context and WITHOUT --no-github or
// --no-push. The override must skip GitHub repo creation (gh stays
// uninvoked) and the push, while the local scaffold still lands.
func TestInit_Bootstrap_Offline_SkipsGitHubAndPush(t *testing.T) {
	skipIfNoGitInit(t)
	preCommitGitIdentity(t)
	marker := stubGh(t)
	tplPath := fixtureInitTemplate(t)
	work := t.TempDir()
	t.Chdir(work)

	cmd := InitCmd(nil)
	cmd.SetArgs([]string{
		"mytool",
		"--from=" + tplPath,
		"--hop=false",
		// deliberately NO --no-github / --no-push: offline must
		// flip both on by itself.
		"--yes",
	})
	cmd.SetOut(io.Discard)
	cmd.SetErr(io.Discard)

	require.NoError(t, cmd.ExecuteContext(cli.WithOffline(context.Background(), true)))

	// Local scaffold still happens offline.
	target := filepath.Join(work, "mytool")
	require.DirExists(t, target)
	assert.FileExists(t, filepath.Join(target, "go.mod"))

	// GitHub repo creation was skipped: the gh stub never ran.
	_, err := os.Stat(marker)
	assert.True(t, os.IsNotExist(err),
		"--offline must skip gh invocation; marker stat err=%v", err)
}
