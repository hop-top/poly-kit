package api_test

import (
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"hop.top/kit/go/transport/api"
)

// cacheSpecDescriptors is a cacheable read beside a plain read and a
// write. cacheable sets Cacheable on the read that takes it and on the
// write, which is projected onto POST and must ignore it.
func cacheSpecDescriptors(cacheable bool) []api.CommandDescriptor {
	return []api.CommandDescriptor{
		{
			Path: []string{"cached"}, Summary: "Cached read",
			SideEffect: api.SideEffectRead, Invocable: true, Cacheable: cacheable,
			Flags: []api.CommandFlag{{Name: "limit", Type: "int"}},
		},
		{
			Path: []string{"plain"}, Summary: "Plain read",
			SideEffect: api.SideEffectRead, Invocable: true,
			Flags: []api.CommandFlag{{Name: "limit", Type: "int"}},
		},
		{
			Path: []string{"write"}, Summary: "Write",
			SideEffect: api.SideEffectWrite, Invocable: true, Cacheable: cacheable,
		},
	}
}

// cacheSpecs builds the spec each generator serves for descs, keyed by
// generator. Both routers stream, so the stream operations are in it.
func cacheSpecs(t *testing.T, descs []api.CommandDescriptor) map[string]map[string]any {
	t.Helper()
	cfg := api.ProjectionConfig{Descriptors: descs, Executor: &streamExecutor{}, ToolName: "fix"}

	full := api.NewRouter(api.WithOpenAPI(api.OpenAPIConfig{Title: "Fixture", Version: "1.0.0"}))
	api.MountCommandProjection(full, cfg)
	api.DescribeCommandProjection(full, cfg)

	minimal := api.NewRouter()
	api.MountCommandProjection(minimal, cfg)
	api.MountMinimalProjectionSpec(minimal, cfg)

	return map[string]map[string]any{
		"huma":    fetchSpec(t, full),
		"minimal": fetchSpec(t, minimal),
	}
}

// specOp returns the operation at path and method, failing when absent.
func specOp(t *testing.T, doc map[string]any, path, method string) map[string]any {
	t.Helper()
	entry, ok := doc["paths"].(map[string]any)[path].(map[string]any)
	require.True(t, ok, "spec must describe %s", path)
	op, ok := entry[method].(map[string]any)
	require.True(t, ok, "%s must be %s", path, method)
	return op
}

// cacheParts is what an operation says about the result cache: its
// If-None-Match parameter, the headers on 200, and the 304 response.
// Absent parts are nil.
func cacheParts(op map[string]any) map[string]any {
	out := map[string]any{}
	params, _ := op["parameters"].([]any)
	for _, p := range params {
		if pm, _ := p.(map[string]any); pm["in"] == "header" && pm["name"] == "If-None-Match" {
			out["if-none-match"] = pm
		}
	}
	responses, _ := op["responses"].(map[string]any)
	if ok, _ := responses["200"].(map[string]any); ok != nil {
		out["200-headers"] = ok["headers"]
	}
	out["304"] = responses["304"]
	return out
}

// TestOpenAPICacheableReadDeclaresRevalidation pins that a GET the
// result cache answers is described with everything a client needs to
// revalidate: If-None-Match in, ETag and Cache-Control out, and 304.
func TestOpenAPICacheableReadDeclaresRevalidation(t *testing.T) {
	for name, doc := range cacheSpecs(t, cacheSpecDescriptors(true)) {
		t.Run(name, func(t *testing.T) {
			parts := cacheParts(specOp(t, doc, "/v1/commands/cached", "get"))

			inm, ok := parts["if-none-match"].(map[string]any)
			require.True(t, ok, "If-None-Match must be a header parameter")
			assert.NotEqual(t, true, inm["required"], "If-None-Match is optional")

			headers, ok := parts["200-headers"].(map[string]any)
			require.True(t, ok, "200 must declare its headers")
			assert.Contains(t, headers, "ETag")
			assert.Contains(t, headers, "Cache-Control")

			nm, ok := parts["304"].(map[string]any)
			require.True(t, ok, "304 must be declared")
			assert.NotContains(t, nm, "content", "304 carries no body")
			nmHeaders, ok := nm["headers"].(map[string]any)
			require.True(t, ok, "304 must declare its headers")
			assert.Contains(t, nmHeaders, "ETag")
			assert.Contains(t, nmHeaders, "Cache-Control")
		})
	}
}

// TestOpenAPICacheOnlyOnCacheableOperations pins that marking a command
// cacheable changes that command's GET operation and nothing else: not
// the other reads, not a write that claims it, not the stream routes,
// not the discovery operation or the components.
func TestOpenAPICacheOnlyOnCacheableOperations(t *testing.T) {
	without := cacheSpecs(t, cacheSpecDescriptors(false))
	with := cacheSpecs(t, cacheSpecDescriptors(true))
	for name := range without {
		t.Run(name, func(t *testing.T) {
			before, after := without[name], with[name]

			// Nothing in the spec without a cacheable command speaks
			// of the cache.
			for _, path := range []string{"/v1/commands/cached", "/v1/commands/plain"} {
				parts := cacheParts(specOp(t, before, path, "get"))
				assert.Nil(t, parts["if-none-match"], "%s: If-None-Match", path)
				assert.Nil(t, parts["200-headers"], "%s: 200 headers", path)
				assert.Nil(t, parts["304"], "%s: 304", path)
			}
			parts := cacheParts(specOp(t, before, "/v1/commands/write", "post"))
			assert.Nil(t, parts["304"], "write: 304")

			// Everything but the cacheable GET is byte-identical.
			delete(before["paths"].(map[string]any)["/v1/commands/cached"].(map[string]any), "get")
			delete(after["paths"].(map[string]any)["/v1/commands/cached"].(map[string]any), "get")
			assert.Equal(t, mustJSON(t, before), mustJSON(t, after))
		})
	}
}

// TestOpenAPICacheDescribedAlikeByBothGenerators pins that the tool
// without WithOpenAPI tells a client the same about the cache as the
// tool with it.
func TestOpenAPICacheDescribedAlikeByBothGenerators(t *testing.T) {
	specs := cacheSpecs(t, cacheSpecDescriptors(true))
	humaParts := cacheParts(specOp(t, specs["huma"], "/v1/commands/cached", "get"))
	minimalParts := cacheParts(specOp(t, specs["minimal"], "/v1/commands/cached", "get"))
	require.NotNil(t, humaParts["304"], "the huma spec must declare 304")
	assert.Equal(t, mustJSON(t, humaParts), mustJSON(t, minimalParts))
}

func mustJSON(t *testing.T, v any) string {
	t.Helper()
	raw, err := json.Marshal(v)
	require.NoError(t, err)
	return string(raw)
}
