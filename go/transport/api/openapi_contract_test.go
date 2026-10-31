package api_test

import (
	"encoding/json"
	"flag"
	"net/http"
	"os"
	"path/filepath"
	"testing"

	"github.com/danielgtaylor/huma/v2"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"hop.top/kit/go/transport/api"
)

var updateAPIContract = flag.Bool("update-api-contract", false,
	"regenerate the expected documents in contracts/parity/api.json from huma")

const apiContractPath = "../../../contracts/parity/api.json"

// oasInput is the language-neutral model of an OpenAPI document the
// Rust emitter also takes: the contract is that both build the same
// document from it.
type oasInput struct {
	Title       string                     `json:"title"`
	Version     string                     `json:"version"`
	Description string                     `json:"description"`
	Servers     []api.ServerConfig         `json:"-"`
	RawServers  []oasServer                `json:"servers"`
	Schemas     map[string]json.RawMessage `json:"schemas"`
	Operations  []oasOperation             `json:"operations"`
}

type oasServer struct {
	URL         string `json:"url"`
	Description string `json:"description"`
}

type oasOperation struct {
	OperationID string                     `json:"operation_id"`
	Method      string                     `json:"method"`
	Path        string                     `json:"path"`
	Summary     string                     `json:"summary"`
	Description string                     `json:"description"`
	Tags        []string                   `json:"tags"`
	Parameters  []oasParam                 `json:"parameters"`
	RequestBody *oasBody                   `json:"request_body"`
	Responses   map[string]oasResponse     `json:"responses"`
	Extensions  map[string]json.RawMessage `json:"extensions"`
}

type oasParam struct {
	Name        string          `json:"name"`
	In          string          `json:"in"`
	Required    bool            `json:"required"`
	Description string          `json:"description"`
	Schema      json.RawMessage `json:"schema"`
}

type oasBody struct {
	Description string          `json:"description"`
	Required    bool            `json:"required"`
	Schema      json.RawMessage `json:"schema"`
}

type oasResponse struct {
	Description string          `json:"description"`
	Schema      json.RawMessage `json:"schema"`
}

func mustSchema(t *testing.T, raw json.RawMessage) *huma.Schema {
	t.Helper()
	if len(raw) == 0 {
		return nil
	}
	var s huma.Schema
	require.NoError(t, json.Unmarshal(raw, &s))
	return &s
}

// buildHuma renders in through huma, the Go implementation of record.
func buildHuma(t *testing.T, in oasInput) json.RawMessage {
	t.Helper()
	cfg := api.OpenAPIConfig{Title: in.Title, Version: in.Version, Description: in.Description}
	for _, s := range in.RawServers {
		cfg.Servers = append(cfg.Servers, api.ServerConfig{URL: s.URL, Description: s.Description})
	}
	r := api.NewRouter(api.WithOpenAPI(cfg))
	spec := api.HumaAPI(r).OpenAPI()
	// The $schema link rewrite is huma's typed-handler convenience; the
	// contract is the plain document, which a kit-owned emitter can match.
	spec.OnAddOperation = nil
	for name, raw := range in.Schemas {
		spec.Components.Schemas.Map()[name] = mustSchema(t, raw)
	}
	for _, o := range in.Operations {
		op := &huma.Operation{
			OperationID: o.OperationID,
			Method:      o.Method,
			Path:        o.Path,
			Summary:     o.Summary,
			Description: o.Description,
			Tags:        o.Tags,
		}
		for _, p := range o.Parameters {
			op.Parameters = append(op.Parameters, &huma.Param{
				Name: p.Name, In: p.In, Required: p.Required,
				Description: p.Description, Schema: mustSchema(t, p.Schema),
			})
		}
		if o.RequestBody != nil {
			op.RequestBody = &huma.RequestBody{
				Description: o.RequestBody.Description,
				Required:    o.RequestBody.Required,
				Content: map[string]*huma.MediaType{
					"application/json": {Schema: mustSchema(t, o.RequestBody.Schema)},
				},
			}
		}
		op.Responses = map[string]*huma.Response{}
		for code, resp := range o.Responses {
			hr := &huma.Response{Description: resp.Description}
			if len(resp.Schema) > 0 {
				hr.Content = map[string]*huma.MediaType{
					"application/json": {Schema: mustSchema(t, resp.Schema)},
				}
			}
			op.Responses[code] = hr
		}
		if len(o.Extensions) > 0 {
			op.Extensions = map[string]any{}
			for k, v := range o.Extensions {
				var x any
				require.NoError(t, json.Unmarshal(v, &x))
				op.Extensions[k] = x
			}
		}
		spec.AddOperation(op)
	}
	rec := httpGet(t, r, "/openapi.json")
	return rec
}

func httpGet(t *testing.T, h http.Handler, path string) json.RawMessage {
	t.Helper()
	w := newRecorder()
	h.ServeHTTP(w, newGet(path))
	require.Equal(t, http.StatusOK, w.Code)
	return json.RawMessage(w.Body.Bytes())
}

// TestAPIContractOpenAPI pins the OpenAPI documents the Rust emitter
// must reproduce: for each case, the document huma serves for the
// input. With -update-api-contract it rewrites the expected documents.
func TestAPIContractOpenAPI(t *testing.T) {
	path := filepath.FromSlash(apiContractPath)
	raw, err := os.ReadFile(path)
	require.NoError(t, err)
	var file map[string]json.RawMessage
	require.NoError(t, json.Unmarshal(raw, &file))
	var cases []struct {
		Name     string          `json:"name"`
		Input    json.RawMessage `json:"input"`
		Expected json.RawMessage `json:"expected,omitempty"`
	}
	require.NoError(t, json.Unmarshal(file["openapi_cases"], &cases))
	require.NotEmpty(t, cases)

	for i := range cases {
		c := &cases[i]
		t.Run(c.Name, func(t *testing.T) {
			var in oasInput
			require.NoError(t, json.Unmarshal(c.Input, &in))
			got := buildHuma(t, in)
			if *updateAPIContract {
				c.Expected = got
				return
			}
			assert.JSONEq(t, string(c.Expected), string(got))
		})
	}
	if *updateAPIContract {
		casesJSON, err := json.Marshal(cases)
		require.NoError(t, err)
		file["openapi_cases"] = casesJSON
		out, err := json.MarshalIndent(file, "", "  ")
		require.NoError(t, err)
		require.NoError(t, os.WriteFile(path, append(out, '\n'), 0o644))
	}
}
