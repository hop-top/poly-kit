package cli_test

import (
	"bytes"
	"encoding/base64"
	"path/filepath"
	"sort"
	"strings"
	"testing"

	"github.com/santhosh-tekuri/jsonschema/v5"
	"github.com/spf13/cobra"

	speccli "hop.top/kit/go/ai/toolspec/cli"
	"hop.top/kit/go/conformance/harness"
	"hop.top/kit/go/console/alias"
	kitcli "hop.top/kit/go/console/cli"
	"hop.top/kit/go/runtime/peer"
)

// The tests in this file hold kit to the contract it asks of its
// adopters: every runnable command declares a versioned output schema
// (kit/output-schema + kit/output-schema-version) that describes what
// it renders under --format json.
//
// kit mounts these commands into the adopter's tree — status, audit,
// quota, uri, peer, token, serve, alias, spec — so an adopter whose
// conformance suite walks its own tree and requires a schema per leaf
// inherits every gap here as a failure it did not write and cannot
// fix. kit's validator only checks a schema parses when one is
// declared; nothing else stops kit from shipping a command without
// one, so this walk does.

// kitCommandTree builds a Root with every command group kit can
// register mounted: the cli.New options, the exported alias
// factories, and RegisterSpecCommand. All state lives under a temp
// HOME/XDG so no command touches the developer's machine.
func kitCommandTree(t *testing.T) *kitcli.Root {
	t.Helper()
	dir := t.TempDir()
	t.Setenv("HOME", dir)
	t.Setenv("XDG_DATA_HOME", filepath.Join(dir, "data"))
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(dir, "config"))
	t.Setenv("XDG_STATE_HOME", filepath.Join(dir, "state"))
	t.Setenv("XDG_CACHE_HOME", filepath.Join(dir, "cache"))

	r := kitcli.New(kitcli.Config{Name: "kittool", Version: "0.0.1", Short: "kit command tree"},
		kitcli.WithStatus(kitcli.StatusConfig{}),
		kitcli.WithAuditCommand(),
		kitcli.WithQuotaCommand(),
		kitcli.WithURI(kitcli.URIConfig{}),
		kitcli.WithIdentity(kitcli.IdentityConfig{Dir: filepath.Join(dir, "identity")}),
		kitcli.WithPeers(kitcli.PeerConfig{DataDir: filepath.Join(dir, "peers")}),
		kitcli.WithAPI(kitcli.APIConfig{}),
		kitcli.WithAPIKeys(kitcli.APIKeysConfig{Path: filepath.Join(dir, "keys.db")}),
		kitcli.WithSocket(kitcli.SocketConfig{Path: filepath.Join(dir, "kit.sock")}),
	)
	r.Cmd.AddCommand(
		r.AliasCmd(alias.NewStore(filepath.Join(dir, "aliases.yaml"))),
		r.AliasesCmd(),
	)
	if err := speccli.RegisterSpecCommand(r, "1.0"); err != nil {
		t.Fatalf("RegisterSpecCommand: %v", err)
	}
	return r
}

// runnableKitCommands returns every command under root that runs —
// leaves and runnable group nodes alike, hidden or not — the same
// set an adopter's per-command conformance walk visits, minus the
// shell-completion commands (see isShellCompletion).
func runnableKitCommands(root *cobra.Command) []*cobra.Command {
	var out []*cobra.Command
	var walk func(c *cobra.Command)
	walk = func(c *cobra.Command) {
		for _, s := range c.Commands() {
			walk(s)
		}
		if c == root || !c.Runnable() || isShellCompletion(root, c) {
			return
		}
		out = append(out, c)
	}
	walk(root)
	sort.Slice(out, func(i, j int) bool { return out[i].CommandPath() < out[j].CommandPath() })
	return out
}

// isShellCompletion reports the commands exempt from the schema
// requirement by design, not by omission: they emit a shell script
// for the shell to source, never a data document, so there is no
// --format json output for a schema to describe.
//
//   - cobra's generated help, root completion command, its shell
//     children and __complete hooks (cobra adds these, not kit);
//   - kit's `uri completion`, which wraps the same cobra generators.
func isShellCompletion(root, c *cobra.Command) bool {
	switch c.Name() {
	case "help", "__complete", "__completeNoDesc":
		return true
	}
	for p := c; p != nil; p = p.Parent() {
		if p.Name() == "completion" && p.Parent() == root {
			return true
		}
	}
	return c.CommandPath() == root.Name()+" uri completion"
}

// TestKitCommands_DeclareOutputSchema fails when any command kit
// registers lacks a versioned output schema, or declares one that is
// empty or does not compile as JSON Schema.
func TestKitCommands_DeclareOutputSchema(t *testing.T) {
	r := kitCommandTree(t)
	cmds := runnableKitCommands(r.Cmd)
	if len(cmds) == 0 {
		t.Fatal("no runnable commands found; the tree did not mount")
	}
	for _, c := range cmds {
		path := c.CommandPath()
		raw, version, ok := kitcli.GetOutputSchemaJSON(c)
		switch {
		case !ok:
			t.Errorf("%s: no output schema declared (kitcli.SetOutputSchema)", path)
			continue
		case version == "":
			t.Errorf("%s: output schema declared without a version", path)
			continue
		case len(bytes.TrimSpace(raw)) == 0, string(raw) == "null", string(raw) == "{}":
			t.Errorf("%s: output schema is empty: %q", path, raw)
			continue
		}
		compiler := jsonschema.NewCompiler()
		compiler.Draft = jsonschema.Draft2020
		if err := compiler.AddResource("schema.json", bytes.NewReader(raw)); err != nil {
			t.Errorf("%s: output schema does not load: %v", path, err)
			continue
		}
		if _, err := compiler.Compile("schema.json"); err != nil {
			t.Errorf("%s: output schema does not compile: %v", path, err)
		}
	}
}

