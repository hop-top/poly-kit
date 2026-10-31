package api_test

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"slices"
	"sort"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"hop.top/kit/go/transport/api"
)

// Runs the "projection" table of contracts/parity/api.json. Go is the
// authority; the Rust port projects the same tree from a kit CLI root.

type projectionContract struct {
	ExitStatus []struct {
		ExitCode int `json:"exit_code"`
		Status   int `json:"status"`
	} `json:"exit_status"`
	Tree []struct {
		Path       []string `json:"path"`
		Summary    string   `json:"summary"`
		SideEffect string   `json:"side_effect"`
		Source     string   `json:"source"`
		Flags      []struct {
			Name string `json:"name"`
			Type string `json:"type"`
		} `json:"flags"`
		Args []struct {
			Name     string `json:"name"`
			Required bool   `json:"required"`
		} `json:"args"`
		Invocable            bool   `json:"invocable"`
		Reason               string `json:"reason"`
		RequiresConfirmation bool   `json:"requires_confirmation"`
		RequiresConfirmToken bool   `json:"requires_confirm_token"`
	} `json:"tree"`
	Discovery struct {
		InvocableFirst []string `json:"invocable_first"`
		Withheld       []struct {
			Name   string `json:"name"`
			Reason string `json:"reason"`
		} `json:"withheld"`
		Reasons []string `json:"reasons"`
		Entries map[string]struct {
			Method           string `json:"method"`
			Route            string `json:"route"`
			StreamRoute      string `json:"stream_route"`
			SideEffect       string `json:"side_effect"`
			SideEffectSource string `json:"side_effect_source"`
			RequiresConfirm  bool   `json:"requires_confirmation"`
		} `json:"entries"`
	} `json:"discovery"`
	OpenAPI []struct {
		Path        string   `json:"path"`
		Method      string   `json:"method"`
		OperationID string   `json:"operation_id"`
		Query       []string `json:"query"`
		BodyFlags   []string `json:"body_flags"`
		SideEffect  string   `json:"side_effect"`
		Source      string   `json:"side_effect_source"`
		Responses   []string `json:"responses"`
	} `json:"openapi"`
	OpenAPIAbsent []string `json:"openapi_absent"`
	Cases         []struct {
		Name      string         `json:"name"`
		Method    string         `json:"method"`
		Path      string         `json:"path"`
		Body      map[string]any `json:"body"`
		Status    int            `json:"status"`
		ExitCode  *int           `json:"exit_code"`
		ErrorCode string         `json:"error_code"`
		Call      *struct {
			Path  []string          `json:"path"`
			Flags map[string]string `json:"flags"`
			Args  []string          `json:"args"`
		} `json:"call"`
	} `json:"cases"`
}

type contractExecutor struct{ got api.CommandRequest }

func (e *contractExecutor) Execute(_ context.Context, req api.CommandRequest) (api.CommandResult, error) {
	e.got = req
	code := 0
	if v, ok := req.Flags["exit"]; ok {
		switch n := v.(type) {
		case int64:
			code = int(n)
		case float64:
			code = int(n)
		}
	}
	return api.CommandResult{ExitCode: code, Stdout: "ran " + strings.Join(req.Path, " ")}, nil
}

func loadProjectionContract(t *testing.T) projectionContract {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join("..", "..", "..", "contracts", "parity", "api.json"))
	require.NoError(t, err)
	var f struct {
		Projection projectionContract `json:"projection"`
	}
	require.NoError(t, json.Unmarshal(raw, &f))
	require.NotEmpty(t, f.Projection.Cases)
	return f.Projection
}

func (c projectionContract) descriptors() []api.CommandDescriptor {
	var out []api.CommandDescriptor
	for _, n := range c.Tree {
		d := api.CommandDescriptor{
			Path:             n.Path,
			Summary:          n.Summary,
			SideEffect:       api.SideEffectClass(n.SideEffect),
			SideEffectSource: api.SideEffectSource(n.Source),
			Invocable:        n.Invocable,
			Reason:           n.Reason,

			RequiresConfirmation: n.RequiresConfirmation,
			RequiresConfirmToken: n.RequiresConfirmToken,
		}
		for _, f := range n.Flags {
			d.Flags = append(d.Flags, api.CommandFlag{Name: f.Name, Type: f.Type})
		}
		for _, a := range n.Args {
			d.Args = append(d.Args, api.CommandArg{Name: a.Name, Required: a.Required})
		}
		out = append(out, d)
	}
	return out
}

func TestAPIContractProjectionExitStatus(t *testing.T) {
	c := loadProjectionContract(t)
	for _, r := range c.ExitStatus {
		assert.Equal(t, r.Status, api.StatusForExitCode(r.ExitCode), "exit %d", r.ExitCode)
	}
	var got []int
	for _, p := range api.ExitStatusTable() {
		got = append(got, p.ExitCode)
	}
	var want []int
	for _, r := range c.ExitStatus {
		want = append(want, r.ExitCode)
	}
	assert.Equal(t, want, got)
}

