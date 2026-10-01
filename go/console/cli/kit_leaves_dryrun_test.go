package cli_test

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
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
	"hop.top/cite/handle/generate"
	"hop.top/cite/scheme"
	"hop.top/kit/go/console/alias"
	"hop.top/kit/go/console/cli"
	breakercmd "hop.top/kit/go/console/cli/breaker"
	configcmd "hop.top/kit/go/console/cli/config"
	conformancecmd "hop.top/kit/go/console/cli/conformance"
	routercmd "hop.top/kit/go/console/cli/router"
	scopecmd "hop.top/kit/go/console/cli/scope"
	uxpcmd "hop.top/kit/go/core/uxp/invoke/cmd/uxp"
	"hop.top/kit/go/runtime/peer"
	_ "hop.top/kit/go/storage/kv/sqlite"
)

// kitLeafDryRunCases is every write or destructive leaf kit ships,
// keyed by its path under the test root, with the arguments one
// --dry-run invocation needs. Each must answer --dry-run with a Plan
// and change nothing. A leaf that cannot honor --dry-run opts out
// with cli.OptOutDryRun (and cli.SetDryRunRationale) instead of
// appearing here.
func kitLeafDryRunCases(sandbox string) map[string][]string {
	scenario, binary := recordFixture(sandbox)
	return map[string][]string{
		"ktool alias add":             {"alias", "add", "x", "status"},
		"ktool alias delete":          {"alias", "delete", "x"},
		"ktool peer trust":            {"peer", "trust", "p1"},
		"ktool peer block":            {"peer", "block", "p1"},
		"ktool peer revoke":           {"peer", "revoke", "p1"},
		"ktool quota reset":           {"quota", "reset", "--all", "-c", "services.api.quota.ops=10"},
		"ktool serve":                 {"serve"},
		"ktool token key create":      {"token", "key", "create", "--sub", "alice"},
		"ktool token key revoke":      {"token", "key", "revoke", "k1"},
		"ktool uri handler generate":  {"uri", "handler", "generate", "--platform", "linux", "--output", filepath.Join(sandbox, "out", "x.desktop")},
		"ktool breaker reset":         {"breaker", "reset", "--all", "--yes"},
		"ktool conformance badge":     {"conformance", "badge", "--emit-seed", "--output", filepath.Join(sandbox, "out", "badge.json")},
		"ktool conformance grade":     {"conformance", "grade", filepath.Join(sandbox, "out"), "--service", "http://127.0.0.1:1"},
		"ktool conformance svc serve": {"conformance", "svc", "serve", "--scenarios-root", filepath.Join(sandbox, "out"), "--claims-db", filepath.Join(sandbox, "out", "claims.db")},
		"ktool conformance svc token mint": {"conformance", "svc", "token", "mint",
			"--claims-db", filepath.Join(sandbox, "out", "claims.db"), "--scope", "grade:x"},
		"ktool conformance svc token revoke": {"conformance", "svc", "token", "revoke", "t1",
			"--claims-db", filepath.Join(sandbox, "out", "claims.db")},
		"ktool conformance harness record": {"conformance", "harness", "record",
			"--scenario", scenario, "--binary", binary, "--out", filepath.Join(sandbox, "out", "cassette")},
		"ktool uxp run":    {"uxp", "run", "--tool", "claude", "--exec", "hello"},
		"ktool uxp resume": {"uxp", "resume", "--tool", "claude", "--continue", "--exec"},
	}
}

// newKitLeavesRoot mounts every command kit ships to adopters: the
// cli.New options that register commands, the commands Root builds,
// and the command packages under go/console/cli and go/core/uxp.
func newKitLeavesRoot(t *testing.T, sandbox string) *cli.Root {
	t.Helper()
	r := cli.New(cli.Config{Name: "ktool", Version: "0.0.0", Short: "kit leaves", DisableValidate: true},
		cli.WithIdentity(cli.IdentityConfig{Dir: filepath.Join(sandbox, "identity")}),
		cli.WithPeers(cli.PeerConfig{DataDir: filepath.Join(sandbox, "peers")}),
		cli.WithAPI(cli.APIConfig{Addr: "127.0.0.1:0"}),
		cli.WithAPIKeys(cli.APIKeysConfig{Path: filepath.Join(sandbox, "apikeys.db")}),
		cli.WithQuotaCommand(),
		cli.WithAuditCommand(),
		cli.WithStatus(cli.StatusConfig{}),
		cli.WithURI(cli.URIConfig{
			Policy: scheme.Policy{DefaultNamespaceSegments: 1},
			Handler: cli.URIHandlerConfig{
				Vendor: "hop-top", App: "ktool", Language: generate.LanguageGo,
				Scheme: "ktool", AppPath: "/usr/local/bin/ktool",
			},
		}),
		cli.WithPromptSource(func() *cli.PromptTTY { return nil }),
	)
	store := alias.NewStore(filepath.Join(sandbox, "aliases.yaml"))
	r.Cmd.AddCommand(
		r.AliasCmd(store),
		r.AliasesCmd(),
		breakercmd.Cmd(),
		conformancecmd.Cmd(),
		scopecmd.Cmd(),
		routercmd.Cmd(),
		configcmd.Command("ktool"),
		uxpcmd.Cmd(),
	)
	return r
}

