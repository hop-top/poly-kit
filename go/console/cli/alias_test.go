package cli_test

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/spf13/cobra"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"hop.top/kit/go/console/alias"
	"hop.top/kit/go/console/cli"
)

func TestAlias_Basic(t *testing.T) {
	r := root()
	cmd := &cobra.Command{Use: "serve", Short: "start server"}
	r.Cmd.AddCommand(cmd)

	require.NoError(t, r.Alias("s", cmd))
	assert.Contains(t, cmd.Aliases, "s")
}

func TestAlias_CollisionWithCommand(t *testing.T) {
	r := root()
	foo := &cobra.Command{Use: "foo"}
	bar := &cobra.Command{Use: "bar"}
	r.Cmd.AddCommand(foo, bar)

	err := r.Alias("foo", bar)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "collides with command")
}

func TestAlias_CollisionWithAlias(t *testing.T) {
	r := root()
	a := &cobra.Command{Use: "alpha"}
	b := &cobra.Command{Use: "beta"}
	r.Cmd.AddCommand(a, b)

	require.NoError(t, r.Alias("x", a))
	err := r.Alias("x", b)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "already registered")
}

func TestAlias_MultipleAliases(t *testing.T) {
	r := root()
	cmd := &cobra.Command{Use: "deploy"}
	r.Cmd.AddCommand(cmd)

	require.NoError(t, r.Alias("d", cmd))
	require.NoError(t, r.Alias("dep", cmd))
	assert.Contains(t, cmd.Aliases, "d")
	assert.Contains(t, cmd.Aliases, "dep")
}

func TestAlias_Dispatch(t *testing.T) {
	r := root()
	var ran bool
	cmd := &cobra.Command{
		Use: "greet",
		Run: func(_ *cobra.Command, _ []string) { ran = true },
	}
	r.Cmd.AddCommand(cmd)
	require.NoError(t, r.Alias("g", cmd))

	r.Cmd.SetArgs([]string{"g"})
	require.NoError(t, r.Execute(t.Context()))
	assert.True(t, ran, "aliased command must execute")
}

func TestAlias_Aliases(t *testing.T) {
	r := root()
	cmd := &cobra.Command{Use: "serve"}
	r.Cmd.AddCommand(cmd)
	require.NoError(t, r.Alias("s", cmd))

	m := r.Aliases()
	assert.Equal(t, "serve", m["s"])
}

func TestAlias_EmptyName(t *testing.T) {
	r := root()
	cmd := &cobra.Command{Use: "serve"}
	r.Cmd.AddCommand(cmd)

	err := r.Alias("", cmd)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "non-empty")
}

func TestAlias_WhitespaceName(t *testing.T) {
	r := root()
	cmd := &cobra.Command{Use: "serve"}
	r.Cmd.AddCommand(cmd)

	err := r.Alias("a b", cmd)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "whitespace")
}

func TestLoadAliases_FromViper(t *testing.T) {
	r := root()
	var ran bool
	parent := &cobra.Command{Use: "router"}
	child := &cobra.Command{
		Use: "start",
		Run: func(*cobra.Command, []string) { ran = true },
	}
	parent.AddCommand(child)
	r.Cmd.AddCommand(parent)

	r.Viper.Set("aliases", map[string]string{"rs": "router start"})
	require.NoError(t, r.LoadAliases())

	m := r.Aliases()
	assert.Equal(t, "router start", m["rs"])

	r.Cmd.SetArgs([]string{"rs"})
	require.NoError(t, r.Execute(t.Context()))
	assert.True(t, ran)
}

func TestLoadAliases_NestedDispatchFromRoot(t *testing.T) {
	r := root()
	var ran bool
	parent := &cobra.Command{Use: "router"}
	child := &cobra.Command{
		Use: "start",
		Run: func(*cobra.Command, []string) { ran = true },
	}
	parent.AddCommand(child)
	r.Cmd.AddCommand(parent)

	r.Viper.Set("aliases", map[string]string{"rs": "router start"})
	require.NoError(t, r.LoadAliases())

	r.Cmd.SetArgs([]string{"rs"})
	require.NoError(t, r.Execute(t.Context()))
	assert.True(t, ran, "root-level alias must dispatch to nested command")
}

