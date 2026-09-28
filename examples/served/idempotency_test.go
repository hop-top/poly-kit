package main

import (
	"context"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"hop.top/kit/go/transport/socket"
)

// isolateState points the tool's state directory, where the served
// idempotency store lives, at a directory of the test's own.
func isolateState(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	t.Setenv("XDG_STATE_HOME", dir)
	return dir
}

func postKeyed(t *testing.T, url, key, body string) (int, http.Header, []byte) {
	t.Helper()
	req, err := http.NewRequestWithContext(context.Background(), http.MethodPost, url, strings.NewReader(body))
	require.NoError(t, err)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Idempotency-Key", key)
	resp, err := http.DefaultClient.Do(req)
	require.NoError(t, err)
	defer func() { _ = resp.Body.Close() }()
	out, err := io.ReadAll(resp.Body)
	require.NoError(t, err)
	return resp.StatusCode, resp.Header, out
}

// TestIdempotencyKeyReplaysOverREST pins that a write retried with its
// Idempotency-Key runs once: the retry answers with the first call's
// result and Idempotent-Replayed, the same key for another call is 422,
// and the record lives in the tool's state directory.
func TestIdempotencyKeyReplaysOverREST(t *testing.T) {
	state := isolateState(t)
	run := startServe(t, options{}, "api", "--addr", "127.0.0.1:0")
	base := "http://" + run.waitReady(t, "api").Address
	url := base + "/v1/commands/item/add"

	status, hdr, first := postKeyed(t, url, "k-42", `{"args":["washer"]}`)
	require.Equal(t, http.StatusOK, status, string(first))
	assert.Empty(t, hdr.Get("Idempotent-Replayed"))

	status, hdr, again := postKeyed(t, url, "k-42", `{"args":["washer"]}`)
	require.Equal(t, http.StatusOK, status, string(again))
	assert.Equal(t, "true", hdr.Get("Idempotent-Replayed"))
	assert.JSONEq(t, string(first), string(again))

	status, body := httpDo(t, http.MethodGet, base+"/v1/commands/item/list", "")
	require.Equal(t, http.StatusOK, status)
	assert.Equal(t, []string{"bolt", "nut", "washer"}, items(t, decodeResult(t, body).Data),
		"the retry did not add a second washer")

	status, _, body = postKeyed(t, url, "k-42", `{"args":["dryer"]}`)
	assert.Equal(t, http.StatusUnprocessableEntity, status, string(body))
	assert.Contains(t, string(body), `"code":"idempotency_key_reused"`)

	_, err := os.Stat(filepath.Join(state, "served", "serve-idempotency.db"))
	assert.NoError(t, err, "the store is the tool's state file")
}

// TestIdempotencyKeyReplaysOverTheSocket pins the socket's carrier and
// marker: the request's idempotency_key, the response's replayed.
func TestIdempotencyKeyReplaysOverTheSocket(t *testing.T) {
	isolateState(t)
	path := shortSocketPath(t)
	run := startServe(t, options{}, "socket", "--socket", path)
	run.waitReady(t, "socket")

	req := socket.Request{Path: []string{"item", "add"}, Args: []string{"washer"}, IdempotencyKey: "s-1"}
	first := callSocket(t, path, req)
	require.True(t, first.Ok, "%+v", first.Error)
	assert.False(t, first.Replayed)
	again := callSocket(t, path, req)
	require.True(t, again.Ok, "%+v", again.Error)
	assert.True(t, again.Replayed)
	assert.Equal(t, first.Result.Stdout, again.Result.Stdout)

	req.Args = []string{"dryer"}
	resp := callSocket(t, path, req)
	require.False(t, resp.Ok)
	assert.Equal(t, socket.CodeConflict, resp.Error.Code)
}
