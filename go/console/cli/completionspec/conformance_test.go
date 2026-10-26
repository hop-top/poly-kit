package completionspec_test

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/spf13/cobra"

	"hop.top/kit/go/console/cli/completionspec"
)

// Runs the shared corpus in contracts/completion-spec-v1: rebuild the
// fixture tree in cobra, export it, compare byte for byte. The expected
// files are the source of truth: a failure means this implementation
// drifted, not that the expected file should be edited to fit.

const specConfDir = "../../../../contracts/completion-spec-v1"

// specConfFiles is every file this runner consumes; the corpus
// directory must match it exactly, README aside.
var specConfFiles = map[string]bool{
	"README.md":               true,
	"tree.json":               true,
	"expected.fig.js":         true,
	"expected.usage.kdl":      true,
	"tree-tera.json":          true,
	"expected-tera.fig.js":    true,
	"expected-tera.usage.kdl": true,
}

// specConfCases pairs each fixture tree with its expected files.
var specConfCases = []struct{ tree, fig, usage string }{
	{"tree.json", "expected.fig.js", "expected.usage.kdl"},
	{"tree-tera.json", "expected-tera.fig.js", "expected-tera.usage.kdl"},
}

type confTree struct {
	Version     string  `json:"version"`
	Description string  `json:"description"`
	Root        confCmd `json:"root"`
}

type confCmd struct {
	Use               string            `json:"use"`
	Short             string            `json:"short"`
	Version           string            `json:"version"`
	Aliases           []string          `json:"aliases"`
	Hidden            bool              `json:"hidden"`
	Deprecated        string            `json:"deprecated"`
	Annotations       map[string]string `json:"annotations"`
	ValidArgs         []string          `json:"valid_args"`
	ValidArgsFunction bool              `json:"valid_args_function"`
	NoArgs            bool              `json:"no_args"`
	Flags             []confFlag        `json:"flags"`
	Commands          []confCmd         `json:"commands"`
}

type confFlag struct {
	Name               string   `json:"name"`
	Short              string   `json:"short"`
	Type               string   `json:"type"`
	Usage              string   `json:"usage"`
	Persistent         bool     `json:"persistent"`
	Hidden             bool     `json:"hidden"`
	Deprecated         string   `json:"deprecated"`
	Required           bool     `json:"required"`
	NoOptDefault       string   `json:"no_opt_default"`
	Enum               []string `json:"enum"`
	FilenameExt        []string `json:"filename_ext"`
	Dirname            bool     `json:"dirname"`
	CompletionFunction bool     `json:"completion_function"`
}

func loadSpecConf(t *testing.T) confTree {
	t.Helper()
	return loadSpecConfFile(t, "tree.json")
}

func loadSpecConfFile(t *testing.T, name string) confTree {
	t.Helper()
	dir := filepath.FromSlash(specConfDir)
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range entries {
		if !specConfFiles[e.Name()] {
			t.Errorf("%s has no runner", e.Name())
		}
	}
	raw, err := os.ReadFile(filepath.Join(dir, name))
	if err != nil {
		t.Fatal(err)
	}
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.DisallowUnknownFields()
	var tree confTree
	if err := dec.Decode(&tree); err != nil {
		t.Fatalf("%s: %v", name, err)
	}
	if tree.Version != "v1" {
		t.Fatalf("%s: version %q, want v1", name, tree.Version)
	}
	return tree
}

func noComplete(*cobra.Command, []string, string) ([]string, cobra.ShellCompDirective) {
	return nil, cobra.ShellCompDirectiveNoFileComp
}

// buildConfCmd rebuilds one fixture node, and its children, as cobra.
func buildConfCmd(t *testing.T, c confCmd) *cobra.Command {
	t.Helper()
	cmd := &cobra.Command{
		Use:         c.Use,
		Short:       c.Short,
		Version:     c.Version,
		Aliases:     c.Aliases,
		Hidden:      c.Hidden,
		Deprecated:  c.Deprecated,
		Annotations: c.Annotations,
		ValidArgs:   c.ValidArgs,
		Run:         func(*cobra.Command, []string) {},
	}
	if c.ValidArgsFunction {
		cmd.ValidArgsFunction = noComplete
	}
	if c.NoArgs {
		cmd.Args = cobra.NoArgs
	}
	for _, sub := range c.Commands {
		cmd.AddCommand(buildConfCmd(t, sub))
	}
	for _, f := range c.Flags {
		addConfFlag(t, cmd, f)
	}
	return cmd
}