func TestLoadAliases_BadTarget(t *testing.T) {
	r := root()
	r.Viper.Set("aliases", map[string]string{"x": "nonexistent"})
	err := r.LoadAliases()
	require.Error(t, err)
	assert.Contains(t, err.Error(), "not found")
}

func TestLoadAliases_Empty(t *testing.T) {
	r := root()
	require.NoError(t, r.LoadAliases())
}

func TestAliasesCmd_Output(t *testing.T) {
	r := root()
	cmd := &cobra.Command{Use: "build", Run: func(*cobra.Command, []string) {}}
	r.Cmd.AddCommand(cmd)
	require.NoError(t, r.Alias("b", cmd))

	aliasCmd := r.AliasesCmd()
	r.Cmd.AddCommand(aliasCmd)

	var buf bytes.Buffer
	r.Cmd.SetOut(&buf)
	r.Cmd.SetArgs([]string{"aliases"})
	require.NoError(t, r.Execute(t.Context()))
	assert.Contains(t, buf.String(), "b")
	assert.Contains(t, buf.String(), "build")
}

func TestAliasesCmd_JSON(t *testing.T) {
	r := cli.New(cli.Config{Name: "test", Version: "0.0.1", Short: "t", DisableValidate: true})
	cmd := &cobra.Command{Use: "deploy", Run: func(*cobra.Command, []string) {}}
	r.Cmd.AddCommand(cmd)
	require.NoError(t, r.Alias("d", cmd))

	r.Cmd.AddCommand(r.AliasesCmd())

	var buf bytes.Buffer
	r.Cmd.SetOut(&buf)
	r.Cmd.SetArgs([]string{"aliases", "--format", "json"})
	require.NoError(t, r.Execute(t.Context()))
	assert.Contains(t, buf.String(), `"alias"`)
	assert.Contains(t, buf.String(), `"deploy"`)
}

func TestLoadAliasStore(t *testing.T) {
	r := root()
	var ran bool
	parent := &cobra.Command{Use: "router"}
	child := &cobra.Command{
		Use: "start",
		Run: func(*cobra.Command, []string) { ran = true },
	}
	parent.AddCommand(child)
	r.Cmd.AddCommand(parent)

	dir := t.TempDir()
	store := alias.NewStore(filepath.Join(dir, "aliases.yaml"))
	require.NoError(t, store.Set("rs", "router start"))

	require.NoError(t, r.LoadAliasStore(store))

	m := r.Aliases()
	assert.Equal(t, "router start", m["rs"])

	r.Cmd.SetArgs([]string{"rs"})
	require.NoError(t, r.Execute(t.Context()))
	assert.True(t, ran)
}

func TestAliasCmd_Add(t *testing.T) {
	r := root()
	dir := t.TempDir()
	store := alias.NewStore(filepath.Join(dir, "aliases.yaml"))

	r.Cmd.AddCommand(r.AliasCmd(store))

	var buf bytes.Buffer
	r.Cmd.SetOut(&buf)
	r.Cmd.SetArgs([]string{"alias", "add", "d", "deploy"})
	require.NoError(t, r.Execute(t.Context()))
	assert.Contains(t, buf.String(), "alias d")

	v, ok := store.Get("d")
	assert.True(t, ok)
	assert.Equal(t, "deploy", v)
}

func TestAliasCmd_Delete(t *testing.T) {
	r := root()
	dir := t.TempDir()
	store := alias.NewStore(filepath.Join(dir, "aliases.yaml"))
	require.NoError(t, store.Set("d", "deploy"))
	require.NoError(t, store.Save())

	r.Cmd.AddCommand(r.AliasCmd(store))

	var buf bytes.Buffer
	r.Cmd.SetOut(&buf)
	r.Cmd.SetArgs([]string{"alias", "delete", "d"})
	require.NoError(t, r.Execute(t.Context()))
	assert.Contains(t, buf.String(), "deleted")

	_, ok := store.Get("d")
	assert.False(t, ok)
}

