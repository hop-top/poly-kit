package adapters

import (
	"io"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"hop.top/kit/go/ai/toolspec"
)

// fakeAdapter is a test-only FormatAdapter for registry / dispatch
// behavior exercises that don't need a real renderer.
type fakeAdapter struct {
	name        string
	aliases     []string
	description string
	contentType string
}

func (f fakeAdapter) Name() string        { return f.name }
func (f fakeAdapter) Aliases() []string   { return f.aliases }
func (f fakeAdapter) Description() string { return f.description }
func (f fakeAdapter) ContentType() string { return f.contentType }
func (f fakeAdapter) Render(io.Writer, *toolspec.ToolSpec, ...RenderOption) error {
	return nil
}

// --- Registry: register and lookup -------------------------------

func TestRegistry_RegisterAndLookup(t *testing.T) {
	r := NewRegistry()
	a := fakeAdapter{name: "alpha", aliases: []string{"a"}}
	require.NoError(t, r.Register(a))
	assert.Equal(t, a, r.Lookup("alpha"))
	assert.Equal(t, a, r.Lookup("a"), "alias resolves to same adapter")
	assert.Nil(t, r.Lookup("unknown"))
}

func TestRegistry_RegisterNil(t *testing.T) {
	r := NewRegistry()
	err := r.Register(nil)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "nil adapter")
}

func TestRegistry_RegisterEmptyName(t *testing.T) {
	r := NewRegistry()
	err := r.Register(fakeAdapter{name: ""})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "empty Name()")
}

func TestRegistry_NameCollision(t *testing.T) {
	r := NewRegistry()
	require.NoError(t, r.Register(fakeAdapter{name: "alpha"}))
	err := r.Register(fakeAdapter{name: "alpha"})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "already registered")
}

func TestRegistry_AliasCollision(t *testing.T) {
	r := NewRegistry()
	require.NoError(t, r.Register(fakeAdapter{name: "alpha", aliases: []string{"a"}}))
	err := r.Register(fakeAdapter{name: "beta", aliases: []string{"a"}})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "alias")
	assert.Contains(t, err.Error(), "collides")
}

func TestRegistry_AliasCollidesWithName(t *testing.T) {
	r := NewRegistry()
	require.NoError(t, r.Register(fakeAdapter{name: "alpha"}))
	err := r.Register(fakeAdapter{name: "beta", aliases: []string{"alpha"}})
	require.Error(t, err)
}

// --- Registry: unregister -----------------------------------------

func TestRegistry_Unregister(t *testing.T) {
	r := NewRegistry()
	a := fakeAdapter{name: "alpha", aliases: []string{"a"}}
	require.NoError(t, r.Register(a))
	r.Unregister("alpha")
	assert.Nil(t, r.Lookup("alpha"))
	assert.Nil(t, r.Lookup("a"), "aliases also removed")
}

func TestRegistry_UnregisterUnknown(t *testing.T) {
	r := NewRegistry()
	require.NotPanics(t, func() { r.Unregister("never-registered") })
}

func TestRegistry_UnregisterByAlias(t *testing.T) {
	// The contract says Unregister takes the canonical name; calling
	// with an alias should also remove (since lookup-by-alias works
	// in the byName map). Verify the behavior matches expectation.
	r := NewRegistry()
	require.NoError(t, r.Register(fakeAdapter{name: "alpha", aliases: []string{"a"}}))
	r.Unregister("a")
	assert.Nil(t, r.Lookup("alpha"))
	assert.Nil(t, r.Lookup("a"))
}

// --- Registry: order + default -----------------------------------

func TestRegistry_Names_PreservesOrder(t *testing.T) {
	r := NewRegistry()
	require.NoError(t, r.Register(fakeAdapter{name: "first"}))
	require.NoError(t, r.Register(fakeAdapter{name: "second"}))
	require.NoError(t, r.Register(fakeAdapter{name: "third"}))
	assert.Equal(t, []string{"first", "second", "third"}, r.Names())
}

