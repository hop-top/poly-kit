package completion_test

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/spf13/cobra"
	"github.com/spf13/viper"

	"hop.top/kit/go/console/cli"
	"hop.top/kit/go/console/cli/completion"
)

// Runs the shared corpus in contracts/completion-v1. The vectors are
// the source of truth: a failure here means this implementation drifted
// from the contract, not that the vector should be edited to fit.

const completionConfDir = "../../../../contracts/completion-v1"

// completionConfFiles is every vector file this runner consumes; the
// corpus directory must match it exactly.
// updateGoldens rewrites contracts/completion-v1/scripts from cobra (and,
// in completion_templates_test.go, the Rust templates) instead of
// comparing: `make completion-regen` after a cobra bump.
var updateGoldens = flag.Bool("update", false, "rewrite the completion-v1 script goldens and the Rust templates")

var completionConfFiles = map[string]bool{
	"completers.json": true,
	"requests.json":   true,
	"scripts.json":    true,
}

// confSpec is a one-key completer spec.
type confSpec struct {
	Static       *[]confItem     `json:"static"`
	StaticValues *[]string       `json:"static_values"`
	Func         *confFunc       `json:"func"`
	Prefixed     *confPrefixed   `json:"prefixed"`
	ConfigKeys   *map[string]any `json:"config_keys"`
	File         *[]string       `json:"file"`
	Dir          *struct{}       `json:"dir"`
}

type confItem struct {
	Value       string `json:"value"`
	Description string `json:"description,omitempty"`
}

type confFunc struct {
	Items *[]confItem `json:"items"`
	Error *string     `json:"error"`
}

type confPrefixed struct {
	Dimension string   `json:"dimension"`
	Values    confSpec `json:"values"`
}

func (s confSpec) build(t *testing.T) completion.Completer {
	t.Helper()
	var out []completion.Completer
	if s.Static != nil {
		items := make([]completion.Item, len(*s.Static))
		for i, it := range *s.Static {
			items[i] = completion.Item{Value: it.Value, Description: it.Description}
		}
		out = append(out, completion.Static(items...))
	}
	if s.StaticValues != nil {
		out = append(out, completion.StaticValues(*s.StaticValues...))
	}
	if s.Func != nil {
		f := *s.Func
		if (f.Items == nil) == (f.Error == nil) {
			t.Fatal("func spec needs exactly one of items, error")
		}
		out = append(out, completion.Func(func(_ context.Context, _ string) ([]completion.Item, error) {
			if f.Error != nil {
				return nil, errors.New(*f.Error)
			}
			items := make([]completion.Item, len(*f.Items))
			for i, it := range *f.Items {
				items[i] = completion.Item{Value: it.Value, Description: it.Description}
			}
			return items, nil
		}))
	}
	if s.Prefixed != nil {
		out = append(out, completion.Prefixed(s.Prefixed.Dimension, s.Prefixed.Values.build(t)))
	}
	if s.ConfigKeys != nil {
		v := viper.New()
		if err := v.MergeConfigMap(*s.ConfigKeys); err != nil {
			t.Fatal(err)
		}
		out = append(out, completion.ConfigKeys(v))
	}
	if s.File != nil {
		out = append(out, completion.File(*s.File...))
	}
	if s.Dir != nil {
		out = append(out, completion.Dir())
	}
	if len(out) != 1 {
		t.Fatalf("completer spec needs exactly one key, got %d", len(out))
	}
	return out[0]
}

type completersFile struct {
	Schema      string `json:"$schema"`
	ID          string `json:"$id"`
	Version     string `json:"version"`
	Description string `json:"description"`
	Cases       []struct {
		Name      string      `json:"name"`
		Completer confSpec    `json:"completer"`
		Prefix    string      `json:"prefix"`
		Items     *[]confItem `json:"items"`
		Error     bool        `json:"error"`
		Unordered bool        `json:"unordered"`
		Resolved  struct {
			Completions []string `json:"completions"`
			Directive   int      `json:"directive"`
		} `json:"resolved"`
	} `json:"cases"`
}

