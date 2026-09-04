package api_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"hop.top/kit/go/transport/api"
)

// fixtureDescriptors is the projection input every test in this file
// works from. Each entry exists to trip exactly one rule, and its
// name says which, so a failure names the rule.
func fixtureDescriptors() []api.CommandDescriptor {
	return []api.CommandDescriptor{
		{
			Path:       []string{"list"},
			Summary:    "list things",
			SideEffect: api.SideEffectRead,
			Flags: []api.CommandFlag{
				{Name: "limit", Type: "int", Default: "10"},
				{Name: "all", Type: "bool"},
				{Name: "filter", Type: "string", Required: true},
			},
			Invocable: true,
		},
		{
			Path:       []string{"widget", "add"},
			Summary:    "add a widget",
			SideEffect: api.SideEffectWrite,
			Flags:      []api.CommandFlag{{Name: "force", Type: "bool"}},
			Args: []api.CommandArg{
				{Name: "name", Required: true},
				{Name: "note"},
			},
			Invocable: true,
		},
		{
			Path:                 []string{"widget", "delete"},
			Summary:              "delete a widget",
			SideEffect:           api.SideEffectDestructive,
			Invocable:            true,
			RequiresConfirmation: true,
		},
		{
			Path:       []string{"shell"},
			Summary:    "interactive shell",
			SideEffect: api.SideEffectInteractive,
			Invocable:  false,
			Reason:     "interactive",
		},
		{
			Path:       []string{"nuke"},
			Summary:    "destroy everything",
			SideEffect: api.SideEffectDestructive,
			Invocable:  false,
			Reason:     "unauthorized-destructive",
		},
		{
			Path:       []string{"secret"},
			Summary:    "internal",
			SideEffect: api.SideEffectRead,
			Invocable:  false,
			Reason:     "hidden-internal",
		},
	}
}

// stubExecutor records the last request and returns a canned result.
type stubExecutor struct {
	got    api.CommandRequest
	result api.CommandResult
	err    error
	calls  int
}

func (s *stubExecutor) Execute(
	_ context.Context, req api.CommandRequest,
) (api.CommandResult, error) {
	s.calls++
	s.got = req
	return s.result, s.err
}

func newProjectedRouter(t *testing.T, ex api.CommandExecutor) *api.Router {
	t.Helper()
	r := api.NewRouter()
	api.MountCommandProjection(r, api.ProjectionConfig{
		Descriptors: fixtureDescriptors(),
		Executor:    ex,
		ToolName:    "fix",
		ToolVersion: "1.0.0",
	})
	return r
}

func TestMethodFor(t *testing.T) {
	// Table pins the method rule. Flipping any row here is the
	// mutation that must turn this test red.
	cases := []struct {
		class api.SideEffectClass
		want  string
	}{
		{api.SideEffectRead, http.MethodGet},
		{api.SideEffectWrite, http.MethodPost},
		{api.SideEffectDestructive, http.MethodPost},
		{api.SideEffectInteractive, http.MethodPost},
	}
	for _, c := range cases {
		t.Run(string(c.class), func(t *testing.T) {
			assert.Equal(t, c.want, api.MethodFor(c.class))
		})
	}
}

func TestRouteFor(t *testing.T) {
	assert.Equal(t, "/v1/commands", api.RouteFor(nil))
	assert.Equal(t, "/v1/commands/list", api.RouteFor([]string{"list"}))
	assert.Equal(t, "/v1/commands/widget/add",
		api.RouteFor([]string{"widget", "add"}))
}

func TestOperationIDFor(t *testing.T) {
	assert.Equal(t, "commands_discover", api.OperationIDFor(nil))
	assert.Equal(t, "commands_widget_add",
		api.OperationIDFor([]string{"widget", "add"}))
	// Hyphens would be invalid in a generated client's identifier.
	assert.Equal(t, "commands_do_thing",
		api.OperationIDFor([]string{"do-thing"}))
}

