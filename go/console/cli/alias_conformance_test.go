package cli_test

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/spf13/cobra"
	"hop.top/kit/go/console/cli"
)

// Runs contracts/alias-v1/bridge.json against Root.Alias. The vectors
// are the source of truth: a failure means the bridge drifted from the
// contract, not that a vector should be edited to fit. validate.json
// and cycles.json run in go/console/alias.

const aliasBridgeConf = "../../../contracts/alias-v1/bridge.json"

type aliasConfNode struct {
	Name        string          `json:"name"`
	Description string          `json:"description"`
	Flags       []aliasConfFlag `json:"flags"`
	Commands    []aliasConfNode `json:"commands"`
}

type aliasConfFlag struct {
	Name    string `json:"name"`
	Default string `json:"default"`
}

type aliasConfEntry struct {
	Kind        string  `json:"kind"`
	Hidden      *bool   `json:"hidden"`
	Description *string `json:"description"`
}

type aliasConfRun struct {
	Ran    string   `json:"ran"`
	Path   string   `json:"path"`
	Env    *string  `json:"env"`
	Args   []string `json:"args"`
	Name   string   `json:"name"`
	Target string   `json:"target"`
}

type aliasConfCheck struct {
	Alias  string                    `json:"alias"`
	Argv   []string                  `json:"argv"`
	Entry  *aliasConfEntry           `json:"entry"`
	Run    *aliasConfRun             `json:"run"`
	Help   string                    `json:"help"`
	ByPort map[string]aliasConfCheck `json:"by_port"`
}

// forPort applies the check's by_port override for port.
func (c aliasConfCheck) forPort(port string) aliasConfCheck {
	o, ok := c.ByPort[port]
	if !ok {
		return c
	}
	if o.Entry != nil {
		c.Entry = o.Entry
	}
	if o.Run != nil {
		c.Run = o.Run
	}
	if o.Help != "" {
		c.Help = o.Help
	}
	return c
}

type aliasConfOptions struct {
	Hidden   *bool  `json:"hidden"`
	Describe bool   `json:"describe"`
	Help     string `json:"help"`
	Dispatch string `json:"dispatch"`
}

type aliasConfBridge struct {
	Version          string        `json:"version"`
	Description      string        `json:"description"`
	Tree             aliasConfNode `json:"tree"`
	DescribeTemplate string        `json:"describe_template"`
	Cases            []struct {
		Name    string            `json:"name"`
		Aliases map[string]string `json:"aliases"`
		Options *aliasConfOptions `json:"options"`
		Skip    map[string]string `json:"skip"`
		Checks  []aliasConfCheck  `json:"checks"`
	} `json:"cases"`
}

// aliasConfRecord is what a target or handler saw.
type aliasConfRecord struct {
	ran          string
	path, env    string
	hasEnv       bool
	args         []string
	name, target string
}

func buildAliasConfTree(parent *cobra.Command, nodes []aliasConfNode, prefix string, rec *aliasConfRecord) map[string]string {
	descs := map[string]string{}
	for _, n := range nodes {
		path := strings.TrimSpace(prefix + " " + n.Name)
		c := &cobra.Command{Use: n.Name, Short: n.Description}
		descs[path] = n.Description
		for _, f := range n.Flags {
			c.Flags().String(f.Name, f.Default, "")
		}
		if len(n.Commands) == 0 {
			c.Args = cobra.ArbitraryArgs
			c.RunE = func(cmd *cobra.Command, args []string) error {
				*rec = aliasConfRecord{ran: "target", path: path, args: append([]string{}, args...)}
				if f := cmd.Flags().Lookup("env"); f != nil {
					rec.env, rec.hasEnv = f.Value.String(), true
				}
				return nil
			}
		}
		parent.AddCommand(c)
		sub := buildAliasConfTree(c, n.Commands, path, rec)
		for k, v := range sub {
			descs[k] = v
		}
	}
	return descs
}

