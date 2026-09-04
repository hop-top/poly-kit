package api

import (
	"encoding/json"
	"net/http"
	"reflect"

	"github.com/danielgtaylor/huma/v2"
)

// DescribeCommandProjection adds one OpenAPI operation per projected
// command, plus the discovery operation, to the router's spec.
//
// It only DESCRIBES. The HTTP handlers are registered by
// MountCommandProjection through the raw router, so the router's
// middleware chain wraps every call uniformly; registering handlers
// through huma as well would install a second, unwrapped path to the
// same command.
//
// A router without WithOpenAPI has no huma.API, and this is then a
// no-op — projection still mounts and still serves. See
// MinimalProjectionSpec for what such a tool serves instead.
func DescribeCommandProjection(r *Router, cfg ProjectionConfig) {
	if r == nil {
		return
	}
	a := HumaAPI(r)
	if a == nil {
		return
	}
	spec := a.OpenAPI()
	if spec == nil {
		return
	}

	describeDiscoveryOp(spec)
	for _, d := range cfg.Descriptors {
		if !d.Invocable {
			continue
		}
		describeCommandOp(spec, d)
	}
}

// describeDiscoveryOp adds the discovery endpoint to the spec,
// including the reason vocabulary as an enum so a generated client
// gets the closed set rather than a bare string.
func describeDiscoveryOp(spec *huma.OpenAPI) {
	reg := registryOf(spec)
	schema := schemaFor(reg, reflect.TypeOf(DiscoveryDocument{}), "DiscoveryDocument")
	spec.AddOperation(&huma.Operation{
		OperationID: OperationIDFor(nil),
		Method:      http.MethodGet,
		Path:        CommandProjectionPrefix,
		Summary:     "List every command, mounted or withheld",
		Description: "Lists every reflected command. Entries with " +
			"invocable=false are not mounted and carry the stable " +
			"reason they were withheld.",
		Tags: []string{"commands"},
		Responses: map[string]*huma.Response{
			"200": {
				Description: "Command listing",
				Content: map[string]*huma.MediaType{
					"application/json": {Schema: schema},
				},
			},
		},
	})
}

// describeCommandOp adds one projected command to the spec.
func describeCommandOp(spec *huma.OpenAPI, d CommandDescriptor) {
	op := &huma.Operation{
		OperationID: OperationIDFor(d.Path),
		Method:      d.Method(),
		Path:        d.Route(),
		Summary:     summaryOf(d),
		Description: d.Description,
		Tags:        []string{"commands"},
		Responses:   commandResponses(spec, d),
	}

	if d.RequiresConfirmation {
		op.Parameters = append(op.Parameters, &huma.Param{
			Name:     ConfirmTokenHeader,
			In:       "header",
			Required: true,
			Description: "Confirmation token; this command is gated on " +
				"explicit confirmation.",
			Schema: &huma.Schema{Type: "string"},
		})
	}

	// Where the parameters go mirrors decodeCommandRequest: a GET
	// carries them in the query, a POST in the body.
	if d.Method() == http.MethodGet {
		op.Parameters = append(op.Parameters, queryParamsFor(d)...)
	} else {
		op.RequestBody = requestBodyFor(d)
	}
	spec.AddOperation(op)
}

// queryParamsFor renders a read command's flags and args as query
// parameters.
func queryParamsFor(d CommandDescriptor) []*huma.Param {
	var out []*huma.Param
	for _, f := range d.sortedFlags() {
		out = append(out, &huma.Param{
			Name:        f.Name,
			In:          "query",
			Required:    f.Required,
			Description: f.Description,
			Schema:      flagSchema(f),
		})
	}
	if len(d.Args) > 0 {
		out = append(out, &huma.Param{
			Name:        "arg",
			In:          "query",
			Required:    hasRequiredArg(d),
			Description: "Positional argument; repeat in order: " + argNames(d),
			Schema: &huma.Schema{
				Type:  "array",
				Items: &huma.Schema{Type: "string"},
			},
		})
	}
	return out
}

// requestBodyFor renders a write command's flags and args as a JSON
// body schema.
func requestBodyFor(d CommandDescriptor) *huma.RequestBody {
	props := map[string]*huma.Schema{}
	var required []string
	for _, f := range d.sortedFlags() {
		props[f.Name] = flagSchema(f)
		if f.Required {
			required = append(required, f.Name)
		}
	}

	body := &huma.Schema{
		Type: "object",
		Properties: map[string]*huma.Schema{
			"flags": {
				Type:       "object",
				Properties: props,
				Required:   required,
			},
			"args": {
				Type:        "array",
				Items:       &huma.Schema{Type: "string"},
				Description: argNames(d),
			},
		},
	}
	return &huma.RequestBody{
		Description: "Command flags and positional arguments",
		Required:    hasRequiredArg(d) || len(required) > 0,
		Content: map[string]*huma.MediaType{
			"application/json": {Schema: body},
		},
	}
}

