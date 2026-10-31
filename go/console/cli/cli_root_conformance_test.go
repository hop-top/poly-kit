package cli_test

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"slices"
	"sort"
	"strings"
	"testing"

	"github.com/spf13/cobra"
	"github.com/spf13/pflag"
	"hop.top/kit/go/console/alias"
	"hop.top/kit/go/console/cli"
	kitcliconfig "hop.top/kit/go/console/cli/config"
	"hop.top/kit/go/console/output"
	"hop.top/kit/go/runtime/sideeffect"
)

// Runs the shared corpus in contracts/cli-root-v1. The vectors are the
// source of truth; Go is canonical. A failure means the implementation
// drifted, not that a vector should be edited to fit.

const cliRootDir = "../../../contracts/cli-root-v1"

type rootFlag struct {
	Long    string   `json:"long"`
	Short   string   `json:"short"`
	Help    string   `json:"help"`
	Type    string   `json:"type"`
	Enum    []string `json:"enum"`
	Default string   `json:"default"`
}

type rootCommand struct {
	Name        string            `json:"name"`
	About       string            `json:"about"`
	Long        string            `json:"long"`
	Kind        string            `json:"kind"`
	DryRun      string            `json:"dry_run"`
	SideEffect  string            `json:"side_effect"`
	Flags       []rootFlag        `json:"flags"`
	Args        []string          `json:"args"`
	Commands    []rootCommand     `json:"commands"`
	Annotations map[string]string `json:"annotations"`
}

type rootValidator struct {
	Flag    string   `json:"flag"`
	Reject  []string `json:"reject"`
	Code    string   `json:"code"`
	Message string   `json:"message"`
	Exit    int      `json:"exit"`
}

type rootChain struct {
	Path   string `json:"path"`
	Source string `json:"source"`
	Scope  string `json:"scope"`
}

type rootTree struct {
	Program  string            `json:"program"`
	Version  string            `json:"version"`
	About    string            `json:"about"`
	Commands []rootCommand     `json:"commands"`
	Aliases  map[string]string `json:"aliases"`
	// Fixtures are files (or, with a trailing slash, directories)
	// created under the case's temp dir, which is also the working
	// directory. {tmp} in any string below names that dir.
	Fixtures map[string]string `json:"fixtures"`
	// Validate turns the pre-flight Root.Validate on; the failure comes
	// back as the error (Exit mode would end the test process).
	Validate bool `json:"validate"`
	// Signature is the signature-validator strictness (warn|reject).
	Signature string `json:"signature"`
	// Policy wires the default named-policy loader.
	Policy bool `json:"policy"`
	// ChdirResolver maps a -C target to a fixture dir.
	ChdirResolver  map[string]string `json:"chdir_resolver"`
	ProjectMarker  string            `json:"project_marker"`
	FlagValidators []rootValidator   `json:"flag_validators"`
	// AliasStore is the fixture file the alias commands are backed by.
	AliasStore string `json:"alias_store"`
	// ConfigChain mounts `config path|paths` over this chain.
	ConfigChain []rootChain `json:"config_chain"`
}

type rootCase struct {
	Name           string            `json:"name"`
	Tree           string            `json:"tree"`
	Args           []string          `json:"args"`
	Env            map[string]string `json:"env"`
	Exit           int               `json:"exit"`
	Stdout         *string           `json:"stdout"`
	StdoutContains []string          `json:"stdout_contains"`
	StdoutOmits    []string          `json:"stdout_omits"`
	StderrContains []string          `json:"stderr_contains"`
	StderrJSON     map[string]any    `json:"stderr_json"`
	// PromptAnswer is what the confirm prompt reads; absent means no
	// terminal to ask on.
	PromptAnswer *string `json:"prompt_answer"`
}

type rootFile struct {
	Schema      string              `json:"$schema"`
	ID          string              `json:"$id"`
	Version     string              `json:"version"`
	Description string              `json:"description"`
	Trees       map[string]rootTree `json:"trees"`
	Cases       []rootCase          `json:"cases"`
}