func TestConformanceAliasBridge(t *testing.T) {
	raw, err := os.ReadFile(filepath.FromSlash(aliasBridgeConf))
	if err != nil {
		t.Fatal(err)
	}
	var f aliasConfBridge
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&f); err != nil {
		t.Fatal(err)
	}
	if f.Version != "v1" {
		t.Fatalf("version %q, want v1", f.Version)
	}
	if len(f.Cases) == 0 {
		t.Fatal("no cases")
	}

	for _, tc := range f.Cases {
		t.Run(tc.Name, func(t *testing.T) {
			if why, ok := tc.Skip["go"]; ok {
				t.Skip(why)
			}
			setup := func(t *testing.T) (*cli.Root, *aliasConfRecord, map[string]string, *bytes.Buffer) {
				r := cli.New(cli.Config{Name: f.Tree.Name, Version: "0.0.1", Short: "t", DisableValidate: true})
				out := &bytes.Buffer{}
				r.Cmd.SetOut(out)
				r.Cmd.SetErr(out)
				rec := &aliasConfRecord{}
				descs := buildAliasConfTree(r.Cmd, f.Tree.Commands, "", rec)

				var opts []cli.AliasOption
				if o := tc.Options; o != nil {
					if o.Hidden != nil {
						opts = append(opts, cli.WithAliasHidden(*o.Hidden))
					}
					if o.Describe {
						tmpl := f.DescribeTemplate
						opts = append(opts, cli.WithAliasDescribe(func(name, target string) string {
							return strings.NewReplacer("{name}", name, "{target}", target).Replace(tmpl)
						}))
					}
					switch o.Help {
					case "own":
						opts = append(opts, cli.WithAliasHelp(cli.AliasHelpOwn))
					case "passthrough":
						opts = append(opts, cli.WithAliasHelp(cli.AliasHelpPassthrough))
					case "":
					default:
						t.Fatalf("unknown help option %q", o.Help)
					}
					switch o.Dispatch {
					case "handler":
						opts = append(opts, cli.WithAliasDispatch(func(_ *cobra.Command, name, target string, args []string) error {
							*rec = aliasConfRecord{ran: "handler", name: name, target: target, args: append([]string{}, args...)}
							return nil
						}))
					case "register-only":
						opts = append(opts, cli.WithAliasRegisterOnly())
					case "":
					default:
						t.Fatalf("unknown dispatch option %q", o.Dispatch)
					}
					if len(opts) == 0 {
						t.Fatal("options set but none mapped")
					}
				}
				r.Viper.Set("aliases", tc.Aliases)
				if err := r.LoadAliases(opts...); err != nil {
					t.Fatal(err)
				}
				return r, rec, descs, out
			}

			for _, raw := range tc.Checks {
				c := raw.forPort("go")
				r, rec, descs, out := setup(t)
				switch {
				case c.Entry != nil:
					checkAliasConfEntry(t, r, tc.Aliases, c.Alias, *c.Entry)
				case c.Run != nil:
					r.Cmd.SetArgs(c.Argv)
					if err := r.Execute(t.Context()); err != nil {
						t.Errorf("%v: %v (output %q)", c.Argv, err, out.String())
						continue
					}
					checkAliasConfRun(t, c.Argv, *rec, *c.Run)
				case c.Help != "":
					r.Cmd.SetArgs(c.Argv)
					if err := r.Execute(t.Context()); err != nil {
						t.Errorf("%v: %v", c.Argv, err)
						continue
					}
					alias := c.Argv[0]
					targetDesc := descs[tc.Aliases[alias]]
					got := "unknown"
					switch {
					case strings.Contains(out.String(), targetDesc):
						got = "target"
					case aliasConfOwnHelp(r, alias, out.String()):
						got = "alias"
					}
					if got != c.Help {
						t.Errorf("%v: help is the %s's, want the %s's; output:\n%s", c.Argv, got, c.Help, out.String())
					}
					if rec.ran != "" {
						t.Errorf("%v: help ran %s", c.Argv, rec.ran)
					}
				default:
					t.Fatalf("check has no observable: %+v", c)
				}
			}
		})
	}
}

// aliasConfOwnHelp reports whether out is the alias proxy's own help.
func aliasConfOwnHelp(r *cli.Root, alias, out string) bool {
	for _, c := range r.Cmd.Commands() {
		if c.Name() == alias {
			return strings.Contains(out, c.Short)
		}
	}
	return false
}

func checkAliasConfEntry(t *testing.T, r *cli.Root, aliases map[string]string, alias string, want aliasConfEntry) {
	t.Helper()
	for _, c := range r.Cmd.Commands() {
		if c.Name() != alias {
			continue
		}
		if want.Kind != "proxy" {
			t.Errorf("%s: proxy, want %s", alias, want.Kind)
			return
		}
		if want.Hidden != nil && c.Hidden != *want.Hidden {
			t.Errorf("%s: hidden %v, want %v", alias, c.Hidden, *want.Hidden)
		}
		if want.Description != nil && c.Short != *want.Description {
			t.Errorf("%s: description %q, want %q", alias, c.Short, *want.Description)
		}
		return
	}
	target, _, err := r.Cmd.Find(strings.Fields(aliases[alias]))
	if err != nil {
		t.Fatal(err)
	}
	native := false
	for _, a := range target.Aliases {
		native = native || a == alias
	}
	got := "missing"
	if native {
		got = "native"
	}
	if got != want.Kind {
		t.Errorf("%s: %s, want %s", alias, got, want.Kind)
	}
}

func checkAliasConfRun(t *testing.T, argv []string, got aliasConfRecord, want aliasConfRun) {
	t.Helper()
	if got.ran == "" {
		got.ran = "none"
	}
	if got.ran != want.Ran {
		t.Errorf("%v: ran %s, want %s", argv, got.ran, want.Ran)
		return
	}
	norm := func(s []string) []string {
		if s == nil {
			return []string{}
		}
		return s
	}
	switch want.Ran {
	case "target":
		if got.path != want.Path {
			t.Errorf("%v: ran %q, want %q", argv, got.path, want.Path)
		}
		var env *string
		if got.hasEnv {
			env = &got.env
		}
		if !reflect.DeepEqual(env, want.Env) {
			t.Errorf("%v: env %v, want %v", argv, env, want.Env)
		}
		if !reflect.DeepEqual(norm(got.args), norm(want.Args)) {
			t.Errorf("%v: args %q, want %q", argv, got.args, want.Args)
		}
	case "handler":
		if got.name != want.Name || got.target != want.Target {
			t.Errorf("%v: handler got (%q, %q), want (%q, %q)", argv, got.name, got.target, want.Name, want.Target)
		}
		if !reflect.DeepEqual(norm(got.args), norm(want.Args)) {
			t.Errorf("%v: handler args %q, want %q", argv, got.args, want.Args)
		}
	}
}
