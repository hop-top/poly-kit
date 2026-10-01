package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/spf13/cobra"
	"github.com/stretchr/testify/require"
	"hop.top/kit/go/console/cli"
	"hop.top/kit/go/runtime/peer"
)

// kitDryRunCase is how one write or destructive leaf of the kit binary
// shows it honors --dry-run: args for an invocation run here in a
// sandbox, or the test that already proves it.
type kitDryRunCase struct {
	args []string
	// plan: the leaf answers with a cli.Plan. Legacy previews print
	// their own text; for those only "nothing changed" is checked.
	plan bool
	// coveredBy names the test that runs this leaf under --dry-run
	// when it needs fixtures this walk does not build.
	coveredBy string
}

func kitBinaryDryRunCases(sandbox string) map[string]kitDryRunCase {
	out := filepath.Join(sandbox, "out")
	db := filepath.Join(out, "claims.db")
	return map[string]kitDryRunCase{
		"kit breaker reset":                {args: []string{"breaker", "reset", "--all", "--yes"}, plan: true},
		"kit conformance badge":            {args: []string{"conformance", "badge", "--emit-seed", "--output", filepath.Join(out, "b.json")}, plan: true},
		"kit conformance grade":            {args: []string{"conformance", "grade", out, "--service", "http://127.0.0.1:1"}, plan: true},
		"kit conformance harness record":   {coveredBy: "go/console/cli TestKitLeaves_HonorDryRunOrOptOut"},
		"kit conformance svc serve":        {args: []string{"conformance", "svc", "serve", "--scenarios-root", out, "--claims-db", db}, plan: true},
		"kit conformance svc token mint":   {args: []string{"conformance", "svc", "token", "mint", "--claims-db", db, "--scope", "grade:x"}, plan: true},
		"kit conformance svc token revoke": {args: []string{"conformance", "svc", "token", "revoke", "t1", "--claims-db", db}, plan: true},
		"kit init":                         {coveredBy: "cmd/kit/init TestInit_Bootstrap_DryRun"},
		"kit peer trust":                   {args: []string{"peer", "trust", "p1"}, plan: true},
		"kit peer block":                   {args: []string{"peer", "block", "p1"}, plan: true},
		"kit peer revoke":                  {args: []string{"peer", "revoke", "p1"}, plan: true},
		"kit serve":                        {args: []string{"serve"}, plan: true},
		"kit symlink": {args: []string{"symlink", "--target", filepath.Join(sandbox, "fixture", "demo"),
			"--name", "demo", "--dir", filepath.Join(sandbox, "bin")}},
		"kit telemetry disable": {args: []string{"telemetry", "disable"}},
		"kit telemetry enable":  {args: []string{"telemetry", "enable"}},
		"kit telemetry reset":   {args: []string{"telemetry", "reset", "--yes"}, plan: true},
		"kit uxp run":           {args: []string{"uxp", "run", "--tool", "claude", "--exec", "hello"}, plan: true},
		"kit uxp resume":        {args: []string{"uxp", "resume", "--tool", "claude", "--continue", "--exec"}, plan: true},
	}
}