// TestKitLeaves_HonorDryRunOrOptOut walks every leaf kit ships. A
// leaf --dry-run is applied to (write or destructive tier, not opted
// out) must have a case above, and running that case must print a
// Plan for the leaf and leave the sandbox untouched. A new kit leaf
// that neither previews nor opts out fails here.
func TestKitLeaves_HonorDryRunOrOptOut(t *testing.T) {
	sandbox := isolateKitLeaves(t)
	cases := kitLeafDryRunCases(sandbox)

	r := newKitLeavesRoot(t, sandbox)
	// The peer the peer leaves name: a dry run reads it as the real
	// run would, and refuses an unknown one the same way.
	require.NoError(t, r.PeerRegistry.Add(peer.PeerInfo{
		ID: "p1", PublicKey: []byte("key"), Addrs: []string{"127.0.0.1:1"},
	}))
	var applied []string
	walkLeaves(r.Cmd, func(c *cobra.Command) {
		if cli.IsDryRunSupported(c) {
			applied = append(applied, c.CommandPath())
		}
	})
	sort.Strings(applied)
	for _, path := range applied {
		if _, ok := cases[path]; !ok {
			t.Errorf("%s: --dry-run applies but no case runs it; honor it "+
				"(cli.RenderPlan before the first side effect) and add a case, "+
				"or cli.OptOutDryRun + cli.SetDryRunRationale", path)
		}
	}
	for path := range cases {
		if !contains(applied, path) {
			t.Errorf("%s: case for a leaf --dry-run does not apply to; drop it", path)
		}
	}

	for _, path := range applied {
		args, ok := cases[path]
		if !ok {
			continue
		}
		t.Run(path, func(t *testing.T) {
			before := snapshot(t, sandbox)
			r := newKitLeavesRoot(t, sandbox)
			stdout, stderr, err := execute(r, append(args, "--dry-run", "--confirm", "no", "--format", "json"))
			require.NoError(t, err, "stderr=%s", stderr)
			assertPlan(t, stdout, path)
			require.Equal(t, before, snapshot(t, sandbox), "--dry-run changed the sandbox")
		})
	}
}

func assertPlan(t *testing.T, stdout, path string) {
	t.Helper()
	var plan map[string]any
	require.NoError(t, json.Unmarshal([]byte(stdout), &plan), "stdout is not a JSON plan: %q", stdout)
	require.Equal(t, path, plan["command"], "plan names the leaf")
	require.Contains(t, plan, "effects")
	require.Contains(t, plan, "generated_at")
}

func execute(r *cli.Root, args []string) (string, string, error) {
	var out, errb bytes.Buffer
	r.Cmd.SetOut(&out)
	r.Cmd.SetErr(&errb)
	r.Cmd.SetIn(strings.NewReader(""))
	r.Cmd.SetArgs(args)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	err := r.Execute(ctx)
	return out.String(), errb.String(), err
}

// isolateKitLeaves points every place a kit leaf writes at a fresh
// sandbox and returns it: HOME, the XDG roots and the working
// directory.
func isolateKitLeaves(t *testing.T) string {
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
	t.Setenv("PATH", filepath.Join(sandbox, "no-bin"))
	cwd := filepath.Join(sandbox, "cwd")
	require.NoError(t, os.MkdirAll(filepath.Join(sandbox, "out"), 0o755))
	require.NoError(t, os.MkdirAll(cwd, 0o755))
	t.Chdir(cwd)
	return sandbox
}

// snapshot lists every path under root, files with size, mode and
// mtime, so a write, create or delete under it shows as a difference.
func snapshot(t *testing.T, root string) []string {
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

func walkLeaves(c *cobra.Command, fn func(*cobra.Command)) {
	if !c.HasSubCommands() && c.Runnable() {
		fn(c)
	}
	for _, sub := range c.Commands() {
		walkLeaves(sub, fn)
	}
}

func contains(xs []string, x string) bool {
	for _, v := range xs {
		if v == x {
			return true
		}
	}
	return false
}

// recordFixture writes the stub binary, story and scenario the
// harness record leaf validates before it plans, and returns the
// scenario and binary paths.
func recordFixture(sandbox string) (string, string) {
	root := filepath.Join(sandbox, "fixture")
	const story = "schema_version: \"1\"\nstory_id: stub.status\ntitle: Status\nbinary: stubcli\n" +
		"intent: read status\nsteps:\n  - id: status\n    intent: read status\n" +
		"    invoke: [\"stubcli\", \"status\"]\n    capture: [exit_code]\n"
	sum := sha256.Sum256([]byte(story))
	scenario := "schema_version: \"1\"\nscenario_id: status\nbinary: stubcli\nfactor_coverage: [11]\ntier: 3\n" +
		"story_ref:\n  story_id: stub.status\n  story_path: stories/status.yaml\n" +
		"  content_hash: \"sha256:" + hex.EncodeToString(sum[:]) + "\"\n" +
		"steps:\n  - id: status\n    invoke: [\"stubcli\", \"status\"]\n    capture: [exit_code]\n" +
		"assertions:\n  - id: ok\n    kind: exit_code_equals\n    on: status\n    factor: 11\n    value: 0\n"
	scDir := filepath.Join(root, "scenarios", "stub", "status", "1.0.0")
	bin := filepath.Join(root, "stubcli")
	mustWrite(filepath.Join(root, "stories", "status.yaml"), story, 0o644)
	mustWrite(filepath.Join(scDir, "scenario.yaml"), scenario, 0o644)
	mustWrite(bin, "#!/bin/sh\nexit 0\n", 0o755)
	return filepath.Join(scDir, "scenario.yaml"), bin
}

func mustWrite(path, body string, mode os.FileMode) {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		panic(err)
	}
	if err := os.WriteFile(path, []byte(body), mode); err != nil {
		panic(err)
	}
}