func addConfFlag(t *testing.T, cmd *cobra.Command, f confFlag) {
	t.Helper()
	set := cmd.Flags()
	if f.Persistent {
		set = cmd.PersistentFlags()
	}
	switch f.Type {
	case "bool":
		set.BoolP(f.Name, f.Short, false, f.Usage)
	case "count":
		set.CountP(f.Name, f.Short, f.Usage)
	case "string":
		set.StringP(f.Name, f.Short, "", f.Usage)
	case "int":
		set.IntP(f.Name, f.Short, 0, f.Usage)
	case "duration":
		set.DurationP(f.Name, f.Short, 0, f.Usage)
	case "stringSlice":
		set.StringSliceP(f.Name, f.Short, nil, f.Usage)
	case "stringArray":
		set.StringArrayP(f.Name, f.Short, nil, f.Usage)
	default:
		t.Fatalf("flag %s: unknown type %q", f.Name, f.Type)
	}
	fl := set.Lookup(f.Name)
	fl.Hidden = f.Hidden
	fl.NoOptDefVal = orDefault(f.NoOptDefault, fl.NoOptDefVal)
	if f.Deprecated != "" {
		if err := set.MarkDeprecated(f.Name, f.Deprecated); err != nil {
			t.Fatal(err)
		}
	}
	if f.Required {
		if err := cobra.MarkFlagRequired(set, f.Name); err != nil {
			t.Fatal(err)
		}
	}
	if f.Enum != nil {
		if err := set.SetAnnotation(f.Name, completionspec.FlagEnumAnnotation, f.Enum); err != nil {
			t.Fatal(err)
		}
	}
	if f.FilenameExt != nil {
		if err := set.SetAnnotation(f.Name, cobra.BashCompFilenameExt, f.FilenameExt); err != nil {
			t.Fatal(err)
		}
	}
	if f.Dirname {
		if err := cobra.MarkFlagDirname(set, f.Name); err != nil {
			t.Fatal(err)
		}
	}
	if f.CompletionFunction {
		if err := cmd.RegisterFlagCompletionFunc(f.Name, noComplete); err != nil {
			t.Fatal(err)
		}
	}
}

func orDefault(v, def string) string {
	if v != "" {
		return v
	}
	return def
}

func TestConformanceCompletionSpec(t *testing.T) {
	for _, c := range specConfCases {
		tree := loadSpecConfFile(t, c.tree)
		spec := completionspec.FromCobra(buildConfCmd(t, tree.Root))
		for _, tc := range []struct {
			file   string
			render func(completionspec.Spec) string
		}{
			{c.fig, completionspec.RenderFig},
			{c.usage, completionspec.RenderUsage},
		} {
			t.Run(tc.file, func(t *testing.T) {
				want, err := os.ReadFile(filepath.Join(filepath.FromSlash(specConfDir), tc.file))
				if err != nil {
					t.Fatal(err)
				}
				got := tc.render(spec)
				if got != string(want) {
					t.Errorf("%s drifted:\n%s", tc.file, lineDiff(string(want), got))
				}
			})
		}
	}
}

// TestConformanceCompletionSpecOrder: the fixture's declaration order
// must not leak into the output. Reversing every list in the tree gives
// the same bytes.
func TestConformanceCompletionSpecOrder(t *testing.T) {
	tree := loadSpecConf(t)
	want := completionspec.RenderFig(completionspec.FromCobra(buildConfCmd(t, tree.Root)))
	reverseConf(&tree.Root)
	root := buildConfCmd(t, tree.Root)
	unsortFlags(root)
	cobra.EnableCommandSorting = false
	defer func() { cobra.EnableCommandSorting = true }()
	if got := completionspec.RenderFig(completionspec.FromCobra(root)); got != want {
		t.Errorf("declaration order leaked into the spec:\n%s", lineDiff(want, got))
	}
}

func reverseConf(c *confCmd) {
	for i, j := 0, len(c.Flags)-1; i < j; i, j = i+1, j-1 {
		c.Flags[i], c.Flags[j] = c.Flags[j], c.Flags[i]
	}
	for i, j := 0, len(c.Commands)-1; i < j; i, j = i+1, j-1 {
		c.Commands[i], c.Commands[j] = c.Commands[j], c.Commands[i]
	}
	for i := range c.Commands {
		reverseConf(&c.Commands[i])
	}
}

// unsortFlags makes every flag set under cmd list flags in declaration
// order, as an adopter calling SortFlags = false would.
func unsortFlags(cmd *cobra.Command) {
	cmd.Flags().SortFlags = false
	cmd.PersistentFlags().SortFlags = false
	for _, c := range cmd.Commands() {
		unsortFlags(c)
	}
}

// lineDiff reports the first differing line with a little context.
func lineDiff(want, got string) string {
	w := bytes.Split([]byte(want), []byte("\n"))
	g := bytes.Split([]byte(got), []byte("\n"))
	for i := 0; i < len(w) || i < len(g); i++ {
		var wl, gl []byte
		if i < len(w) {
			wl = w[i]
		}
		if i < len(g) {
			gl = g[i]
		}
		if !bytes.Equal(wl, gl) {
			return "line " + itoa(i+1) + ":\n  want: " + string(wl) + "\n  got:  " + string(gl)
		}
	}
	return "(no line differs; trailing bytes differ)"
}

func itoa(n int) string {
	b, _ := json.Marshal(n)
	return string(b)
}