// build registers c under parent. Every runnable command echoes what it
// was invoked with, so the vectors pin parsing, not behavior.
func buildRootCommand(c rootCommand, root *cli.Root, prefix string) *cobra.Command {
	path := strings.TrimSpace(prefix + " " + c.Name)
	cmd := &cobra.Command{Use: c.Name, Short: c.About, Long: c.Long}
	for k, v := range c.Annotations {
		if cmd.Annotations == nil {
			cmd.Annotations = map[string]string{}
		}
		cmd.Annotations[k] = v
	}
	if c.DryRun == "opted-out" {
		cli.OptOutDryRun(cmd)
	}
	for _, f := range c.Flags {
		fs := cmd.Flags()
		switch f.Type {
		case "bool":
			fs.BoolP(f.Long, f.Short, false, f.Help)
		case "int":
			fs.IntP(f.Long, f.Short, 0, f.Help)
		default:
			fs.StringP(f.Long, f.Short, f.Default, f.Help)
		}
		if len(f.Enum) > 0 {
			root.WithCommandFlagEnum(path, f.Long, f.Enum...)
		}
	}
	if c.SideEffect != "" {
		cli.SetSideEffect(cmd, cli.SideEffect(c.SideEffect))
	}
	if len(c.Commands) == 0 {
		cmd.Args = cobra.ArbitraryArgs
		cmd.RunE = func(cmd *cobra.Command, args []string) error {
			switch c.Kind {
			case "pwd":
				wd, _ := os.Getwd()
				out, _ := json.Marshal(map[string]any{"cwd": filepath.Base(wd)})
				_, _ = cmd.OutOrStdout().Write(append(out, '\n'))
				return nil
			case "dryrun":
				out, _ := json.Marshal(map[string]any{
					"path": path, "dry_run": sideeffect.IsDryRun(cmd.Context()),
				})
				_, _ = cmd.OutOrStdout().Write(append(out, '\n'))
				return nil
			case "config":
				paths, overrides, err := root.ConfigArgs()
				var out []byte
				if err != nil {
					out, _ = json.Marshal(map[string]any{"error": err.Error()})
				} else {
					if paths == nil {
						paths = []string{}
					}
					if overrides == nil {
						overrides = map[string]any{}
					}
					out, _ = json.Marshal(map[string]any{"paths": paths, "overrides": overrides})
				}
				_, _ = cmd.OutOrStdout().Write(append(out, '\n'))
				return nil
			}
			set := map[string]string{}
			cmd.Flags().Visit(func(f *pflag.Flag) {
				if root.Cmd.PersistentFlags().Lookup(f.Name) == nil {
					set[f.Name] = f.Value.String()
				}
			})
			globals := map[string]any{}
			for _, n := range []string{"quiet", "no-color", "no-hints", "offline", "format"} {
				if f := cmd.Flags().Lookup(n); f != nil {
					globals[n] = f.Value.String()
				}
			}
			if n, err := cmd.Flags().GetCount("verbose"); err == nil {
				globals["verbose"] = n
			}
			if args == nil {
				args = []string{}
			}
			out, _ := json.Marshal(map[string]any{
				"path":  path,
				"flags": set, "args": args, "globals": globals,
			})
			_, _ = cmd.OutOrStdout().Write(append(out, '\n'))
			return nil
		}
	}
	for _, sub := range c.Commands {
		cmd.AddCommand(buildRootCommand(sub, root, path))
	}
	return cmd
}

func findRootCommand(t *testing.T, root *cobra.Command, path string) *cobra.Command {
	t.Helper()
	cur := root
	for _, w := range strings.Fields(path) {
		var next *cobra.Command
		for _, c := range cur.Commands() {
			if c.Name() == w {
				next = c
			}
		}
		if next == nil {
			t.Fatalf("no command %q", path)
		}
		cur = next
	}
	return cur
}

