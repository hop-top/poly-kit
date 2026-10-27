package cli_test

import (
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/spf13/cobra"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"hop.top/kit/go/console/cli"
	"hop.top/kit/go/console/cli/policy"
)

// recorder is a stub plane implementing every hook, recording calls.
type recorder struct {
	name    string
	log     *[]string
	initErr error
	mount   string // command OptionsApplied mounts, if any
	exempt  *cobra.Command
	all     bool // with owns: exempt every command
	owns    bool
}

func (p *recorder) OptionsApplied(r *cli.Root) {
	*p.log = append(*p.log, p.name+":options")
	if p.mount != "" {
		r.Cmd.AddCommand(&cobra.Command{Use: p.mount, RunE: func(*cobra.Command, []string) error { return nil }})
	}
}

func (p *recorder) Init(*cli.Root) error {
	*p.log = append(*p.log, p.name+":init")
	return p.initErr
}

func (p *recorder) PrepareTree(*cli.Root) { *p.log = append(*p.log, p.name+":prepare") }

func (p *recorder) ExemptFromPolicy(_ *cli.Root, cmd *cobra.Command) (bool, bool) {
	*p.log = append(*p.log, p.name+":exempt?")
	if !p.owns {
		return false, false
	}
	return p.all || cmd == p.exempt, true
}

var (
	keyA = cli.NewExtensionKey[*recorder]("a")
	keyB = cli.NewExtensionKey[*recorder]("b")
	keyC = cli.NewExtensionKey[*recorder]("c")
)

func withRecorder(key *cli.ExtensionKey[*recorder], p *recorder) func(*cli.Root) {
	return func(r *cli.Root) {
		cli.Extend(r, key, func(*cli.Root) *recorder { return p })
	}
}

func extConfig(name string) cli.Config {
	return cli.Config{Name: name, Version: "0.0.0", Short: "ext", DisableValidate: true}
}

func TestExtend_AttachesOnceAndKeysByIdentity(t *testing.T) {
	r := &cli.Root{Cmd: &cobra.Command{Use: "bare"}}
	_, ok := cli.Extension(r, keyA)
	assert.False(t, ok, "a bare Root has no extension")

	calls := 0
	mk := func(*cli.Root) *recorder { calls++; return &recorder{name: "x"} }
	first := cli.Extend(r, keyA, mk)
	second := cli.Extend(r, keyA, mk)
	assert.Same(t, first, second, "Extend returns the attached value")
	assert.Equal(t, 1, calls, "create runs once per key")

	// Another key with the same name and type is another extension.
	twin := cli.NewExtensionKey[*recorder]("a")
	_, ok = cli.Extension(r, twin)
	assert.False(t, ok, "keys are told apart by address, not name")
	assert.Equal(t, "a", twin.String())

	got, ok := cli.Extension(r, keyA)
	require.True(t, ok)
	assert.Same(t, first, got)

	_, ok = cli.Extension[*recorder](nil, keyA)
	assert.False(t, ok, "a nil Root has none")
}

func TestExtension_HooksRunInAttachOrder(t *testing.T) {
	var log []string
	r := cli.New(extConfig("order"),
		withRecorder(keyB, &recorder{name: "b", log: &log}),
		withRecorder(keyA, &recorder{name: "a", log: &log}))
	assert.Equal(t, []string{"b:options", "a:options", "b:init", "a:init"}, log)

	log = nil
	require.NoError(t, r.Prepare())
	assert.Equal(t, []string{"b:prepare", "a:prepare"}, log)
}

// What OptionsApplied mounts is mounted before New snapshots the
// reserved subcommands, so it is kit-reserved like a With* option's.
func TestExtension_OptionsHookMountsBeforeTheReservedSnapshot(t *testing.T) {
	var log []string
	r := cli.New(extConfig("reserve"), withRecorder(keyA, &recorder{name: "a", log: &log, mount: "plane"}))
	assert.True(t, r.IsReserved("plane"), "a command the hook mounts is reserved")
}

