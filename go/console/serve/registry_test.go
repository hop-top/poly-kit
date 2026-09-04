package serve_test

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"hop.top/kit/go/console/serve"
)

func TestRegistry_RegisterAndLookup(t *testing.T) {
	reg := serve.NewRegistry()
	reg.Register(fakeService{name: "api"})

	got, ok := reg.Lookup("api")

	require.True(t, ok)
	assert.Equal(t, "api", got.Name())
}

func TestRegistry_LookupMiss(t *testing.T) {
	reg := serve.NewRegistry()

	_, ok := reg.Lookup("api")

	assert.False(t, ok)
}

func TestRegistry_DuplicateNamePanics(t *testing.T) {
	reg := serve.NewRegistry()
	reg.Register(fakeService{name: "api"})

	assert.PanicsWithValue(
		t,
		`serve: service "api" already registered (use Override to replace)`,
		func() { reg.Register(fakeService{name: "api"}) },
	)
}

func TestRegistry_OverrideReplacesAndKeepsPosition(t *testing.T) {
	reg := serve.NewRegistry()
	reg.Register(fakeService{name: "api"})
	reg.Register(fakeService{name: "socket"})

	replacement := classifiedService{
		fakeService: fakeService{name: "api"},
		sideEffect:  "read",
		network:     "local-only",
	}
	reg.Override(replacement)

	got, ok := reg.Lookup("api")
	require.True(t, ok)
	assert.IsType(t, classifiedService{}, got)
	assert.Equal(t, []string{"api", "socket"}, reg.Names(),
		"override must not move the name to the end of the order")
	assert.Equal(t, 2, reg.Len())
}

func TestRegistry_OverrideRegistersNewName(t *testing.T) {
	reg := serve.NewRegistry()

	reg.Override(fakeService{name: "api"})

	_, ok := reg.Lookup("api")
	assert.True(t, ok)
	assert.Equal(t, []string{"api"}, reg.Names())
}

func TestRegistry_ListIsRegistrationOrder(t *testing.T) {
	reg := serve.NewRegistry()
	for _, n := range []string{"socket", "api", "mcp"} {
		reg.Register(fakeService{name: n})
	}

	assert.Equal(t, []string{"socket", "api", "mcp"}, reg.Names())

	list := reg.List()
	require.Len(t, list, 3)
	assert.Equal(t, "socket", list[0].Name())
	assert.Equal(t, "mcp", list[2].Name())
}

func TestRegistry_NamesIsACopy(t *testing.T) {
	reg := serve.NewRegistry()
	reg.Register(fakeService{name: "api"})

	names := reg.Names()
	names[0] = "mutated"

	assert.Equal(t, []string{"api"}, reg.Names())
}

func TestRegistry_InvalidNamePanics(t *testing.T) {
	tests := []struct {
		name string
		svc  string
	}{
		{"empty", ""},
		{"uppercase", "API"},
		{"underscore", "my_api"},
		{"leading digit", "1api"},
		{"leading hyphen", "-api"},
		{"reserved all", "all"},
		{"reserved none", "none"},
		{"reserved list", "list"},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			reg := serve.NewRegistry()
			assert.Panics(t, func() { reg.Register(fakeService{name: tc.svc}) })
			assert.Panics(t, func() { reg.Override(fakeService{name: tc.svc}) },
				"Override lifts the collision rule, not the grammar")
		})
	}
}

func TestRegistry_NilServicePanics(t *testing.T) {
	reg := serve.NewRegistry()

	assert.Panics(t, func() { reg.Register(nil) })
	assert.Panics(t, func() { reg.Override(nil) })
}

func TestValidateName(t *testing.T) {
	tests := []struct {
		name    string
		wantErr bool
	}{
		{"api", false},
		{"socket", false},
		{"my-api", false},
		{"a1", false},
		{"", true},
		{"API", true},
		{"my_api", true},
		{"1api", true},
		{"-api", true},
		{"api.v2", true},
		{"all", true},
		{"none", true},
		{"list", true},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			err := serve.ValidateName(tc.name)
			if tc.wantErr {
				assert.Error(t, err)
				return
			}
			assert.NoError(t, err)
		})
	}
}

func TestIsReservedName(t *testing.T) {
	assert.True(t, serve.IsReservedName("all"))
	assert.True(t, serve.IsReservedName("none"))
	assert.True(t, serve.IsReservedName("list"))
	assert.False(t, serve.IsReservedName("api"))
}
