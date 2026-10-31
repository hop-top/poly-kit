package cli_test

import (
	"bytes"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"slices"
	"sort"
	"strings"
	"testing"

	"github.com/spf13/cobra"
	"github.com/spf13/pflag"
	"hop.top/kit/go/console/cli"
	"hop.top/kit/go/console/output"
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
	SideEffect  string            `json:"side_effect"`
	Flags       []rootFlag        `json:"flags"`
	Args        []string          `json:"args"`
	Commands    []rootCommand     `json:"commands"`
	Annotations map[string]string `json:"annotations"`
}

type rootTree struct {
	Program  string            `json:"program"`
	Version  string            `json:"version"`
	About    string            `json:"about"`
	Commands []rootCommand     `json:"commands"`
	Aliases  map[string]string `json:"aliases"`
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
	cmd := &cobra.Command{Use: c.Name, Short: c.About}
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
			for k, v := range c.Env {
				t.Setenv(k, v)
			}
			r := cli.New(cli.Config{Name: tree.Program, Version: tree.Version, Short: tree.About, DisableValidate: true})
			for _, cc := range tree.Commands {
				r.Cmd.AddCommand(buildRootCommand(cc, r, ""))
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
			r.SetArgs(slices.Clone(c.Args))
			runErr := r.Execute(t.Context())
			exit := 0
			if runErr != nil {
				exit = 1
				var oe *output.Error
				if errors.As(runErr, &oe) {
					exit = oe.ExitCode
				}
			}
			if record {
				t.Logf("RECORD %s exit=%d\nSTDOUT:\n%s\nSTDERR:\n%s", c.Name, exit, stdout.String(), stderr.String())
				return
			}
			if exit != c.Exit {
				t.Errorf("exit = %d, want %d (stderr %q)", exit, c.Exit, stderr.String())
			}
			if c.Stdout != nil && stdout.String() != *c.Stdout {
				t.Errorf("stdout = %q, want %q", stdout.String(), *c.Stdout)
			}
			for _, s := range c.StdoutContains {
				if !strings.Contains(stdout.String(), s) {
					t.Errorf("stdout %q lacks %q", stdout.String(), s)
				}
			}
			for _, s := range c.StdoutOmits {
				if strings.Contains(stdout.String(), s) {
					t.Errorf("stdout %q has %q", stdout.String(), s)
				}
			}
			for _, s := range c.StderrContains {
				if !strings.Contains(stderr.String(), s) {
					t.Errorf("stderr %q lacks %q", stderr.String(), s)
				}
			}
			if c.StderrJSON != nil {
				var got map[string]any
				if err := json.Unmarshal(stderr.Bytes(), &got); err != nil {
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
