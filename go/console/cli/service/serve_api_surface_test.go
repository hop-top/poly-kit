package service

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"hop.top/kit/go/console/cli"
	"hop.top/kit/go/transport/api"
)

// echoSurface is the minimal extra surface: it mounts two routes on the
// shared router, runs a command through the executor it is handed, and
// declares one of them public.
type echoSurface struct {
	name   string
	public []PublicRoute
	// mount, when set, replaces the default mount.
	mount func(*api.Router, api.ProjectionConfig) error
	got   api.ProjectionConfig
}

func (s *echoSurface) Name() string { return s.name }

func (s *echoSurface) PublicRoutes() []PublicRoute { return s.public }

func (s *echoSurface) Mount(r *api.Router, p api.ProjectionConfig) error {
	s.got = p
	if s.mount != nil {
		return s.mount(r, p)
	}
	r.Handle(http.MethodGet, "/v1/echo/hello", func(w http.ResponseWriter, _ *http.Request) {
		api.JSON(w, http.StatusOK, map[string]string{"tool": p.ToolName, "version": p.ToolVersion})
	})
	r.Handle(http.MethodGet, "/v1/echo/run", func(w http.ResponseWriter, req *http.Request) {
		res, err := p.Executor.Execute(req.Context(), api.CommandRequest{Path: []string{"list"}})
		if err != nil {
			api.JSON(w, http.StatusBadGateway, map[string]string{"error": err.Error()})
			return
		}
		api.JSON(w, http.StatusOK, res)
	})
	return nil
}

func surfaceClient(t *testing.T, s *echoSurface, extra ...Surface) *http.Client {
	t.Helper()
	sock := shortSock(t)
	r := authRoot(t, WithAPI(APIConfig{
		Addr: "unix:" + sock, Auth: bearerAuth,
		Surfaces: append([]Surface{s}, extra...),
	}))
	_, stop := serveAPI(t, r)
	t.Cleanup(stop)
	return unixClient(sock)
}

// bearerAuth admits "Bearer <name>" and names the caller <name>.
func bearerAuth(r *http.Request) (any, error) {
	tok, ok := strings.CutPrefix(r.Header.Get("Authorization"), "Bearer ")
	if !ok || tok == "" {
		return nil, errors.New("missing bearer")
	}
	return api.Claims{Subject: tok, Tenant: "acme"}, nil
}

func authedDo(t *testing.T, c *http.Client, method, path, tok string) (int, []byte) {
	t.Helper()
	req, err := http.NewRequest(method, "http://localhost"+path, nil)
	require.NoError(t, err)
	if tok != "" {
		req.Header.Set("Authorization", "Bearer "+tok)
	}
	resp, err := c.Do(req)
	require.NoError(t, err)
	defer func() { _ = resp.Body.Close() }()
	b, _ := io.ReadAll(resp.Body)
	return resp.StatusCode, b
}

func TestAPISurface_DeclaredPublicRoutePassesAuth(t *testing.T) {
	s := &echoSurface{name: "echo", public: []PublicRoute{{http.MethodGet, "/v1/echo/hello"}}}
	c := surfaceClient(t, s)

	code, body := authedDo(t, c, http.MethodGet, "/v1/echo/hello", "")
	require.Equal(t, http.StatusOK, code, string(body))
	var got map[string]string
	require.NoError(t, json.Unmarshal(body, &got))
	assert.Equal(t, "tool", got["tool"])
	assert.Equal(t, "1.0.0", got["version"])
}

func TestAPISurface_UndeclaredRouteStaysGuarded(t *testing.T) {
	s := &echoSurface{name: "echo", public: []PublicRoute{{http.MethodGet, "/v1/echo/hello"}}}
	c := surfaceClient(t, s)

	code, _ := authedDo(t, c, http.MethodGet, "/v1/echo/run", "")
	assert.Equal(t, http.StatusUnauthorized, code, "a surface route not declared public is guarded")
	code, _ = authedDo(t, c, http.MethodGet, "/v1/commands", "")
	assert.Equal(t, http.StatusUnauthorized, code, "REST stays guarded beside a public surface route")
	// The exemption is for the declared method only.
	code, _ = authedDo(t, c, http.MethodPost, "/v1/echo/hello", "")
	assert.Equal(t, http.StatusUnauthorized, code)
	// A path that merely starts with the declared one is not declared.
	code, _ = authedDo(t, c, http.MethodGet, "/v1/echo/hello/x", "")
	assert.Equal(t, http.StatusUnauthorized, code)
}

