package api

import (
	"net/http"
	"strings"
)

// OpenAPISpecPath is where the generated spec is served. It is huma's
// own default when WithOpenAPI is configured; the projection reuses
// the literal so a tool without WithOpenAPI serves its minimal spec
// at the same URL rather than inventing a second one a client would
// have to know about.
const OpenAPISpecPath = "/openapi.json"

// MountMinimalProjectionSpec serves a minimal OpenAPI document at
// OpenAPISpecPath describing the projected routes.
//
// It is for the tool that never called WithOpenAPI. Projection still
// mounts for such a tool — the routes work — and without this it
// would serve routes that no document describes, which is the one
// combination that leaves a caller with no way to discover the shape
// of what is running.
//
// When WithOpenAPI IS configured, huma owns OpenAPISpecPath and this
// is a no-op: registering here as well would collide on the pattern.
func MountMinimalProjectionSpec(r *Router, cfg ProjectionConfig) {
	if r == nil || HumaAPI(r) != nil {
		return
	}
	doc := buildMinimalSpec(cfg)
	r.Handle(http.MethodGet, OpenAPISpecPath, func(w http.ResponseWriter, _ *http.Request) {
		JSON(w, http.StatusOK, doc)
	})
}

// buildMinimalSpec assembles a hand-rolled OpenAPI 3.1 document. It
// is deliberately small: enough for a client to find every projected
// operation, its method, and its path. Full request and response
// schemas are what WithOpenAPI buys; this is the floor, not a
// substitute.
func buildMinimalSpec(cfg ProjectionConfig) map[string]any {
	paths := map[string]any{
		CommandProjectionPrefix: map[string]any{
			"get": map[string]any{
				"operationId": OperationIDFor(nil),
				"summary":     "List every command, mounted or withheld",
				"tags":        []string{"commands"},
				"responses": map[string]any{
					"200": map[string]any{"description": "Command listing"},
				},
			},
		},
	}

	for _, d := range cfg.Descriptors {
		if !d.Invocable {
			continue
		}
		method := strings.ToLower(d.Method())
		entry, ok := paths[d.Route()].(map[string]any)
		if !ok {
			entry = map[string]any{}
			paths[d.Route()] = entry
		}
		entry[method] = map[string]any{
			"operationId": OperationIDFor(d.Path),
			"summary":     summaryOf(d),
			"tags":        []string{"commands"},
			"responses": map[string]any{
				"200": map[string]any{"description": "Command completed"},
				"400": map[string]any{"description": "Malformed request, or the command exited USAGE"},
				"403": map[string]any{"description": "Refused by policy, or the command exited UNAUTHORIZED"},
				"500": map[string]any{"description": "Command failed"},
			},
		}
	}

	title := cfg.ToolName
	if title == "" {
		title = "Command projection"
	}
	version := cfg.ToolVersion
	if version == "" {
		version = "0.0.0"
	}
	return map[string]any{
		"openapi": "3.1.0",
		"info":    map[string]any{"title": title, "version": version},
		"paths":   paths,
	}
}
