package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/spf13/cobra"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"hop.top/kit/go/console/output"
	_ "hop.top/kit/go/storage/kv/sqlite"
	"hop.top/kit/go/transport/authn"
	"hop.top/kit/go/transport/cmdsurface"
)

// apiKeyRoot is a root that issues API keys into path and serves the
// api service under auth.mode: apikey, with a `secret` command that
// requires an authenticated caller.
func apiKeyRoot(t *testing.T, path string, opts ...func(*Root)) *Root {
	t.Helper()
	opts = append([]func(*Root){
		WithAPI(APIConfig{Addr: "127.0.0.1:0"}),
		WithAPIKeys(APIKeysConfig{Path: path}),
	}, opts...)
	r := authRoot(t, opts...)
	r.Cmd.AddCommand(&cobra.Command{
		Use:   "secret",
		Short: "a secret",
		RunE:  func(cmd *cobra.Command, _ []string) error { cmd.Print("unlocked"); return nil },
		Annotations: map[string]string{
			"kit/side-effect":   "read",
			"kit/auth-required": "true",
		},
	})
	r.Viper.Set("services.api.auth.mode", "apikey")
	return r
}

// runKey runs `token key <args>` on a fresh root over path.
func runKey(t *testing.T, path string, args ...string) (string, error) {
	t.Helper()
	r := apiKeyRoot(t, path)
	var out bytes.Buffer
	r.Cmd.SetOut(&out)
	r.Cmd.SetErr(io.Discard)
	r.SetArgs(append([]string{"token", "key"}, args...))
	err := r.Execute(context.Background())
	return out.String(), err
}

func keyExit(t *testing.T, err error) int {
	t.Helper()
	if err == nil {
		return 0
	}
	var oe *output.Error
	require.ErrorAs(t, err, &oe, err.Error())
	return oe.ExitCode
}

// TestAPIKeyModeEndToEnd pins auth.mode: apikey on the api service: a
// key `token key create` issued in one process authenticates requests
// served by another, in X-API-Key or Authorization: Bearer, as an
// established caller with its principal, tenant and scopes; no key is
// 401 and audited; `token key revoke` stops the key at once, with the
// service still running.
func TestAPIKeyModeEndToEnd(t *testing.T) {
	path := filepath.Join(t.TempDir(), "keys", "apikeys.db")
	out, err := runKey(t, path, "create", "--sub", "ci-bot", "--tenant", "acme", "--scopes", "items:read,items:write")
	require.NoError(t, err)
	key := strings.TrimSpace(out)
	id := strings.Split(key, "_")[1]

	rec := &auditRecorder{}
	r := apiKeyRoot(t, path, WithAuditSinks(rec.spec()))
	base, stop := serveAPI(t, r)
	defer stop()

	for name, hdr := range map[string]map[string]string{
		"x-api-key": {"X-API-Key": key},
		"bearer":    {"Authorization": "Bearer " + key},
	} {
		t.Run(name, func(t *testing.T) {
			resp, body := get(t, base+"/v1/commands/secret", hdr)
			require.Equal(t, http.StatusOK, resp.StatusCode, string(body))
			assert.Contains(t, string(body), "unlocked")
			inv, _, err := rec.last(t)
			require.NoError(t, err)
			assert.Equal(t, cmdsurface.EstablishedVerified, inv.Meta.Established)
			assert.Equal(t, "ci-bot", inv.Meta.Caller)
			assert.Equal(t, "acme", inv.Meta.Tenant)
			assert.Equal(t, "items:read,items:write", inv.Meta.Extra["scopes"])
		})
	}

	before := rec.count()
	resp, body := get(t, base+"/v1/commands/list", nil)
	assert.Equal(t, http.StatusUnauthorized, resp.StatusCode, string(body))
	assert.Equal(t, before+1, rec.count(), "the refusal is audited")
	_, _, err = rec.last(t)
	assert.ErrorIs(t, err, cmdsurface.ErrAuthRefused)

	out, err = runKey(t, path, "revoke", id)
	require.NoError(t, err)
	assert.Contains(t, out, "revoked "+id)
	resp, body = get(t, base+"/v1/commands/list", map[string]string{"X-API-Key": key})
	assert.Equal(t, http.StatusUnauthorized, resp.StatusCode, string(body))
	assert.Contains(t, string(body), "revoked")

	out, err = runKey(t, path, "list", "--format", "json")
	require.NoError(t, err)
	var rows []map[string]any
	require.NoError(t, json.Unmarshal([]byte(out), &rows), out)
	require.Len(t, rows, 1)
	assert.Equal(t, "revoked", rows[0]["state"])
	assert.NotContains(t, out, strings.Split(key, "_")[2], "no secret is listed")
}