func TestAPISurface_NothingPublicByDefault(t *testing.T) {
	c := surfaceClient(t, &echoSurface{name: "echo"})
	code, _ := authedDo(t, c, http.MethodGet, "/v1/echo/hello", "")
	assert.Equal(t, http.StatusUnauthorized, code)
	code, _ = authedDo(t, c, http.MethodGet, "/v1/echo/hello", "alice")
	assert.Equal(t, http.StatusOK, code, "a credentialed caller reaches the route")
}

func TestAPISurface_RunsCommandsThroughTheSharedExecutor(t *testing.T) {
	c := surfaceClient(t, &echoSurface{name: "echo"})
	code, body := authedDo(t, c, http.MethodGet, "/v1/echo/run", "alice")
	require.Equal(t, http.StatusOK, code, string(body))
	assert.Contains(t, string(body), "listed")
}

func TestAPISurface_ReceivesTheProjection(t *testing.T) {
	s := &echoSurface{name: "echo"}
	c := surfaceClient(t, s)
	_, _ = authedDo(t, c, http.MethodGet, "/healthz", "")
	assert.NotNil(t, s.got.Executor)
	assert.NotEmpty(t, s.got.Descriptors)
}

func TestAPISurface_CannotExemptARESTRoute(t *testing.T) {
	// /healthz is answered ahead of the router, so a surface cannot
	// mount it either; the declaration fails as not mounted.
	for p, why := range map[string]string{"/v1/commands": "already served", "/healthz": "not mounted"} {
		s := &echoSurface{name: "echo", public: []PublicRoute{{http.MethodGet, p}}}
		r := authRoot(t, WithAPI(APIConfig{
			Addr: "unix:" + shortSock(t), Auth: bearerAuth, Surfaces: []Surface{s},
		}))
		_, err := apiSvc(t, r).buildHandler(context.Background())
		require.Error(t, err, p)
		assert.Contains(t, err.Error(), why, p)
	}
}

func TestAPISurface_PublicRouteMustBeMounted(t *testing.T) {
	s := &echoSurface{name: "echo", public: []PublicRoute{{http.MethodGet, "/v1/echo/ghost"}}}
	r := authRoot(t, WithAPI(APIConfig{
		Addr: "unix:" + shortSock(t), Auth: bearerAuth, Surfaces: []Surface{s},
	}))
	_, err := apiSvc(t, r).buildHandler(context.Background())
	require.Error(t, err)
	assert.Contains(t, err.Error(), "not mounted")
}

func TestAPISurface_MountErrorAndPanicAreStartErrors(t *testing.T) {
	for name, mount := range map[string]func(*api.Router, api.ProjectionConfig) error{
		"error": func(*api.Router, api.ProjectionConfig) error { return errors.New("boom") },
		"panic": func(r *api.Router, _ api.ProjectionConfig) error {
			// Same pattern twice: the mux refuses with a panic.
			r.Handle(http.MethodGet, "/v1/echo/a", func(http.ResponseWriter, *http.Request) {})
			r.Handle(http.MethodGet, "/v1/echo/a", func(http.ResponseWriter, *http.Request) {})
			return nil
		},
	} {
		s := &echoSurface{name: "echo", mount: mount}
		r := authRoot(t, WithAPI(APIConfig{
			Addr: "unix:" + shortSock(t), Auth: bearerAuth, Surfaces: []Surface{s},
		}))
		_, err := apiSvc(t, r).buildHandler(context.Background())
		require.Error(t, err, name)
		assert.Contains(t, err.Error(), `surface "echo"`, name)
	}
}

func TestAPISurface_ValidateRefusesBadDeclarations(t *testing.T) {
	cases := map[string][]Surface{
		"nil":           {nil},
		"empty name":    {&echoSurface{}},
		"duplicate":     {&echoSurface{name: "a"}, &echoSurface{name: "a"}},
		"no method":     {&echoSurface{name: "a", public: []PublicRoute{{"", "/x"}}}},
		"relative path": {&echoSurface{name: "a", public: []PublicRoute{{http.MethodGet, "x"}}}},
		"wildcard":      {&echoSurface{name: "a", public: []PublicRoute{{http.MethodGet, "/x/{id}"}}}},
		"root":          {&echoSurface{name: "a", public: []PublicRoute{{http.MethodGet, "/"}}}},
		"query":         {&echoSurface{name: "a", public: []PublicRoute{{http.MethodGet, "/x?y=1"}}}},
	}
	for name, ss := range cases {
		r := authRoot(t, func(r *cli.Root) {
			WithAPI(APIConfig{Addr: "unix:" + shortSock(t), Surfaces: ss})(r)
		})
		assert.Error(t, apiSvc(t, r).Validate(), name)
	}
}