type confFlag struct {
	Long  string `json:"long"`
	Short string `json:"short"`
	Help  string `json:"help"`
	Value bool   `json:"value"`
}

type confCommand struct {
	Name     string        `json:"name"`
	About    string        `json:"about"`
	Flags    []confFlag    `json:"flags"`
	Commands []confCommand `json:"commands"`
}

// confTree is a kit root built from the corpus.
type confTree struct {
	Program   string            `json:"program"`
	About     string            `json:"about"`
	Commands  []confCommand     `json:"commands"`
	Aliases   map[string]string `json:"aliases"`
	FlagEnums []struct {
		Path   string   `json:"path"`
		Flag   string   `json:"flag"`
		Values []string `json:"values"`
	} `json:"flag_enums"`
	FlagCompleters []struct {
		Path      string   `json:"path"`
		Flag      string   `json:"flag"`
		Completer confSpec `json:"completer"`
	} `json:"flag_completers"`
	ArgCompleters []struct {
		Path      string   `json:"path"`
		Completer confSpec `json:"completer"`
	} `json:"arg_completers"`
}

type requestsFile struct {
	Schema      string              `json:"$schema"`
	ID          string              `json:"$id"`
	Version     string              `json:"version"`
	Description string              `json:"description"`
	Trees       map[string]confTree `json:"trees"`
	Cases       []struct {
		Name           string   `json:"name"`
		Tree           string   `json:"tree"`
		Args           []string `json:"args"`
		NoDesc         bool     `json:"no_desc"`
		Unordered      bool     `json:"unordered"`
		Stdout         []string `json:"stdout"`
		StderrContains string   `json:"stderr_contains"`
	} `json:"cases"`
}

type scriptsFile struct {
	Schema      string `json:"$schema"`
	ID          string `json:"$id"`
	Version     string `json:"version"`
	Description string `json:"description"`
	Cases       []struct {
		Name           string `json:"name"`
		Program        string `json:"program"`
		Shell          string `json:"shell"`
		NoDescriptions bool   `json:"no_descriptions"`
		Golden         string `json:"golden"`
	} `json:"cases"`
}

func loadCompletionConf(t *testing.T, name string, into any) {
	t.Helper()
	dir := filepath.FromSlash(completionConfDir)
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range entries {
		if filepath.Ext(e.Name()) == ".json" && !completionConfFiles[e.Name()] {
			t.Errorf("%s has no runner", e.Name())
		}
	}
	raw, err := os.ReadFile(filepath.Join(dir, name))
	if err != nil {
		t.Fatal(err)
	}
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.DisallowUnknownFields()
	if err := dec.Decode(into); err != nil {
		t.Fatalf("parse %s: %v", name, err)
	}
}

func requireV1(t *testing.T, version string) {
	t.Helper()
	if version != "v1" {
		t.Fatalf("version = %q, want v1", version)
	}
}

// sameLines compares got with want, as a set when unordered.
func sameLines(got, want []string, unordered bool) bool {
	if unordered {
		got, want = slices.Clone(got), slices.Clone(want)
		slices.Sort(got)
		slices.Sort(want)
	}
	return slices.Equal(got, want)
}