// TestTokenKeyRefusals pins the verbs' exit codes.
func TestTokenKeyRefusals(t *testing.T) {
	path := filepath.Join(t.TempDir(), "apikeys.db")
	_, err := runKey(t, path, "create")
	assert.Equal(t, 2, keyExit(t, err), "no --sub")
	_, err = runKey(t, path, "create", "--sub", "a", "--scopes", "a b")
	assert.Equal(t, 2, keyExit(t, err))
	_, err = runKey(t, path, "revoke", "0000000000000000")
	assert.Equal(t, 3, keyExit(t, err), "no such key")
}

// TestServeAPIKeyConfigErrors pins the configuration gate, exit 2.
func TestServeAPIKeyConfigErrors(t *testing.T) {
	path := filepath.Join(t.TempDir(), "apikeys.db")
	for name, c := range map[string]struct {
		keys map[string]any
		want string
	}{
		"memory cannot hold keys": {map[string]any{"services.api.auth.apikey.backend": "memory"},
			`services.api.auth.apikey.backend: "memory" cannot hold API keys; use sqlite or badger`},
		"driver not imported": {map[string]any{"services.api.auth.apikey.backend": "badger"},
			`kv backend "badger" is not registered; import hop.top/kit/go/storage/kv/badger`},
		"unknown key": {map[string]any{"services.api.auth.apikey.table": "x"}, `unknown key "table"`},
		"under another mode": {map[string]any{"services.api.auth.mode": "jwt",
			"services.api.auth.apikey.path": path}, `services.api.auth.apikey.path: set, but services.api.auth.mode is not "apikey"`},
	} {
		t.Run(name, func(t *testing.T) {
			r := apiKeyRoot(t, path)
			setKeys(r, c.keys)
			oe := usageErr(t, runServeExpect(t, r, []string{"serve", "api"}, 2*time.Second))
			assert.Equal(t, 2, oe.ExitCode)
			assert.Contains(t, oe.Message, c.want)
		})
	}
}

// TestAPIKeyVerifierReleasesItsStore pins the store's lifetime: opened
// on the first request, closed with Close, and a request after Close
// is still judged against a store of its own.
func TestAPIKeyVerifierReleasesItsStore(t *testing.T) {
	path := filepath.Join(t.TempDir(), "apikeys.db")
	out, err := runKey(t, path, "create", "--sub", "p")
	require.NoError(t, err)
	key := strings.TrimSpace(out)

	cfg, err := tlsResolver{svc: APIServiceName}.apiKeyStoreConfig(&Root{apiKeysCfg: &APIKeysConfig{Path: path}})
	require.NoError(t, err)
	v := &apiKeyVerifier{cfg: cfg}
	req, err := http.NewRequest(http.MethodGet, "/", nil)
	require.NoError(t, err)
	req.Header.Set(authn.APIKeyHeader, key)

	assert.Nil(t, v.store, "nothing is opened before a request")
	_, err = v.AuthFunc()(req)
	require.NoError(t, err)
	assert.NotNil(t, v.store)
	require.NoError(t, v.Close())
	assert.Nil(t, v.store)
	_, err = v.AuthFunc()(req)
	require.NoError(t, err, "a request draining after Close is judged")
	assert.Nil(t, v.store, "and holds nothing open")
}

// TestAPIKeyStoreIsOwnerOnly pins the key store's permissions: the
// sqlite file kit creates is 0600 and the directory it creates for it
// 0700, whatever the umask lets through, so another local user cannot
// read the key hashes and principals.
func TestAPIKeyStoreIsOwnerOnly(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("POSIX permission bits")
	}
	dir := filepath.Join(t.TempDir(), "keys")
	path := filepath.Join(dir, "apikeys.db")
	_, err := runKey(t, path, "create", "--sub", "ci-bot")
	require.NoError(t, err)

	fi, err := os.Stat(path)
	require.NoError(t, err)
	assert.Equal(t, os.FileMode(0o600), fi.Mode().Perm(), "the store file")
	di, err := os.Stat(dir)
	require.NoError(t, err)
	assert.Equal(t, os.FileMode(0o700), di.Mode().Perm(), "the directory kit created")
	for _, side := range []string{"-wal", "-shm", "-journal"} {
		if si, err := os.Stat(path + side); err == nil {
			assert.Equal(t, os.FileMode(0o600), si.Mode().Perm(), side)
		}
	}
}