func TestConformanceCLIRoot(t *testing.T) {
	raw, err := os.ReadFile(filepath.Join(filepath.FromSlash(cliRootDir), "vectors.json"))
	if err != nil {
		t.Fatal(err)
	}
	var f rootFile
	if err := json.Unmarshal(raw, &f); err != nil {
		t.Fatal(err)
	}
	if f.Version != "v1" {
		t.Fatalf("version = %q", f.Version)
	}
	used := map[string]bool{}
	record := os.Getenv("CLIROOT_RECORD") != ""
	for _, c := range f.Cases {
		t.Run(c.Name, func(t *testing.T) {
			tree := f.Trees[c.Tree]
			used[c.Tree] = true
			tmp := t.TempDir()
			sub := func(s string) string { return strings.ReplaceAll(s, "{tmp}", tmp) }
			for name, body := range tree.Fixtures {
				p := filepath.Join(tmp, filepath.FromSlash(name))
				if strings.HasSuffix(name, "/") {
					if err := os.MkdirAll(p, 0o755); err != nil {
						t.Fatal(err)
					}
					continue
				}
				if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(p, []byte(sub(body)), 0o644); err != nil {
					t.Fatal(err)
				}
			}
			t.Chdir(tmp)
			t.Setenv("XDG_CONFIG_HOME", filepath.Join(tmp, "xdg"))
			for k, v := range c.Env {
				t.Setenv(k, sub(v))
			}
			cfg := cli.Config{
				Name: tree.Program, Version: tree.Version, Short: tree.About,
				DisableValidate: !tree.Validate, ProjectMarker: tree.ProjectMarker,
				QuietBootWarnings: true,
			}
			if tree.Validate {
				cfg.ValidationFailureMode = cli.ValidationFailureError
			}
			if tree.Signature != "" {
				cfg.SignatureStrictness = cli.SignatureStrictness(tree.Signature)
				cfg.ValidationFailureMode = cli.ValidationFailureError
			}
			if len(tree.ChdirResolver) > 0 {
				cfg.ChdirResolver = func(target string) (string, error) {
					if rel, ok := tree.ChdirResolver[target]; ok {
						return filepath.Join(tmp, rel), nil
					}
					return "", fmt.Errorf("unknown target %q", target)
				}
			}
			var promptSrc cli.PromptSource = func() *cli.PromptTTY { return nil }
			if c.PromptAnswer != nil {
				promptSrc = cli.PromptSourceFromReadWriter(strings.NewReader(*c.PromptAnswer+"\n"), io.Discard)
			}
			opts := []func(*cli.Root){cli.WithPromptSource(promptSrc)}
			if tree.Policy {
				opts = append(opts, cli.WithPolicy(cli.DefaultPolicyLoader(tree.Program)))
			}
			r := cli.New(cfg, opts...)
			for _, v := range tree.FlagValidators {
				r.WithFlagValidator(v.Flag, func(val string) *output.Error {
					if slices.Contains(v.Reject, val) {
						return &output.Error{Code: v.Code, Message: v.Message, ExitCode: v.Exit}
					}
					return nil
				})
			}
			for _, cc := range tree.Commands {
				r.Cmd.AddCommand(buildRootCommand(cc, r, ""))
			}
			if tree.AliasStore != "" {
				store := alias.NewStore(filepath.Join(tmp, tree.AliasStore))
				if err := store.Load(); err != nil {
					t.Fatal(err)
				}
				r.Cmd.AddCommand(r.AliasCmd(store))
				r.Cmd.AddCommand(r.AliasesCmd())
				if err := r.LoadAliasStore(store); err != nil {
					t.Fatal(err)
				}
			}
			if len(tree.ConfigChain) > 0 {
				chain := func(string) []kitcliconfig.ResolvedPath {
					var out []kitcliconfig.ResolvedPath
					for _, e := range tree.ConfigChain {
						p := sub(e.Path)
						_, err := os.Stat(p)
						out = append(out, kitcliconfig.ResolvedPath{Path: p, Source: e.Source, Scope: e.Scope, Exists: err == nil})
					}
					return out
				}
				r.Cmd.AddCommand(kitcliconfig.Command(tree.Program, kitcliconfig.WithResolver(chain)))
			}
			names := make([]string, 0, len(tree.Aliases))
			for n := range tree.Aliases {
				names = append(names, n)
			}
			sort.Strings(names)
			for _, n := range names {
				if err := r.Alias(n, findRootCommand(t, r.Cmd, tree.Aliases[n])); err != nil {
					t.Fatal(err)
				}
			}
			var stdout, stderr bytes.Buffer
			r.Cmd.SetOut(&stdout)
			r.Cmd.SetErr(&stderr)
			args := slices.Clone(c.Args)
			for i := range args {
				args[i] = sub(args[i])
			}
			r.SetArgs(args)
			runErr := r.Execute(t.Context())
			exit := 0
			if runErr != nil {
				exit = 1
				var oe *output.Error
				var ce interface{ AsCLIError() *output.Error }
				var sre *cli.SignatureReportError
				switch {
				case errors.As(runErr, &sre):
					exit = 2
					stderr.WriteString(runErr.Error() + "\n")
				case errors.As(runErr, &oe):
					exit = oe.ExitCode
				case errors.As(runErr, &ce):
					// A pre-flight failure comes back unrendered: show
					// what Exit mode would have written.
					exit = ce.AsCLIError().ExitCode
					stderr.WriteString(runErr.Error() + "\n")
				}
			}
			if record {
				t.Logf("RECORD %s exit=%d\nSTDOUT:\n%s\nSTDERR:\n%s", c.Name, exit, stdout.String(), stderr.String())
				return
			}
			if exit != c.Exit {
				t.Errorf("exit = %d, want %d (stderr %q)", exit, c.Exit, stderr.String())
			}
			if c.Stdout != nil && stdout.String() != sub(*c.Stdout) {
				t.Errorf("stdout = %q, want %q", stdout.String(), sub(*c.Stdout))
			}
			for _, s := range c.StdoutContains {
				if !strings.Contains(stdout.String(), sub(s)) {
					t.Errorf("stdout %q lacks %q", stdout.String(), sub(s))
				}
			}
			for _, s := range c.StdoutOmits {
				if strings.Contains(stdout.String(), s) {
					t.Errorf("stdout %q has %q", stdout.String(), s)
				}
			}
			for _, s := range c.StderrContains {
				if !strings.Contains(stderr.String(), sub(s)) {
					t.Errorf("stderr %q lacks %q", stderr.String(), sub(s))
				}
			}
			if c.StderrJSON != nil {
				// The first JSON value: a renderer may add a styled line
				// after the envelope.
				var got map[string]any
				if err := json.NewDecoder(bytes.NewReader(stderr.Bytes())).Decode(&got); err != nil {
					t.Fatalf("stderr is not JSON: %v: %q", err, stderr.String())
				}
				for k, want := range c.StderrJSON {
					gj, _ := json.Marshal(got[k])
					wj, _ := json.Marshal(want)
					if string(gj) != string(wj) {
						t.Errorf("stderr.%s = %s, want %s", k, gj, wj)
					}
				}
			}
		})
	}
	for name := range f.Trees {
		if !used[name] {
			t.Errorf("tree %q has no case", name)
		}
	}
}
