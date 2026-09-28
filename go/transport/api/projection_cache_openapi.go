package api

import (
	"net/http"

	"github.com/danielgtaylor/huma/v2"
)

// Headers the result cache reads and writes on a cacheable GET route.
const (
	headerETag         = "ETag"
	headerCacheControl = "Cache-Control"
	headerIfNoneMatch  = "If-None-Match"
)

// What a cacheable operation's spec says about the cache. Both
// generators render these, so the tool without WithOpenAPI tells a
// client the same as the tool with it.
const (
	ifNoneMatchDescription = "An ETag this operation answered earlier. While it " +
		"still names the current result (weak comparison), or is `*`, the " +
		"answer is 304 Not Modified with no body."
	notModifiedDescription = "Not Modified: If-None-Match names the current " +
		"result. No body."
)

// cacheResponseHeaders are the headers a cacheable operation sets on
// 200 and on 304, in the order the spec lists them.
var cacheResponseHeaders = []struct{ name, description string }{
	{headerETag, "Weak validator of the result (`W/\"…\"`). Send it back " +
		"as If-None-Match to revalidate."},
	{headerCacheControl, "`private` when the call carried an established " +
		"principal, tenant or scopes, `public` otherwise; `max-age` is how " +
		"many seconds the result stays fresh."},
}

// cacheable reports whether d's operation answers from the result
// cache, and so declares the cache in the spec: a GET marked
// [CommandDescriptor.Cacheable].
func (d CommandDescriptor) cacheable() bool {
	return d.Cacheable && d.Method() == http.MethodGet
}

// describeCacheOp adds the result cache to a cacheable operation in the
// huma spec: the If-None-Match parameter, the ETag and Cache-Control
// headers on 200, and the 304 response. Any other operation is left
// untouched.
func describeCacheOp(op *huma.Operation, d CommandDescriptor) {
	if !d.cacheable() {
		return
	}
	op.Parameters = append(op.Parameters, &huma.Param{
		Name:        headerIfNoneMatch,
		In:          "header",
		Description: ifNoneMatchDescription,
		Schema:      &huma.Schema{Type: "string"},
	})
	if ok := op.Responses["200"]; ok != nil {
		ok.Headers = humaCacheHeaders()
	}
	op.Responses["304"] = &huma.Response{
		Description: notModifiedDescription,
		Headers:     humaCacheHeaders(),
	}
}

func humaCacheHeaders() map[string]*huma.Param {
	out := make(map[string]*huma.Param, len(cacheResponseHeaders))
	for _, h := range cacheResponseHeaders {
		out[h.name] = &huma.Param{Description: h.description, Schema: &huma.Schema{Type: "string"}}
	}
	return out
}

// describeCacheMinimal is describeCacheOp for the minimal spec.
func describeCacheMinimal(op map[string]any, d CommandDescriptor) {
	if !d.cacheable() {
		return
	}
	params, _ := op["parameters"].([]any)
	op["parameters"] = append(params, map[string]any{
		"name":        headerIfNoneMatch,
		"in":          "header",
		"description": ifNoneMatchDescription,
		"schema":      map[string]any{"type": "string"},
	})
	responses, _ := op["responses"].(map[string]any)
	if responses == nil {
		responses = map[string]any{}
		op["responses"] = responses
	}
	if ok, _ := responses["200"].(map[string]any); ok != nil {
		ok["headers"] = minimalCacheHeaders()
	}
	responses["304"] = map[string]any{
		"description": notModifiedDescription,
		"headers":     minimalCacheHeaders(),
	}
}

func minimalCacheHeaders() map[string]any {
	out := make(map[string]any, len(cacheResponseHeaders))
	for _, h := range cacheResponseHeaders {
		out[h.name] = map[string]any{
			"description": h.description,
			"schema":      map[string]any{"type": "string"},
		}
	}
	return out
}
