package main

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"hop.top/kit/go/console/cli/mcpserve"
	"hop.top/kit/go/transport/socket"
)

// TestPermissionsRefuseAnUnverifiedCallerOverREST pins the built-in
// scope check on the api service: `item export` declares
// kit/permissions, the fixture verifies nobody, so a caller holds no
// scope and is refused 403 insufficient_scope with the RFC 6750
// challenge naming the scope a token needs. Discovery still lists the
// command: the refusal depends on the caller.
func TestPermissionsRefuseAnUnverifiedCallerOverREST(t *testing.T) {
	run := startServe(t, options{}, "api", "--addr", "127.0.0.1:0")
	base := "http://" + run.waitReady(t, "api").Address

	resp, err := http.Get(base + "/v1/commands/item/export")
	require.NoError(t, err)
	defer func() { _ = resp.Body.Close() }()
	var body struct {
		Code    string `json:"code"`
		Message string `json:"message"`
	}
	require.NoError(t, json.NewDecoder(resp.Body).Decode(&body))
	assert.Equal(t, http.StatusForbidden, resp.StatusCode)
	assert.Equal(t, "insufficient_scope", body.Code)
	assert.Contains(t, body.Message, "missing scope items:export")
	assert.Equal(t, `Bearer error="insufficient_scope", scope="items:export"`, resp.Header.Get("WWW-Authenticate"))

	assert.Equal(t, verdict{Invocable: true}, discover(t, base)["item export"])
}

// TestPermissionsRefuseAnUnverifiedCallerOverMCP pins the same refusal
// on the mcp service over HTTP: an isError result whose text starts
// with the code.
func TestPermissionsRefuseAnUnverifiedCallerOverMCP(t *testing.T) {
	run := startServe(t, options{}, "mcp", "--mcp-addr", "127.0.0.1:0")
	sess := mcpDial(t, run.waitReady(t, mcpserve.ServiceName).Address, nil)

	text, _, isErr := mcpCall(t, sess, "item.export", nil)
	assert.True(t, isErr)
	assert.True(t, strings.HasPrefix(text, "insufficient_scope: "), text)
}

// TestPermissionsRunForTheOwnerOverTheSocket pins that a caller the
// socket file established holds the owner's authority: no credential
// carries scopes there, and the owner could run the command from the
// CLI anyway.
func TestPermissionsRunForTheOwnerOverTheSocket(t *testing.T) {
	path := shortSocketPath(t)
	run := startServe(t, options{}, "socket", "--socket", path)
	run.waitReady(t, "socket")

	resp := callSocket(t, path, socket.Request{Path: []string{"item", "export"}})
	require.True(t, resp.Ok, "%+v", resp.Error)
	assert.Equal(t, "exported 2 items\n", resp.Result.Stdout)
}