// TestKitCommands_OutputMatchesSchema runs the commands that can be
// invoked hermetically with --format json and validates what they
// print against the schema they declare, so a schema declared for the
// wrong type fails here rather than in an agent.
//
// setup, when set, seeds state on the tree first; a non-nil return
// replaces args (for argv that depends on the seeded state).
func TestKitCommands_OutputMatchesSchema(t *testing.T) {
	cases := []struct {
		name  string
		args  []string
		setup func(t *testing.T, r *kitcli.Root) []string
	}{
		{name: "spec coverage", args: []string{"spec", "coverage"}},
		{name: "alias", args: []string{"alias"}, setup: seedAlias},
		{name: "alias list", args: []string{"alias", "list"}, setup: seedAlias},
		{name: "aliases", args: []string{"aliases"}},
		{name: "peer list", args: []string{"peer", "list"}, setup: seedPeer},
		{name: "token claims", args: []string{"token", "claims", "--sub", "alice", "--scopes", "read,write"}},
		{name: "token decode", args: []string{"token", "decode", unsignedJWT(t)}},
		{name: "token verify", setup: func(t *testing.T, r *kitcli.Root) []string {
			return []string{"token", "verify", mustRun(t, r, "token", "create", "--sub", "alice")}
		}},
		{name: "token key list", args: []string{"token", "key", "list"}, setup: func(t *testing.T, r *kitcli.Root) []string {
			mustRun(t, r, "token", "key", "create", "--sub", "alice")
			return nil
		}},
		{name: "peer list empty", args: []string{"peer", "list"}},
		{name: "peer trust", args: []string{"peer", "trust", "peer-1"}, setup: seedPeer},
		{name: "peer block", args: []string{"peer", "block", "peer-1"}, setup: seedPeer},
		{name: "peer revoke", args: []string{"peer", "revoke", "peer-1"}, setup: seedPeer},
		{name: "alias add", args: []string{"alias", "add", "st", "status"}},
		{name: "alias delete", args: []string{"alias", "delete", "st"}, setup: seedAlias},
		{name: "token create", args: []string{"token", "create", "--sub", "alice"}},
		{name: "token key create", args: []string{"token", "key", "create", "--sub", "alice"}},
		{name: "token key revoke", setup: func(t *testing.T, r *kitcli.Root) []string {
			key := mustRun(t, r, "token", "key", "create", "--sub", "alice")
			return []string{"token", "key", "revoke", strings.Split(key, "_")[1]}
		}},
		{name: "serve --list", args: []string{"serve", "--list"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			r := kitCommandTree(t)
			// peer list writes to the data stream, not cmd's stdout;
			// route it to whatever writer the invocation installs.
			r.Streams.Data = cmdStdout{r.Cmd}
			args := tc.args
			if tc.setup != nil {
				if seeded := tc.setup(t, r); seeded != nil {
					args = seeded
				}
			}
			harness.AssertJSONSchema(t, r.Cmd, harness.Args(args...))
		})
	}
}

// cmdStdout forwards writes to c's stdout as it is at write time.
type cmdStdout struct{ c *cobra.Command }

func (w cmdStdout) Write(p []byte) (int, error) { return w.c.OutOrStdout().Write(p) }

// mustRun executes argv on r and returns its trimmed stdout.
func mustRun(t *testing.T, r *kitcli.Root, argv ...string) string {
	t.Helper()
	var out, errOut bytes.Buffer
	r.Cmd.SetArgs(argv)
	r.Cmd.SetOut(&out)
	r.Cmd.SetErr(&errOut)
	if err := r.Cmd.Execute(); err != nil {
		t.Fatalf("%v: %v\nstderr: %s", argv, err, errOut.String())
	}
	return strings.TrimSpace(out.String())
}

func seedAlias(t *testing.T, r *kitcli.Root) []string {
	t.Helper()
	mustRun(t, r, "alias", "add", "st", "status")
	return nil
}

func seedPeer(t *testing.T, r *kitcli.Root) []string {
	t.Helper()
	if err := r.PeerRegistry.Add(peer.PeerInfo{
		ID: "peer-1", Name: "one", PublicKey: []byte("key"), Addrs: []string{"127.0.0.1:7000"},
	}); err != nil {
		t.Fatalf("seed peer: %v", err)
	}
	return nil
}

// unsignedJWT returns a structurally valid JWT whose signature is not
// checked by `token decode`.
func unsignedJWT(t *testing.T) string {
	t.Helper()
	enc := base64.RawURLEncoding
	return enc.EncodeToString([]byte(`{"alg":"none"}`)) + "." +
		enc.EncodeToString([]byte(`{"sub":"alice","exp":1}`)) + ".sig"
}
