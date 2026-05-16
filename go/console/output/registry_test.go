package output_test

import (
	"io"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"hop.top/kit/go/console/output"
)

type fakeFormatter struct {
	key  string
	exts []string
}

func (f fakeFormatter) Key() string                  { return f.key }
func (f fakeFormatter) Extensions() []string         { return f.exts }
func (f fakeFormatter) Options() []output.OptionSpec { return nil }
func (f fakeFormatter) Render(_ io.Writer, _ any, _ output.Options, _ []string) error {
	return nil
}

func TestRegistry_RegisterAndLookup(t *testing.T) {
	r := output.NewRegistry()
	r.Register(fakeFormatter{key: "html", exts: []string{".html"}})
	got, ok := r.Lookup("html")
	require.True(t, ok)
	assert.Equal(t, "html", got.Key())
}

func TestRegistry_LookupMiss(t *testing.T) {
	r := output.NewRegistry()
	_, ok := r.Lookup("nope")
	assert.False(t, ok)
}

func TestRegistry_DuplicateRegisterPanics(t *testing.T) {
	r := output.NewRegistry()
	r.Register(fakeFormatter{key: "x"})
	assert.Panics(t, func() {
		r.Register(fakeFormatter{key: "x"})
	})
}

func TestRegistry_OverrideReplaces(t *testing.T) {
	r := output.NewRegistry()
	r.Register(fakeFormatter{key: "x", exts: []string{".x"}})
	r.Override(fakeFormatter{key: "x", exts: []string{".xx"}})
	got, ok := r.Lookup("x")
	require.True(t, ok)
	assert.Equal(t, []string{".xx"}, got.Extensions())
}

func TestRegistry_OverrideNewKeyAlsoWorks(t *testing.T) {
	r := output.NewRegistry()
	r.Override(fakeFormatter{key: "fresh"}) // no prior registration
	_, ok := r.Lookup("fresh")
	assert.True(t, ok)
}

func TestRegistry_EmptyKeyPanics(t *testing.T) {
	r := output.NewRegistry()
	assert.Panics(t, func() {
		r.Register(fakeFormatter{key: ""})
	})
	assert.Panics(t, func() {
		r.Override(fakeFormatter{key: ""})
	})
}

func TestRegistry_KeysSorted(t *testing.T) {
	r := output.NewRegistry()
	r.Register(fakeFormatter{key: "yaml"})
	r.Register(fakeFormatter{key: "csv"})
	r.Register(fakeFormatter{key: "json"})
	assert.Equal(t, []string{"csv", "json", "yaml"}, r.Keys())
}

func TestRegistry_FormattersStableOrder(t *testing.T) {
	r := output.NewRegistry()
	r.Register(fakeFormatter{key: "zeta"})
	r.Register(fakeFormatter{key: "alpha"})
	fs := r.Formatters()
	require.Len(t, fs, 2)
	assert.Equal(t, "alpha", fs[0].Key())
	assert.Equal(t, "zeta", fs[1].Key())
}

func TestRegistry_ExtensionMap(t *testing.T) {
	r := output.NewRegistry()
	r.Register(fakeFormatter{key: "csv", exts: []string{".csv"}})
	r.Register(fakeFormatter{key: "yaml", exts: []string{".yaml", ".yml"}})
	m := r.ExtensionMap()
	assert.Equal(t, "csv", m[".csv"])
	assert.Equal(t, "yaml", m[".yaml"])
	assert.Equal(t, "yaml", m[".yml"])
}

func TestRegistry_IsolatedRegistries(t *testing.T) {
	a := output.NewRegistry()
	b := output.NewRegistry()
	a.Register(fakeFormatter{key: "only-a"})
	_, okA := a.Lookup("only-a")
	_, okB := b.Lookup("only-a")
	assert.True(t, okA)
	assert.False(t, okB)
}

func TestDefault_HasBuiltins(t *testing.T) {
	for _, k := range []string{"json", "yaml", "table"} {
		_, ok := output.Default.Lookup(k)
		assert.True(t, ok, "Default registry must contain built-in %q", k)
	}
}
