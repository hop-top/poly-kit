package service

import (
	"context"
	"encoding/json"
	"io"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// unixClient talks HTTP over the socket at path. The URL host is
// "localhost": a unix listener answers to the loopback names.
func unixClient(path string) *http.Client {
	return &http.Client{Transport: &http.Transport{
		DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
			var d net.Dialer
			return d.DialContext(ctx, "unix", path)
		},
	}}
}

func shortSock(t *testing.T) string {
	t.Helper()
	dir, err := os.MkdirTemp("", "kd")
	require.NoError(t, err)
	t.Cleanup(func() { _ = os.RemoveAll(dir) })
	return filepath.Join(dir, "api.sock")
}

func unixDo(t *testing.T, c *http.Client, method, path, body string) (int, []byte) {
	t.Helper()
	req, err := http.NewRequest(method, "http://localhost"+path, strings.NewReader(body))
	require.NoError(t, err)
	if body != "" {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := c.Do(req)
	require.NoError(t, err)
	defer func() { _ = resp.Body.Close() }()
	b, _ := io.ReadAll(resp.Body)
	return resp.StatusCode, b
}

func TestAPIUnix_ServesOverTheSocket(t *testing.T) {
	sock := shortSock(t)
	r := healthRoot(t, APIConfig{Addr: "unix:" + sock})
	_, stop := serveAPI(t, r)
	defer stop()

	st, err := os.Stat(sock)
	require.NoError(t, err)
	assert.Equal(t, os.FileMode(0o600), st.Mode().Perm())

	code, body := unixDo(t, unixClient(sock), http.MethodGet, "/v1/commands", "")
	assert.Equal(t, http.StatusOK, code, string(body))
	assert.Contains(t, string(body), `"list"`)

	code, _ = unixDo(t, unixClient(sock), http.MethodGet, "/healthz", "")
	assert.Equal(t, http.StatusOK, code)
}

func TestAPIUnix_SocketRemovedOnStop(t *testing.T) {
	sock := shortSock(t)
	r := healthRoot(t, APIConfig{Addr: "unix:" + sock})
	_, stop := serveAPI(t, r)
	stop()
	_, err := os.Stat(sock)
	assert.True(t, os.IsNotExist(err), "socket file must go with the service: %v", err)
}

func TestAPIUnix_ValidateRefusesRelativePath(t *testing.T) {
	r := healthRoot(t, APIConfig{Addr: "unix:relative.sock"})
	err := apiSvc(t, r).Validate()
	require.Error(t, err)
	assert.Contains(t, err.Error(), "absolute")
}

func TestAPIUnix_NeedsNoAuthLikeLoopback(t *testing.T) {
	r := healthRoot(t, APIConfig{Addr: "unix:" + shortSock(t)})
	assert.NoError(t, apiSvc(t, r).Validate())
}

func dekRoot(t *testing.T, sock string) *http.Client {
	t.Helper()
	r := healthRoot(t, APIConfig{Addr: "unix:" + sock, Dek: &DekConfig{CoreID: "tool-core"}})
	_, stop := serveAPI(t, r)
	t.Cleanup(stop)
	return unixClient(sock)
}

func TestAPIDek_OffUnlessConfigured(t *testing.T) {
	sock := shortSock(t)
	r := healthRoot(t, APIConfig{Addr: "unix:" + sock})
	_, stop := serveAPI(t, r)
	defer stop()
	code, _ := unixDo(t, unixClient(sock), http.MethodGet, "/v1/dek/hello", "")
	assert.Equal(t, http.StatusNotFound, code)
}

func TestAPIDek_ManifestFromTheCommandTree(t *testing.T) {
	c := dekRoot(t, shortSock(t))

	code, body := unixDo(t, c, http.MethodGet, "/v1/dek/hello", "")
	require.Equal(t, http.StatusOK, code, string(body))
	var h struct {
		CoreID       string `json:"core_id"`
		ManifestETag string `json:"manifest_etag"`
	}
	require.NoError(t, json.Unmarshal(body, &h))
	assert.Equal(t, "tool-core", h.CoreID)

	code, body = unixDo(t, c, http.MethodGet, "/v1/dek/capabilities", "")
	require.Equal(t, http.StatusOK, code, string(body))
	var caps []struct {
		ID          string `json:"id"`
		Read        bool   `json:"read"`
		Destructive bool   `json:"destructive"`
		DryRun      bool   `json:"dry_run"`
	}
	require.NoError(t, json.Unmarshal(body, &caps))
	by := map[string]int{}
	for i, cp := range caps {
		by[cp.ID] = i
	}
	require.Contains(t, by, "tool.list")
	require.Contains(t, by, "tool.add")
	assert.True(t, caps[by["tool.list"]].Read)
	assert.False(t, caps[by["tool.add"]].Read)
	assert.NotContains(t, by, "tool.serve", "commands the REST projection reserves are not capabilities")
}

func TestAPIDek_InvokeThroughTheBridge(t *testing.T) {
	c := dekRoot(t, shortSock(t))

	code, body := unixDo(t, c, http.MethodPost, "/v1/dek/invoke",
		`{"capability":"tool.list","args":{},"dry_run":false,"idempotency_key":""}`)
	require.Equal(t, http.StatusOK, code, string(body))
	assert.Contains(t, string(body), `"kind":"result"`)
	assert.Contains(t, string(body), "listed")

	code, body = unixDo(t, c, http.MethodPost, "/v1/dek/invoke",
		`{"capability":"tool.add","args":{},"dry_run":true,"idempotency_key":"k1"}`)
	require.Equal(t, http.StatusOK, code, string(body))
	assert.Contains(t, string(body), `"kind":"plan"`)
}