// commandResponses builds the response map, using the command's
// declared output schema for 200 when it has one.
func commandResponses(spec *huma.OpenAPI, d CommandDescriptor) map[string]*huma.Response {
	reg := registryOf(spec)
	ok := schemaFor(reg, reflect.TypeOf(CommandResult{}), "CommandResult")

	// An adopter that declared an output schema gets it on the
	// data field, which is the whole point of declaring one: a
	// generated client should see the command's real shape, not
	// `data: any`.
	if len(d.OutputSchema) > 0 {
		if declared := parseDeclaredSchema(d.OutputSchema); declared != nil {
			ok = &huma.Schema{
				Type: "object",
				Properties: map[string]*huma.Schema{
					"exit_code": {Type: "integer"},
					"data":      declared,
					"stdout":    {Type: "string"},
					"stderr":    {Type: "string"},
				},
			}
		}
	}

	errSchema := schemaFor(reg, reflect.TypeOf(APIError{}), "APIError")
	return map[string]*huma.Response{
		"200": {
			Description: "Command completed",
			Content:     map[string]*huma.MediaType{"application/json": {Schema: ok}},
		},
		"400": {
			Description: "Malformed request, or the command exited USAGE",
			Content:     map[string]*huma.MediaType{"application/json": {Schema: errSchema}},
		},
		"403": {
			Description: "Refused by policy, or the command exited UNAUTHORIZED",
			Content:     map[string]*huma.MediaType{"application/json": {Schema: errSchema}},
		},
		"500": {
			Description: "Command failed",
			Content:     map[string]*huma.MediaType{"application/json": {Schema: errSchema}},
		},
	}
}

// parseDeclaredSchema turns adopter-declared JSON Schema bytes into a
// huma schema. Malformed bytes yield nil, and the caller falls back
// to the generic result shape: a broken declaration should degrade
// the spec, never break spec generation.
func parseDeclaredSchema(raw []byte) *huma.Schema {
	var s huma.Schema
	if err := json.Unmarshal(raw, &s); err != nil {
		return nil
	}
	return &s
}

// flagSchema maps a pflag type name onto a JSON Schema type.
func flagSchema(f CommandFlag) *huma.Schema {
	s := &huma.Schema{Type: "string", Description: f.Description}
	switch f.Type {
	case "bool":
		s.Type = "boolean"
	case "int", "int8", "int16", "int32", "int64",
		"uint", "uint8", "uint16", "uint32", "uint64", "count":
		s.Type = "integer"
	case "float32", "float64":
		s.Type = "number"
	case "stringSlice", "stringArray":
		s.Type = "array"
		s.Items = &huma.Schema{Type: "string"}
	}
	if f.Default != "" {
		s.Default = f.Default
	}
	return s
}

// summaryOf returns the operation summary, marking a destructive
// command so the danger is visible in a generated client's method
// list rather than only in prose.
func summaryOf(d CommandDescriptor) string {
	s := d.Summary
	if s == "" {
		s = "Invoke " + d.PathKey()
	}
	if d.SideEffect == SideEffectDestructive {
		s = "[destructive] " + s
	}
	return s
}

// hasRequiredArg reports whether any declared positional is required.
func hasRequiredArg(d CommandDescriptor) bool {
	for _, a := range d.Args {
		if a.Required {
			return true
		}
	}
	return false
}

// argNames renders the declared positional names in order, marking
// optional ones, so a spec reader learns the order the array takes.
func argNames(d CommandDescriptor) string {
	if len(d.Args) == 0 {
		return ""
	}
	out := "Positional arguments in order: "
	for i, a := range d.Args {
		if i > 0 {
			out += ", "
		}
		out += a.Name
		if !a.Required {
			out += "?"
		}
	}
	return out
}

// registryOf returns the spec's schema registry, or nil.
func registryOf(spec *huma.OpenAPI) huma.Registry {
	if spec.Components == nil {
		return nil
	}
	return spec.Components.Schemas
}

// schemaFor returns a schema for t, registering it under hint when a
// registry is present and falling back to an inline schema otherwise.
func schemaFor(reg huma.Registry, t reflect.Type, hint string) *huma.Schema {
	if reg != nil {
		return reg.Schema(t, true, hint)
	}
	return huma.SchemaFromType(nil, t)
}