func TestAPIContractProjectionCases(t *testing.T) {
	c := loadProjectionContract(t)
	for _, tc := range c.Cases {
		t.Run(tc.Name, func(t *testing.T) {
			ex := &contractExecutor{}
			r := api.NewRouter()
			api.MountCommandProjection(r, api.ProjectionConfig{
				Descriptors: c.descriptors(), Executor: ex, ToolName: "fix", ToolVersion: "1.0.0",
			})
			var body *strings.Reader
			if tc.Body != nil {
				b, _ := json.Marshal(tc.Body)
				body = strings.NewReader(string(b))
			} else {
				body = strings.NewReader("")
			}
			req := httptest.NewRequest(tc.Method, tc.Path, body)
			if tc.Body != nil {
				req.Header.Set("Content-Type", "application/json")
			}
			rec := httptest.NewRecorder()
			r.ServeHTTP(rec, req)
			require.Equal(t, tc.Status, rec.Code, rec.Body.String())
			var out map[string]any
			_ = json.Unmarshal(rec.Body.Bytes(), &out)
			if tc.ErrorCode != "" {
				assert.Equal(t, tc.ErrorCode, out["code"])
			}
			if tc.ExitCode != nil {
				assert.EqualValues(t, *tc.ExitCode, out["exit_code"])
			}
			if tc.Call != nil {
				assert.Equal(t, tc.Call.Path, ex.got.Path)
				flags := map[string]string{}
				for k, v := range ex.got.Flags {
					flags[k] = fmt.Sprint(v)
				}
				assert.Equal(t, tc.Call.Flags, flags)
				args := ex.got.Args
				if args == nil {
					args = []string{}
				}
				assert.Equal(t, tc.Call.Args, args)
			}
		})
	}
}

func TestAPIContractProjectionDiscovery(t *testing.T) {
	c := loadProjectionContract(t)
	doc := api.BuildDiscoveryDocument(api.ProjectionConfig{
		Descriptors: c.descriptors(), Executor: &contractExecutor{}, ToolName: "fix",
	})
	// Go's executor here does not stream, so the stream routes are
	// asserted from the descriptor.
	var order []string
	for _, e := range doc.Commands {
		if e.Invocable {
			order = append(order, e.Name)
		}
	}
	assert.Equal(t, c.Discovery.InvocableFirst, order)
	for _, w := range c.Discovery.Withheld {
		var found bool
		for _, e := range doc.Commands {
			if e.Name == w.Name {
				found = true
				assert.False(t, e.Invocable)
				assert.Equal(t, w.Reason, e.Reason)
				assert.Empty(t, e.Route)
				assert.Empty(t, e.Method)
			}
		}
		assert.True(t, found, w.Name)
	}
	assert.Equal(t, c.Discovery.Reasons, doc.Reasons)
	for name, want := range c.Discovery.Entries {
		for _, e := range doc.Commands {
			if e.Name != name {
				continue
			}
			assert.Equal(t, want.Method, e.Method, name)
			assert.Equal(t, want.Route, e.Route, name)
			assert.Equal(t, want.SideEffect, string(e.SideEffect), name)
			assert.Equal(t, want.SideEffectSource, string(e.SideEffectSource), name)
			assert.Equal(t, want.RequiresConfirm, e.RequiresConfirmation, name)
		}
	}
	for _, d := range c.descriptors() {
		if want, ok := c.Discovery.Entries[d.PathKey()]; ok {
			assert.Equal(t, want.StreamRoute, d.StreamRoute())
		}
	}
}

func TestAPIContractProjectionOpenAPI(t *testing.T) {
	c := loadProjectionContract(t)
	r := api.NewRouter(api.WithOpenAPI(api.OpenAPIConfig{Title: "Fixture", Version: "1.0.0"}))
	cfg := api.ProjectionConfig{Descriptors: c.descriptors(), Executor: &contractExecutor{}, ToolName: "fix"}
	api.MountCommandProjection(r, cfg)
	api.DescribeCommandProjection(r, cfg)
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/openapi.json", nil))
	require.Equal(t, http.StatusOK, rec.Code)
	var doc map[string]any
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &doc))
	paths := doc["paths"].(map[string]any)
	for _, want := range c.OpenAPI {
		op := paths[want.Path].(map[string]any)[want.Method].(map[string]any)
		assert.Equal(t, want.OperationID, op["operationId"], want.Path)
		if want.Query != nil {
			var names []string
			for _, p := range op["parameters"].([]any) {
				m := p.(map[string]any)
				assert.Equal(t, "query", m["in"])
				names = append(names, m["name"].(string))
			}
			sort.Strings(names)
			assert.Equal(t, want.Query, names, want.Path)
		}
		if want.BodyFlags != nil {
			rb := op["requestBody"].(map[string]any)["content"].(map[string]any)["application/json"].(map[string]any)
			props := rb["schema"].(map[string]any)["properties"].(map[string]any)["flags"].(map[string]any)
			names := []string{}
			if fp, ok := props["properties"].(map[string]any); ok {
				for k := range fp {
					names = append(names, k)
				}
			}
			sort.Strings(names)
			assert.Equal(t, want.BodyFlags, names, want.Path)
		}
		if want.SideEffect != "" {
			assert.Equal(t, want.SideEffect, op[api.OpenAPIExtSideEffect])
			assert.Equal(t, want.Source, op[api.OpenAPIExtSideEffectSource])
		}
		if want.Responses != nil {
			var codes []string
			for k := range op["responses"].(map[string]any) {
				codes = append(codes, k)
			}
			sort.Strings(codes)
			assert.True(t, slices.Equal(want.Responses, codes), "%s responses %v", want.Path, codes)
		}
	}
	for _, p := range c.OpenAPIAbsent {
		assert.NotContains(t, paths, p)
	}
}