// TestKitBinary_LeavesHonorDryRunOrOptOut walks the tree the kit
// binary ships. A leaf --dry-run applies to (write or destructive
// tier, not opted out) needs a case above; running it must change
// nothing in the sandbox and, for a Plan leaf, print the leaf's plan.
// A new leaf that neither previews nor opts out fails here.
func TestKitBinary_LeavesHonorDryRunOrOptOut(t *testing.T) {
	sandbox := isolateKitBinary(t)
	cases := kitBinaryDryRunCases(sandbox)

	root, eng := newKitRoot("dev")
	eng.close()
	require.NoError(t, root.PeerRegistry.Add(peer.PeerInfo{
		ID: "p1", PublicKey: []byte("key"), Addrs: []string{"127.0.0.1:1"},
	}))
	var applied []string
	walkKitLeaves(root.Cmd, func(c *cobra.Command) {
		if cli.IsDryRunSupported(c) {
			applied = append(applied, c.CommandPath())
		}
	})
	sort.Strings(applied)
	for _, path := range applied {
		if _, ok := cases[path]; !ok {
			t.Errorf("%s: --dry-run applies but no case covers it; honor it "+
				"(cli.RenderPlan before the first side effect) and add a case, "+
				"or cli.OptOutDryRun + cli.SetDryRunRationale", path)
		}
	}
	for path := range cases {
		if !containsPath(applied, path) {
			t.Errorf("%s: case for a leaf --dry-run does not apply to; drop it", path)
		}
	}

	for _, path := range applied {
		tc, ok := cases[path]
		if !ok || tc.coveredBy != "" {
			continue
		}
		t.Run(path, func(t *testing.T) {
			before := sandboxSnapshot(t, sandbox)
			root, eng := newKitRoot("dev")
			defer eng.close()
			var stdout, stderr bytes.Buffer
			root.Cmd.SetOut(&stdout)
			root.Cmd.SetErr(&stderr)
			root.Cmd.SetIn(strings.NewReader(""))
			root.Cmd.SetArgs(append(tc.args, "--dry-run", "--confirm", "no", "--format", "json"))
			ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
			defer cancel()
			require.NoError(t, root.Execute(ctx), "stderr=%s", stderr.String())
			if tc.plan {
				var plan map[string]any
				require.NoError(t, json.Unmarshal(stdout.Bytes(), &plan),
					"stdout is not a JSON plan: %q", stdout.String())
				require.Equal(t, path, plan["command"])
				require.Contains(t, plan, "effects")
			}
			require.Equal(t, before, sandboxSnapshot(t, sandbox), "--dry-run changed the sandbox")
		})
	}
}

// isolateKitBinary points HOME, the XDG roots, the working directory
// and PATH at a fresh sandbox, writes the symlink target, and returns
// the sandbox.
func isolateKitBinary(t *testing.T) string {
	t.Helper()
	sandbox := t.TempDir()
	for _, kv := range [][2]string{
		{"HOME", "home"}, {"XDG_CONFIG_HOME", "config"}, {"XDG_DATA_HOME", "data"},
		{"XDG_STATE_HOME", "state"}, {"XDG_CACHE_HOME", "cache"},
	} {
		dir := filepath.Join(sandbox, kv[1])
		require.NoError(t, os.MkdirAll(dir, 0o755))
		t.Setenv(kv[0], dir)
	}
	for _, k := range []string{"CI", "KIT_DRY_RUN", "KIT_CONFORMANCE_SERVICE", "KIT_CONFORMANCE_TOKEN", "GITHUB_TOKEN"} {
		t.Setenv(k, "")
	}
	// No binary is reachable by name: a leaf that ignores --dry-run
	// and spawns one fails instead of running it.
	t.Setenv("PATH", filepath.Join(sandbox, "bin"))
	for _, d := range []string{"out", "cwd", "bin", "fixture"} {
		require.NoError(t, os.MkdirAll(filepath.Join(sandbox, d), 0o755))
	}
	require.NoError(t, os.WriteFile(filepath.Join(sandbox, "fixture", "demo"), []byte("#!/bin/sh\n"), 0o755))
	t.Chdir(filepath.Join(sandbox, "cwd"))
	return sandbox
}

func sandboxSnapshot(t *testing.T, root string) []string {
	t.Helper()
	var out []string
	require.NoError(t, filepath.WalkDir(root, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		info, err := d.Info()
		if err != nil {
			return err
		}
		if d.IsDir() {
			// A directory's mtime moves with any transient entry —
			// a writability probe created and removed — which leaves
			// no state behind; its entries are listed on their own.
			out = append(out, p+" dir "+info.Mode().String())
			return nil
		}
		out = append(out, fmt.Sprintf("%s %d %s %d", p, info.Size(), info.Mode(), info.ModTime().UnixNano()))
		return nil
	}))
	return out
}

func walkKitLeaves(c *cobra.Command, fn func(*cobra.Command)) {
	if !c.HasSubCommands() && c.Runnable() {
		fn(c)
	}
	for _, sub := range c.Commands() {
		walkKitLeaves(sub, fn)
	}
}

func containsPath(xs []string, x string) bool {
	for _, v := range xs {
		if v == x {
			return true
		}
	}
	return false
}
