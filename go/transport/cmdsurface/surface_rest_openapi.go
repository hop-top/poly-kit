package cmdsurface

import (
	"fmt"
	"reflect"
	"strings"

	"github.com/danielgtaylor/huma/v2"
)

// WithRESTOpenAPI registers one OpenAPI operation per mounted leaf so
// the router's spec reflects the command surface. Pass the value
// returned by api.HumaAPI(router) — it must be a huma.API.
//
// The HTTP handler itself is registered by MountREST via the raw
// api.Router.Handle path so the per-leaf middleware (auth gate,
// confirmation gate, user-supplied) is preserved. WithRESTOpenAPI
// only adds the operation metadata to the spec; it does NOT install a
// second HTTP handler.
//
// When the argument is not a huma.API (e.g. nil because the caller's
// router has no WithOpenAPI), the option becomes a no-op.
func WithRESTOpenAPI(humaAPI any) RESTOption {
	return func(c *restConfig) {
		if humaAPI == nil {
			return
		}
		a, ok := humaAPI.(huma.API)
		if !ok {
			return
		}
		c.humaRegister = func(b *Bridge, leaf *Leaf, path string) {
			describeHumaLeafOp(a, leaf, path)
		}
	}
}

// describeHumaLeafOp adds an OpenAPI operation entry for the leaf at
// path to the spec, with no HTTP handler attached. The handler lives
// on the raw mux registered by MountREST so per-leaf middleware (auth,
// confirmation, user-supplied) wraps every call uniformly.
func describeHumaLeafOp(api huma.API, leaf *Leaf, path string) {
	spec := api.OpenAPI()
	if spec == nil {
		return
	}
	reg := spec.Components.Schemas
	op := &huma.Operation{
		OperationID: operationIDFor(leaf.Path),
		Method:      "POST",
		Path:        path,
		Summary:     summaryFor(leaf),
		Description: descriptionFor(leaf),
		Tags:        []string{"commands"},
		RequestBody: &huma.RequestBody{
			Description: "Invocation envelope",
			Required:    true,
			Content: map[string]*huma.MediaType{
				"application/json": {
					Schema: schemaForType(reg, reflect.TypeOf(Invocation{}), "Invocation"),
				},
			},
		},
		Responses: map[string]*huma.Response{
			"200": {
				Description: "Command result",
				Content: map[string]*huma.MediaType{
					"application/json": {
						Schema: schemaForType(reg, reflect.TypeOf(Result{}), "Result"),
					},
				},
			},
		},
	}
	spec.AddOperation(op)
}

// schemaForType returns a schema for t, registering it with reg under
// hint when reg is non-nil. Falls back to an inline schema when reg is
// nil (e.g. a huma.API without a schema registry attached).
func schemaForType(reg huma.Registry, t reflect.Type, hint string) *huma.Schema {
	if reg != nil {
		return reg.Schema(t, true, hint)
	}
	return huma.SchemaFromType(nil, t)
}

// operationIDFor returns the huma operationID for leaf path.
//
//	["widget","add"] → "cmd_widget_add"
func operationIDFor(path []string) string {
	if len(path) == 0 {
		return "cmd_root"
	}
	return "cmd_" + strings.Join(path, "_")
}

// summaryFor returns the leaf's huma summary line, prefixed with
// "[destructive] " when applicable.
func summaryFor(leaf *Leaf) string {
	s := ""
	if leaf.Cmd != nil {
		s = leaf.Cmd.Short
	}
	if s == "" {
		s = fmt.Sprintf("Invoke %s", strings.Join(leaf.Path, " "))
	}
	if leaf.Class.Destructive {
		s = "[destructive] " + s
	}
	return s
}

// descriptionFor returns Long when present, falling back to Short.
func descriptionFor(leaf *Leaf) string {
	if leaf.Cmd == nil {
		return ""
	}
	if leaf.Cmd.Long != "" {
		return leaf.Cmd.Long
	}
	return leaf.Cmd.Short
}