func TestMountsInvocableCommandsOnly(t *testing.T) {
	r := newProjectedRouter(t, &stubExecutor{})

	// Invocable commands answer on their declared method.
	mounted := []struct {
		method, path string
	}{
		{http.MethodGet, "/v1/commands/list"},
		{http.MethodPost, "/v1/commands/widget/add"},
		{http.MethodPost, "/v1/commands/widget/delete"},
	}
	for _, m := range mounted {
		t.Run("mounted "+m.path, func(t *testing.T) {
			rec := httptest.NewRecorder()
			r.ServeHTTP(rec, httptest.NewRequest(m.method, m.path+"?arg=x", nil))
			assert.NotEqual(t, http.StatusNotFound, rec.Code,
				"invocable command must be mounted")
		})
	}

	// Non-invocable commands have no route at all.
	for _, path := range []string{
		"/v1/commands/shell", "/v1/commands/nuke", "/v1/commands/secret",
	} {
		t.Run("unmounted "+path, func(t *testing.T) {
			rec := httptest.NewRecorder()
			r.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, path, nil))
			assert.Equal(t, http.StatusNotFound, rec.Code,
				"non-invocable command must not be mounted")
		})
	}
}

func TestReadCommandRejectsPost(t *testing.T) {
	// A read is mounted as GET only: POSTing to it must not reach
	// the executor, or the method rule would be cosmetic.
	ex := &stubExecutor{}
	r := newProjectedRouter(t, ex)

	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/v1/commands/list", nil))
	assert.Equal(t, http.StatusMethodNotAllowed, rec.Code)
	assert.Zero(t, ex.calls, "executor must not run for the wrong method")
}

func TestDiscoveryListsEveryCommandWithReasons(t *testing.T) {
	r := newProjectedRouter(t, &stubExecutor{})

	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/v1/commands", nil))
	require.Equal(t, http.StatusOK, rec.Code)

	var doc api.DiscoveryDocument
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &doc))

	assert.Len(t, doc.Commands, len(fixtureDescriptors()),
		"discovery must list every command, mounted or not")

	byName := map[string]api.DiscoveryEntry{}
	for _, e := range doc.Commands {
		byName[e.Name] = e
	}

	// Invocable entries carry a callable method and route.
	list := byName["list"]
	assert.True(t, list.Invocable)
	assert.Empty(t, list.Reason)
	assert.Equal(t, http.MethodGet, list.Method)
	assert.Equal(t, "/v1/commands/list", list.Route)

	// Non-invocable entries carry the reason and NO route: an
	// advertised route that can only 404 is worse than none.
	for name, reason := range map[string]string{
		"shell":  "interactive",
		"nuke":   "unauthorized-destructive",
		"secret": "hidden-internal",
	} {
		e := byName[name]
		assert.False(t, e.Invocable, name)
		assert.Equal(t, reason, e.Reason, name)
		assert.Empty(t, e.Route, name+" must not advertise a route")
		assert.Empty(t, e.Method, name+" must not advertise a method")
	}

	assert.ElementsMatch(t,
		[]string{"interactive", "unauthorized-destructive", "hidden-internal"},
		doc.Reasons, "reason vocabulary must list each reason once")

	assert.Equal(t, "/v1/commands", doc.Prefix)
	assert.Equal(t, "fix", doc.Tool)
	assert.NotEmpty(t, doc.ExitStatus, "exit mapping must be published")
}

func TestDiscoveryOrdersInvocableFirst(t *testing.T) {
	r := newProjectedRouter(t, &stubExecutor{})
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/v1/commands", nil))

	var doc api.DiscoveryDocument
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &doc))

	seenNonInvocable := false
	for _, e := range doc.Commands {
		if !e.Invocable {
			seenNonInvocable = true
			continue
		}
		assert.False(t, seenNonInvocable,
			"invocable commands must precede withheld ones")
	}
}

func TestDiscoveryServedWithoutExecutor(t *testing.T) {
	// A tool with no bridge still describes its surface; it just
	// cannot run anything.
	r := api.NewRouter()
	api.MountCommandProjection(r, api.ProjectionConfig{
		Descriptors: fixtureDescriptors(),
	})

	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/v1/commands", nil))
	assert.Equal(t, http.StatusOK, rec.Code)

	rec = httptest.NewRecorder()
	r.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/v1/commands/list", nil))
	assert.Equal(t, http.StatusNotFound, rec.Code,
		"no executor means no command routes")
}