func TestRegistry_Names_OrderSurvivesUnregister(t *testing.T) {
	r := NewRegistry()
	require.NoError(t, r.Register(fakeAdapter{name: "first"}))
	require.NoError(t, r.Register(fakeAdapter{name: "second"}))
	require.NoError(t, r.Register(fakeAdapter{name: "third"}))
	r.Unregister("second")
	assert.Equal(t, []string{"first", "third"}, r.Names())
}

func TestRegistry_Default(t *testing.T) {
	r := NewRegistry()
	assert.Nil(t, r.Default(), "empty registry has no default")
	a := fakeAdapter{name: "first"}
	require.NoError(t, r.Register(a))
	assert.Equal(t, a, r.Default(), "first registered = default")

	// Adding a second adapter does not change the default.
	b := fakeAdapter{name: "second"}
	require.NoError(t, r.Register(b))
	assert.Equal(t, a, r.Default(), "default sticks to first")
}

// --- RenderOption resolution -------------------------------------

func TestResolveRenderOptions_Defaults(t *testing.T) {
	cfg := ResolveRenderOptions(nil)
	assert.True(t, cfg.Pretty)
	assert.True(t, cfg.IncludeDeprecated)
	assert.Empty(t, cfg.SchemaVersion)
	assert.Nil(t, cfg.Custom)
}

func TestResolveRenderOptions_AllSet(t *testing.T) {
	cfg := ResolveRenderOptions([]RenderOption{
		WithPretty(false),
		WithSchemaVersion("2.0"),
		WithIncludeDeprecated(false),
		WithCustom("foo", "bar"),
		WithCustom("baz", 42),
	})
	assert.False(t, cfg.Pretty)
	assert.Equal(t, "2.0", cfg.SchemaVersion)
	assert.False(t, cfg.IncludeDeprecated)
	assert.Equal(t, "bar", cfg.Custom["foo"])
	assert.Equal(t, 42, cfg.Custom["baz"])
}

func TestResolveRenderOptions_LastWins(t *testing.T) {
	cfg := ResolveRenderOptions([]RenderOption{
		WithSchemaVersion("1.0"),
		WithSchemaVersion("2.0"),
		WithSchemaVersion("3.0"),
	})
	assert.Equal(t, "3.0", cfg.SchemaVersion)
}

// --- Helpers for adapter tests -----------------------------------

// minimalSpec returns a small ToolSpec exercising the curation,
// command-tree, and flag fields.
func minimalSpec() *toolspec.ToolSpec {
	return &toolspec.ToolSpec{
		Name:          "mytool",
		SchemaVersion: "1.0",
		Commands: []toolspec.Command{
			{
				Name:   "list",
				Safety: &toolspec.Safety{Level: toolspec.SafetyLevelSafe},
			},
			{
				Name:   "create",
				Safety: &toolspec.Safety{Level: toolspec.SafetyLevelSafe},
				Contract: &toolspec.Contract{
					Idempotent:  false,
					SideEffects: []string{"write"},
				},
				Flags: []toolspec.Flag{
					{Name: "name", Type: "string", Description: "thing name"},
				},
			},
			{
				Name:   "delete",
				Safety: &toolspec.Safety{Level: toolspec.SafetyLevelDangerous, RequiresConfirmation: true},
			},
		},
		Flags: []toolspec.Flag{
			{Name: "config", Type: "string", Description: "config path"},
			{Name: "verbose", Short: "v", Type: "bool", Description: "verbose"},
		},
	}
}

// readJSON unmarshals the captured render output into the value
// pointed at by out. Helper used by both adapters' tests.
func mustContain(t *testing.T, haystack, needle string) {
	t.Helper()
	require.True(t, strings.Contains(haystack, needle),
		"expected to find %q in:\n%s", needle, haystack)
}