func TestConformanceCompletionCompleters(t *testing.T) {
	var f completersFile
	loadCompletionConf(t, "completers.json", &f)
	requireV1(t, f.Version)
	if len(f.Cases) == 0 {
		t.Fatal("cases section is empty")
	}
	for _, c := range f.Cases {
		t.Run(c.Name, func(t *testing.T) {
			comp := c.Completer.build(t)
			items, err := comp.Complete(t.Context(), c.Prefix)
			if c.Error {
				if err == nil {
					t.Errorf("Complete(%q) = %v, want an error", c.Prefix, items)
				}
				if c.Items != nil {
					t.Error("a failing case carries no items")
				}
			} else {
				if err != nil {
					t.Fatalf("Complete(%q): %v", c.Prefix, err)
				}
				if c.Items == nil {
					t.Fatal("a succeeding case needs items")
				}
				got := make([]string, len(items))
				for i, it := range items {
					got[i] = it.Value + "|" + it.Description
				}
				want := make([]string, len(*c.Items))
				for i, it := range *c.Items {
					want[i] = it.Value + "|" + it.Description
				}
				if !sameLines(got, want, c.Unordered) {
					t.Errorf("items = %q, want %q", got, want)
				}
			}

			// The bridge, driven through cobra as a shell would reach it.
			cmd := &cobra.Command{Use: "t"}
			completion.BindArgs(cmd, comp)
			comps, dir := cmd.ValidArgsFunction(cmd, nil, c.Prefix)
			if comps == nil {
				comps = []string{}
			}
			if !sameLines(comps, c.Resolved.Completions, c.Unordered) {
				t.Errorf("resolved = %q, want %q", comps, c.Resolved.Completions)
			}
			if int(dir) != c.Resolved.Directive {
				t.Errorf("directive = %d, want %d", dir, c.Resolved.Directive)
			}
		})
	}
}

func buildConfCommand(t *testing.T, c confCommand) *cobra.Command {
	t.Helper()
	cmd := &cobra.Command{Use: c.Name, Short: c.About}
	if len(c.Commands) == 0 {
		cmd.Run = func(*cobra.Command, []string) {}
	}
	for _, fl := range c.Flags {
		if fl.Value {
			cmd.Flags().StringP(fl.Long, fl.Short, "", fl.Help)
		} else {
			cmd.Flags().BoolP(fl.Long, fl.Short, false, fl.Help)
		}
	}
	for _, child := range c.Commands {
		cmd.AddCommand(buildConfCommand(t, child))
	}
	return cmd
}

func findConfCommand(t *testing.T, root *cobra.Command, path string) *cobra.Command {
	t.Helper()
	cmd, rest, err := root.Find(strings.Fields(path))
	if err != nil || len(rest) != 0 || cmd == root {
		t.Fatalf("no command at path %q", path)
	}
	return cmd
}

func TestConformanceCompletionRequests(t *testing.T) {
	var f requestsFile
	loadCompletionConf(t, "requests.json", &f)
	requireV1(t, f.Version)
	if len(f.Cases) == 0 {
		t.Fatal("cases section is empty")
	}
	if len(f.Trees) == 0 {
		t.Fatal("trees section is empty")
	}
	used := map[string]bool{}
	build := func(t *testing.T, tree confTree) *cli.Root {
		r := cli.New(cli.Config{Name: tree.Program, Version: "1.0.0", Short: tree.About, DisableValidate: true})
		for _, c := range tree.Commands {
			r.Cmd.AddCommand(buildConfCommand(t, c))
		}
		names := make([]string, 0, len(tree.Aliases))
		for n := range tree.Aliases {
			names = append(names, n)
		}
		slices.Sort(names)
		for _, n := range names {
			if err := r.Alias(n, findConfCommand(t, r.Cmd, tree.Aliases[n])); err != nil {
				t.Fatal(err)
			}
		}
		for _, e := range tree.FlagEnums {
			if e.Path == "" {
				r.WithFlagEnum(e.Flag, e.Values...)
			} else {
				r.WithCommandFlagEnum(e.Path, e.Flag, e.Values...)
			}
		}
		for _, fc := range tree.FlagCompleters {
			completion.BindFlag(findConfCommand(t, r.Cmd, fc.Path), fc.Flag, fc.Completer.build(t))
		}
		for _, ac := range tree.ArgCompleters {
			completion.BindArgs(findConfCommand(t, r.Cmd, ac.Path), ac.Completer.build(t))
		}
		return r
	}
	for _, c := range f.Cases {
		t.Run(c.Name, func(t *testing.T) {
			tree, ok := f.Trees[c.Tree]
			if !ok {
				t.Fatalf("no tree %q", c.Tree)
			}
			used[c.Tree] = true
			r := build(t, tree)
			var stdout, stderr bytes.Buffer
			r.Cmd.SetOut(&stdout)
			r.Cmd.SetErr(&stderr)
			req := cobra.ShellCompRequestCmd
			if c.NoDesc {
				req = cobra.ShellCompNoDescRequestCmd
			}
			r.SetArgs(append([]string{req}, c.Args...))
			if err := r.Execute(t.Context()); err != nil {
				t.Fatalf("execute: %v (stderr %q)", err, stderr.String())
			}
			got := strings.Split(strings.TrimSuffix(stdout.String(), "\n"), "\n")
			if len(got) == 0 || len(c.Stdout) == 0 {
				t.Fatalf("stdout = %q", stdout.String())
			}
			last, want := got[len(got)-1], c.Stdout[len(c.Stdout)-1]
			if last != want {
				t.Errorf("directive line = %q, want %q", last, want)
			}
			if !sameLines(got[:len(got)-1], c.Stdout[:len(c.Stdout)-1], c.Unordered) {
				t.Errorf("completions = %q, want %q", got[:len(got)-1], c.Stdout[:len(c.Stdout)-1])
			}
			if c.StderrContains != "" && !strings.Contains(stderr.String(), c.StderrContains) {
				t.Errorf("stderr = %q, want it to contain %q", stderr.String(), c.StderrContains)
			}
		})
	}
	for name := range f.Trees {
		if !used[name] {
			t.Errorf("tree %q has no case", name)
		}
	}
}