// An extension attached after New returned gets no OptionsApplied,
// but Prepare still runs its PrepareTree.
func TestExtension_LateAttachSkipsOptionsHook(t *testing.T) {
	var log []string
	r := cli.New(extConfig("late"))
	withRecorder(keyA, &recorder{name: "a", log: &log})(r)
	assert.Empty(t, log)
	require.NoError(t, r.Prepare())
	assert.Equal(t, []string{"a:prepare"}, log)
}

// An InitHook error does not fail New: PersistentPreRunE returns it,
// after the chdir hook ran, so --help and completion keep working.
func TestExtension_InitErrorDeferredAfterChdir(t *testing.T) {
	dir := t.TempDir()
	orig, err := os.Getwd()
	require.NoError(t, err)
	t.Cleanup(func() { _ = os.Chdir(orig) })

	var log []string
	boom := errors.New("plane: not configured")
	r := cli.New(extConfig("initerr"), withRecorder(keyA, &recorder{name: "a", log: &log, initErr: boom}))
	require.NotNil(t, r.Cmd.PersistentPreRunE, "the chain is installed despite the init failure")

	require.NoError(t, r.Cmd.ParseFlags([]string{"--chdir", dir}))
	err = r.Cmd.PersistentPreRunE(r.Cmd, nil)

	cwd, cwdErr := os.Getwd()
	require.NoError(t, cwdErr)
	wantReal, _ := filepath.EvalSymlinks(dir)
	gotReal, _ := filepath.EvalSymlinks(cwd)
	assert.Equal(t, wantReal, gotReal, "chdir runs before the init error short-circuits the chain")
	require.ErrorIs(t, err, boom, "the init error surfaces")
}

// The first extension that owns the decision answers whether --policy
// exempts a command; without an owner, a depth-1 `serve` is exempt by
// name. The exemption is observable under a policy that refuses every
// destructive command: an exempt one runs, any other is refused.
func TestExtension_PolicyExemptionFirstOwnerDecides(t *testing.T) {
	denyDestructive := policy.Policy{
		Name:  "deny",
		Allow: map[policy.SideEffect][]string{policy.SideEffectDestructive: {}},
	}
	run := func(t *testing.T, opts []func(*cli.Root), name string) error {
		t.Helper()
		opts = append(opts, cli.WithPolicy(func(string) (policy.Policy, error) { return denyDestructive, nil }))
		r := cli.New(extConfig("policy"), opts...)
		leaf := &cobra.Command{Use: name, Short: name, RunE: func(*cobra.Command, []string) error { return nil }}
		cli.SetSideEffect(leaf, cli.SideEffectDestructive)
		r.Cmd.AddCommand(leaf)
		r.Cmd.SetOut(io.Discard)
		r.Cmd.SetErr(io.Discard)
		r.SetArgs([]string{"--policy", "deny", "--confirm=yes", name})
		return r.Execute(t.Context())
	}

	t.Run("no owner: depth-1 serve is exempt by name", func(t *testing.T) {
		assert.NoError(t, run(t, nil, "serve"))
		assert.Error(t, run(t, nil, "wipe"), "the policy refuses a command it does not exempt")
	})

	t.Run("an owner overrides the by-name default", func(t *testing.T) {
		var log []string
		opts := []func(*cli.Root){
			withRecorder(keyA, &recorder{name: "a", log: &log}),             // defers
			withRecorder(keyB, &recorder{name: "b", log: &log, owns: true}), // owns: exempts nothing
			withRecorder(keyC, &recorder{name: "c", log: &log, owns: true, all: true}),
		}
		assert.Error(t, run(t, opts, "serve"), "the first owner said not exempt")
		assert.Equal(t, []string{"a:exempt?", "b:exempt?"}, filter(log, "exempt?"),
			"asked in attach order, and no one after the first owner")
	})
}

func filter(log []string, suffix string) []string {
	var out []string
	for _, l := range log {
		if strings.HasSuffix(l, suffix) {
			out = append(out, l)
		}
	}
	return out
}
