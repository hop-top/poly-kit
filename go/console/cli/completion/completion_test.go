package completion

import (
	"context"
	"testing"

	"github.com/spf13/cobra"
	"github.com/spf13/viper"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestStatic_EmptyPrefix(t *testing.T) {
	c := Static(
		Item{Value: "leo", Description: "Low Earth Orbit"},
		Item{Value: "geo", Description: "Geostationary"},
		Item{Value: "lunar", Description: "Trans-lunar"},
	)
	items, err := c.Complete(context.Background(), "")
	require.NoError(t, err)
	assert.Len(t, items, 3)
}

func TestStatic_PrefixFilter(t *testing.T) {
	c := Static(
		Item{Value: "leo", Description: "Low Earth Orbit"},
		Item{Value: "geo", Description: "Geostationary"},
		Item{Value: "lunar", Description: "Trans-lunar"},
	)
	items, err := c.Complete(context.Background(), "l")
	require.NoError(t, err)
	assert.Len(t, items, 2)
	assert.Equal(t, "leo", items[0].Value)
	assert.Equal(t, "lunar", items[1].Value)
}

func TestStatic_CaseInsensitive(t *testing.T) {
	c := Static(
		Item{Value: "LEO", Description: "Low Earth Orbit"},
		Item{Value: "GEO", Description: "Geostationary"},
	)
	items, err := c.Complete(context.Background(), "le")
	require.NoError(t, err)
	assert.Len(t, items, 1)
	assert.Equal(t, "LEO", items[0].Value)
}

func TestStaticValues(t *testing.T) {
	c := StaticValues("alpha", "beta", "gamma")
	items, err := c.Complete(context.Background(), "b")
	require.NoError(t, err)
	assert.Len(t, items, 1)
	assert.Equal(t, "beta", items[0].Value)
	assert.Empty(t, items[0].Description)
}

func TestFunc_CallbackInvoked(t *testing.T) {
	var captured string
	c := Func(func(_ context.Context, prefix string) ([]Item, error) {
		captured = prefix
		return []Item{{Value: "result"}}, nil
	})
	items, err := c.Complete(context.Background(), "test-prefix")
	require.NoError(t, err)
	assert.Equal(t, "test-prefix", captured)
	assert.Len(t, items, 1)
	assert.Equal(t, "result", items[0].Value)
}

func TestPrefixed_CompletesDimension(t *testing.T) {
	vals := StaticValues("high", "low")
	c := Prefixed("priority", vals)

	// No colon yet — suggest the dimension prefix.
	items, err := c.Complete(context.Background(), "")
	require.NoError(t, err)
	require.Len(t, items, 1)
	assert.Equal(t, "priority:", items[0].Value)
}

func TestPrefixed_CompletesValuesAfterColon(t *testing.T) {
	vals := StaticValues("high", "low")
	c := Prefixed("priority", vals)

	items, err := c.Complete(context.Background(), "priority:h")
	require.NoError(t, err)
	require.Len(t, items, 1)
	assert.Equal(t, "priority:high", items[0].Value)
}

func TestPrefixed_NoMatchWrongDimension(t *testing.T) {
	vals := StaticValues("high", "low")
	c := Prefixed("priority", vals)

	items, err := c.Complete(context.Background(), "other:")
	require.NoError(t, err)
	assert.Empty(t, items)
}

func TestConfigKeys(t *testing.T) {
	v := viper.New()
	v.Set("log.level", "info")
	v.Set("log.format", "json")
	v.Set("server.port", 8080)

	c := ConfigKeys(v)
	items, err := c.Complete(context.Background(), "log")
	require.NoError(t, err)
	assert.Len(t, items, 2)

	// All keys on empty prefix.
	all, err := c.Complete(context.Background(), "")
	require.NoError(t, err)
	assert.Len(t, all, 3)
}

func TestFile_Directive(t *testing.T) {
	c := File(".yaml", ".yml")
	fc, ok := c.(*fileCompleter)
	require.True(t, ok)
	assert.Equal(t, []string{".yaml", ".yml"}, fc.extensions)
	assert.False(t, fc.dirOnly)
}

func TestDir_Directive(t *testing.T) {
	c := Dir()
	fc, ok := c.(*fileCompleter)
	require.True(t, ok)
	assert.True(t, fc.dirOnly)
}

func TestRegistry_FlagLookup(t *testing.T) {
	r := NewRegistry()
	c := StaticValues("a", "b")
	r.Register("orbit", c)

	got := r.ForFlag("orbit")
	assert.NotNil(t, got)
	assert.Nil(t, r.ForFlag("missing"))
}

func TestRegistry_ArgLookup(t *testing.T) {
	r := NewRegistry()
	c := StaticValues("m1", "m2")
	r.RegisterArg("launch", 0, c)

	got := r.ForArg("launch", 0)
	assert.NotNil(t, got)
	assert.Nil(t, r.ForArg("launch", 1))
	assert.Nil(t, r.ForArg("other", 0))
}

func TestBindFlag_CobraIntegration(t *testing.T) {
	cmd := &cobra.Command{Use: "test"}
	cmd.Flags().String("orbit", "", "target orbit")

	c := StaticValues("leo", "geo", "lunar")
	BindFlag(cmd, "orbit", c)

	// Invoke cobra's completion function for the flag.
	completions, dir := cobraFlagCompletions(cmd, "orbit", "l")
	assert.Equal(t, cobra.ShellCompDirectiveNoFileComp, dir)
	assert.Contains(t, completions, "leo")
	assert.Contains(t, completions, "lunar")
	assert.NotContains(t, completions, "geo")
}

func TestBindArgs_CobraIntegration(t *testing.T) {
	cmd := &cobra.Command{Use: "launch"}
	c := StaticValues("starlink-42", "crew-dragon-1", "starship-ift-3")
	BindArgs(cmd, c)

	assert.NotNil(t, cmd.ValidArgsFunction)
	completions, dir := cmd.ValidArgsFunction(cmd, nil, "star")
	assert.Equal(t, cobra.ShellCompDirectiveNoFileComp, dir)
	assert.Contains(t, completions, "starlink-42")
	assert.Contains(t, completions, "starship-ift-3")
	assert.NotContains(t, completions, "crew-dragon-1")
}

func TestBindFlag_FileCompleter(t *testing.T) {
	cmd := &cobra.Command{Use: "test"}
	cmd.Flags().String("config", "", "config file")

	c := File(".yaml", ".yml")
	BindFlag(cmd, "config", c)

	completions, dir := cobraFlagCompletions(cmd, "config", "")
	assert.Empty(t, completions)
	assert.Equal(t, cobra.ShellCompDirectiveFilterFileExt, dir)
}

func TestBindFlag_DirCompleter(t *testing.T) {
	cmd := &cobra.Command{Use: "test"}
	cmd.Flags().String("outdir", "", "output directory")

	c := Dir()
	BindFlag(cmd, "outdir", c)

	completions, dir := cobraFlagCompletions(cmd, "outdir", "")
	assert.Empty(t, completions)
	assert.Equal(t, cobra.ShellCompDirectiveFilterDirs, dir)
}

// cobraFlagCompletions invokes the registered flag completion func.
func cobraFlagCompletions(
	cmd *cobra.Command, flag, toComplete string,
) ([]string, cobra.ShellCompDirective) {
	// Access __completeFlag via cobra internals: call RegisterFlagCompletionFunc
	// then invoke via the completion mechanism. We use the internal map.
	fn, ok := cmd.GetFlagCompletionFunc(flag)
	if !ok || fn == nil {
		return nil, cobra.ShellCompDirectiveError
	}
	return fn(cmd, nil, toComplete)
}