// TestAliasCmd_DeleteRemoveAlias verifies the legacy "remove" verb
// still works as an alias for "delete" during the deprecation window.
func TestAliasCmd_DeleteRemoveAlias(t *testing.T) {
	r := root()
	dir := t.TempDir()
	store := alias.NewStore(filepath.Join(dir, "aliases.yaml"))
	require.NoError(t, store.Set("d", "deploy"))
	require.NoError(t, store.Save())

	r.Cmd.AddCommand(r.AliasCmd(store))

	var buf bytes.Buffer
	r.Cmd.SetOut(&buf)
	r.Cmd.SetArgs([]string{"alias", "remove", "d"})
	require.NoError(t, r.Execute(t.Context()))
	assert.Contains(t, buf.String(), "deleted")

	_, ok := store.Get("d")
	assert.False(t, ok)
}

func TestAlias_FlagDefaultOverride(t *testing.T) {
	r := root()
	var envVal string
	cmd := &cobra.Command{
		Use: "deploy",
		Run: func(cmd *cobra.Command, _ []string) {
			envVal, _ = cmd.Flags().GetString("env")
		},
	}
	cmd.Flags().String("env", "dev", "target environment")
	r.Cmd.AddCommand(cmd)
	require.NoError(t, r.Alias("dp", cmd))

	// alias dp → deploy; user passes --env staging (last wins)
	r.Cmd.SetArgs([]string{"dp", "--env", "staging"})
	require.NoError(t, r.Execute(t.Context()))
	assert.Equal(t, "staging", envVal, "user flag must override default")
}

func TestAlias_CompletionIncludesAliases(t *testing.T) {
	r := root()
	deploy := &cobra.Command{Use: "deploy", Run: func(*cobra.Command, []string) {}}
	r.Cmd.AddCommand(deploy)
	require.NoError(t, r.Alias("d", deploy))

	require.NotNil(t, r.Cmd.ValidArgsFunction,
		"ValidArgsFunction must be set after Alias registration")

	results, dir := r.Cmd.ValidArgsFunction(r.Cmd, nil, "d")
	assert.Equal(t, cobra.ShellCompDirectiveNoFileComp, dir)

	// "d" should appear with description
	found := false
	for _, s := range results {
		if strings.HasPrefix(s, "d\t") {
			found = true
			assert.Contains(t, s, "alias for")
			break
		}
	}
	assert.True(t, found, "alias 'd' must appear in completions, got: %v", results)
}

func TestAlias_CompletionFiltersPrefix(t *testing.T) {
	r := root()
	deploy := &cobra.Command{Use: "deploy", Run: func(*cobra.Command, []string) {}}
	serve := &cobra.Command{Use: "serve", Run: func(*cobra.Command, []string) {}}
	r.Cmd.AddCommand(deploy, serve)
	require.NoError(t, r.Alias("d", deploy))
	require.NoError(t, r.Alias("s", serve))

	results, _ := r.Cmd.ValidArgsFunction(r.Cmd, nil, "d")
	for _, s := range results {
		// only "d" alias should match, not "s"
		assert.False(t, strings.HasPrefix(s, "s\t"),
			"alias 's' should not appear when completing 'd'")
	}
}

func TestAlias_CompletionPreservesOriginal(t *testing.T) {
	r := root()
	// Set an original ValidArgsFunction before aliases
	r.Cmd.ValidArgsFunction = func(_ *cobra.Command, _ []string, _ string) ([]string, cobra.ShellCompDirective) {
		return []string{"original"}, cobra.ShellCompDirectiveNoFileComp
	}

	deploy := &cobra.Command{Use: "deploy", Run: func(*cobra.Command, []string) {}}
	r.Cmd.AddCommand(deploy)
	require.NoError(t, r.Alias("d", deploy))

	results, _ := r.Cmd.ValidArgsFunction(r.Cmd, nil, "")
	assert.Contains(t, results, "original",
		"original ValidArgsFunction results must be preserved")

	found := false
	for _, s := range results {
		if strings.HasPrefix(s, "d\t") {
			found = true
			break
		}
	}
	assert.True(t, found, "alias must also appear alongside original completions")
}