func TestConformanceCompletionScripts(t *testing.T) {
	var f scriptsFile
	loadCompletionConf(t, "scripts.json", &f)
	requireV1(t, f.Version)
	if len(f.Cases) == 0 {
		t.Fatal("cases section is empty")
	}
	// Every golden is claimed by a case and every case has a golden.
	goldens := map[string]bool{}
	entries, err := os.ReadDir(filepath.Join(filepath.FromSlash(completionConfDir), "scripts"))
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range entries {
		if e.Name() != "README.md" {
			goldens["scripts/"+e.Name()] = false
		}
	}
	for _, c := range f.Cases {
		t.Run(c.Name, func(t *testing.T) {
			if _, ok := goldens[c.Golden]; !ok && !*updateGoldens {
				t.Fatalf("golden %s is missing", c.Golden)
			}
			goldens[c.Golden] = true
			want, err := os.ReadFile(filepath.Join(filepath.FromSlash(completionConfDir), c.Golden))
			if err != nil && !*updateGoldens {
				t.Fatal(err)
			}
			r := cli.New(cli.Config{Name: c.Program, Version: "1.0.0", Short: "Kit app", DisableValidate: true})
			r.Cmd.AddCommand(&cobra.Command{Use: "sub", Short: "A subcommand", Run: func(*cobra.Command, []string) {}})
			var stdout bytes.Buffer
			r.Cmd.SetOut(&stdout)
			args := []string{"completion", c.Shell}
			if c.NoDescriptions {
				args = append(args, "--no-descriptions")
			}
			r.SetArgs(args)
			if err := r.Execute(t.Context()); err != nil {
				t.Fatal(err)
			}
			if *updateGoldens {
				if err := os.WriteFile(filepath.Join(filepath.FromSlash(completionConfDir), c.Golden), stdout.Bytes(), 0o644); err != nil {
					t.Fatal(err)
				}
				return
			}
			if got := stdout.String(); got != string(want) {
				t.Errorf("script differs from %s (%d bytes, want %d)%s",
					c.Golden, len(got), len(want), firstDiff(got, string(want)))
			}
		})
	}
	for g, used := range goldens {
		if !used && !*updateGoldens {
			t.Errorf("%s has no case", g)
		}
	}
}

func firstDiff(got, want string) string {
	gl, wl := strings.Split(got, "\n"), strings.Split(want, "\n")
	for i := 0; i < len(gl) && i < len(wl); i++ {
		if gl[i] != wl[i] {
			return fmt.Sprintf("; line %d: %q, want %q", i+1, gl[i], wl[i])
		}
	}
	return ""
}
