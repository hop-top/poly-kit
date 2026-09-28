package api_test

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"hop.top/kit/go/transport/api"
)

// tierSourceDescriptors is one invocable command per way a tier can
// be arrived at. Three of them project onto the same `write` class,
// which is the point: the class alone cannot tell them apart.
func tierSourceDescriptors() []api.CommandDescriptor {
	return []api.CommandDescriptor{
		{
			Path: []string{"declared"}, SideEffect: api.SideEffectWrite,
			SideEffectSource: api.SideEffectSourceDeclared, Invocable: true,
		},
		{
			Path: []string{"silent"}, SideEffect: api.SideEffectWrite,
			SideEffectSource: api.SideEffectSourceUnannotated, Invocable: true,
		},
		{
			Path: []string{"delete"}, SideEffect: api.SideEffectDestructive,
			SideEffectSource: api.SideEffectSourceInferred, Invocable: true,
		},
		{
			Path: []string{"typo"}, SideEffect: api.SideEffectWrite,
			SideEffectSource: api.SideEffectSourceMalformed,
			Invocable:        false, Reason: "malformed-schema",
		},
		// Built by hand, with no source: the caller stated a class,
		// and that statement is the declaration.
		{Path: []string{"legacy"}, SideEffect: api.SideEffectRead, Invocable: true},
	}
}

// TestDiscoveryDistinguishesDeclaredFromGuessed pins that an agent
// reading /v1/commands can tell "the adopter said write" from "kit
// guessed write" — before this, both read `side_effect: write`.
func TestDiscoveryDistinguishesDeclaredFromGuessed(t *testing.T) {
	doc := api.BuildDiscoveryDocument(api.ProjectionConfig{Descriptors: tierSourceDescriptors()})

	raw, err := json.Marshal(doc)
	require.NoError(t, err)
	var wire struct {
		Commands []map[string]any `json:"commands"`
	}
	require.NoError(t, json.Unmarshal(raw, &wire))

	got := map[string]map[string]any{}
	for _, c := range wire.Commands {
		got[c["name"].(string)] = c
	}
	for name, want := range map[string][2]string{
		"declared": {"write", "declared"},
		"silent":   {"write", "unannotated"},
		"delete":   {"destructive", "inferred"},
		"typo":     {"write", "malformed"},
		"legacy":   {"read", "declared"},
	} {
		require.Contains(t, got, name)
		assert.Equal(t, want[0], got[name]["side_effect"], "%s side_effect", name)
		assert.Equal(t, want[1], got[name]["side_effect_source"],
			"%s must say where its side_effect came from", name)
	}
}

// TestOpenAPIOperationsCarrySideEffectSource pins the same distinction
// on the generated spec: a client generated from it must not have to
// fetch discovery to learn that a POST is kit's guess.
func TestOpenAPIOperationsCarrySideEffectSource(t *testing.T) {
	r := api.NewRouter(api.WithOpenAPI(api.OpenAPIConfig{Title: "Fixture", Version: "1.0.0"}))
	cfg := api.ProjectionConfig{Descriptors: tierSourceDescriptors(), Executor: &stubExecutor{}}
	api.MountCommandProjection(r, cfg)
	api.DescribeCommandProjection(r, cfg)
	doc := fetchSpec(t, r)
	assertOperationSources(t, doc)

	// The discovery schema publishes the closed set, so a generated
	// client gets an enum rather than a bare string.
	schemas := doc["components"].(map[string]any)["schemas"].(map[string]any)
	entry, ok := schemas["DiscoveryEntry"].(map[string]any)
	require.True(t, ok, "discovery entry schema must be registered")
	prop := entry["properties"].(map[string]any)["side_effect_source"].(map[string]any)
	assert.ElementsMatch(t,
		[]any{"declared", "inferred", "unannotated", "malformed"}, prop["enum"])
	assert.Contains(t, entry["required"], "side_effect_source")
}

// TestMinimalSpecCarriesSideEffectSource covers the tool without
// WithOpenAPI: the floor document must not drop the distinction.
func TestMinimalSpecCarriesSideEffectSource(t *testing.T) {
	r := api.NewRouter()
	cfg := api.ProjectionConfig{Descriptors: tierSourceDescriptors(), Executor: &stubExecutor{}}
	api.MountCommandProjection(r, cfg)
	api.MountMinimalProjectionSpec(r, cfg)
	assertOperationSources(t, fetchSpec(t, r))
}

func assertOperationSources(t *testing.T, doc map[string]any) {
	t.Helper()
	paths, ok := doc["paths"].(map[string]any)
	require.True(t, ok)
	for path, want := range map[string][3]string{
		"/v1/commands/declared": {"post", "write", "declared"},
		"/v1/commands/silent":   {"post", "write", "unannotated"},
		"/v1/commands/delete":   {"post", "destructive", "inferred"},
		"/v1/commands/legacy":   {"get", "read", "declared"},
	} {
		entry, found := paths[path].(map[string]any)
		require.True(t, found, "spec must describe %s", path)
		op, found := entry[want[0]].(map[string]any)
		require.True(t, found, "%s must be %s", path, want[0])
		assert.Equal(t, want[1], op[api.OpenAPIExtSideEffect], "%s class", path)
		assert.Equal(t, want[2], op[api.OpenAPIExtSideEffectSource], "%s source", path)
	}
	assert.NotContains(t, paths, "/v1/commands/typo", "a malformed command is withheld")
}

// TestDiscoveryResponseCarriesSourceOverHTTP drives the served
// endpoint, not only the builder, so the field is proven on the wire.
func TestDiscoveryResponseCarriesSourceOverHTTP(t *testing.T) {
	r := api.NewRouter()
	api.MountCommandProjection(r, api.ProjectionConfig{
		Descriptors: tierSourceDescriptors(), Executor: &stubExecutor{},
	})
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, api.CommandProjectionPrefix, nil))
	require.Equal(t, http.StatusOK, rec.Code)

	var doc api.DiscoveryDocument
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &doc))
	for _, e := range doc.Commands {
		if e.Name == "silent" {
			assert.Equal(t, api.SideEffectSourceUnannotated, e.SideEffectSource)
			return
		}
	}
	t.Fatal("silent missing from discovery")
}