func TestAlias_CompletionFromStore(t *testing.T) {
	r := root()
	parent := &cobra.Command{Use: "router"}
	child := &cobra.Command{
		Use: "start",
		Run: func(*cobra.Command, []string) {},
	}
	parent.AddCommand(child)
	r.Cmd.AddCommand(parent)

	dir := t.TempDir()
	store := alias.NewStore(filepath.Join(dir, "aliases.yaml"))
	require.NoError(t, store.Set("rs", "router start"))

	require.NoError(t, r.LoadAliasStore(store))

	require.NotNil(t, r.Cmd.ValidArgsFunction)
	results, _ := r.Cmd.ValidArgsFunction(r.Cmd, nil, "r")

	found := false
	for _, s := range results {
		if strings.HasPrefix(s, "rs\t") {
			found = true
			assert.Contains(t, s, "alias for")
			break
		}
	}
	assert.True(t, found, "store-loaded alias 'rs' must appear in completions")
}

func TestAlias_CompletionPreservesDirective(t *testing.T) {
	r := root()
	r.Cmd.ValidArgsFunction = func(cmd *cobra.Command, args []string, toComplete string) ([]string, cobra.ShellCompDirective) {
		return []string{"file.txt"}, cobra.ShellCompDirectiveFilterFileExt
	}

	deploy := &cobra.Command{Use: "deploy", Run: func(*cobra.Command, []string) {}}
	r.Cmd.AddCommand(deploy)
	require.NoError(t, r.Alias("d", deploy))

	_, directive := r.Cmd.ValidArgsFunction(r.Cmd, nil, "")
	// Should preserve FilterFileExt, not just NoFileComp
	assert.True(t, directive&cobra.ShellCompDirectiveFilterFileExt != 0,
		"original directive must be preserved, got: %v", directive)
}

func TestAliasCmd_List(t *testing.T) {
	r := root()
	cmd := &cobra.Command{Use: "deploy", Run: func(*cobra.Command, []string) {}}
	r.Cmd.AddCommand(cmd)
	require.NoError(t, r.Alias("d", cmd))

	dir := t.TempDir()
	store := alias.NewStore(filepath.Join(dir, "aliases.yaml"))
	require.NoError(t, store.Set("g", "greet"))

	r.Cmd.AddCommand(r.AliasCmd(store))

	var buf bytes.Buffer
	r.Cmd.SetOut(&buf)
	r.Cmd.SetArgs([]string{"alias", "list"})
	require.NoError(t, r.Execute(t.Context()))

	// both store and runtime aliases shown
	assert.Contains(t, buf.String(), "d")
	assert.Contains(t, buf.String(), "g")
}

// TestAliasCmd_List_OutputToFile_JSON exercises the Dispatch
// migration: --output writes to a path, --format json switches the
// formatter. Regression guard for the T-0990 callsite swap.
func TestAliasCmd_List_OutputToFile_JSON(t *testing.T) {
	r := root()
	cmd := &cobra.Command{Use: "deploy", Run: func(*cobra.Command, []string) {}}
	r.Cmd.AddCommand(cmd)
	require.NoError(t, r.Alias("d", cmd))

	dir := t.TempDir()
	store := alias.NewStore(filepath.Join(dir, "aliases.yaml"))
	r.Cmd.AddCommand(r.AliasCmd(store))

	out := filepath.Join(dir, "aliases.json")
	r.Cmd.SetArgs([]string{"alias", "list", "--format", "json", "-o", out})
	require.NoError(t, r.Execute(t.Context()))

	body, err := os.ReadFile(out)
	require.NoError(t, err)
	s := string(body)
	assert.Contains(t, s, `"alias"`)
	assert.Contains(t, s, `"deploy"`)
	assert.True(t, strings.HasPrefix(strings.TrimSpace(s), "["),
		"json output should be a top-level array, got: %q", s)
}
