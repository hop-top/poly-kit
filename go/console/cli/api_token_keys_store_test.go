package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"hop.top/kit/go/console/output"
)

// keyStoreRoot is a root with the api, the socket and an "mcp" service
// registered, issuing keys whose code default store is codeDefault.
// The services' own code is beside the point: which store `token key`
// opens follows from configuration and the registered names alone.
func keyStoreRoot(t *testing.T, codeDefault string) *Root {
	t.Helper()
	return authRoot(t,
		WithAPI(APIConfig{Addr: "127.0.0.1:0"}),
		WithSocket(SocketConfig{Path: filepath.Join(t.TempDir(), "s.sock")}),
		WithServices(newToggle("mcp")),
		WithAPIKeys(APIKeysConfig{Path: codeDefault}),
	)
}

// tokenKey runs `token key <args>` on r and returns stdout.
func tokenKey(t *testing.T, r *Root, args ...string) (string, error) {
	t.Helper()
	var out bytes.Buffer
	r.Cmd.SetOut(&out)
	r.Cmd.SetErr(io.Discard)
	r.SetArgs(append([]string{"token", "key"}, args...))
	err := r.Execute(context.Background())
	return out.String(), err
}

// createdID creates a key for alice on r and returns its id.
func createdID(t *testing.T, r *Root, args ...string) string {
	t.Helper()
	out, err := tokenKey(t, r, append([]string{"create", "--sub", "alice", "--format", "json"}, args...)...)
	require.NoError(t, err)
	var doc apiKeyCreated
	require.NoError(t, json.Unmarshal([]byte(out), &doc), out)
	return doc.ID
}

// TestTokenKey_StoreFromServiceConfigSources pins that `token key`
// reads services.<svc>.auth from every source `service start` reads —
// the user config file, a -c file, -c key=value and the environment —
// so it writes the store the service reads, not the code default.
func TestTokenKey_StoreFromServiceConfigSources(t *testing.T) {
	yaml := func(path string) string {
		return "services:\n  mcp:\n    auth:\n      mode: apikey\n      apikey:\n        path: " + path + "\n"
	}
	for name, set := range map[string]func(t *testing.T, r *Root, path string){
		"user config file": func(t *testing.T, _ *Root, path string) { userConfig(t, yaml(path)) },
		"-c file": func(t *testing.T, r *Root, path string) {
			f := filepath.Join(t.TempDir(), "extra.yaml")
			require.NoError(t, os.WriteFile(f, []byte(yaml(path)), 0o600))
			withConfigArgs(t, r, f)
		},
		"-c key=value": func(t *testing.T, r *Root, path string) {
			withConfigArgs(t, r, "services.mcp.auth.mode=apikey", "services.mcp.auth.apikey.path="+path)
		},
		"environment": func(t *testing.T, _ *Root, path string) {
			t.Setenv("TOOL_SERVICES_MCP_AUTH_MODE", "apikey")
			t.Setenv("TOOL_SERVICES_MCP_AUTH_APIKEY_PATH", path)
		},
	} {
		t.Run(name, func(t *testing.T) {
			isolateHome(t)
			path := filepath.Join(t.TempDir(), "mcp", "apikeys.db")
			codeDefault := filepath.Join(t.TempDir(), "default", "apikeys.db")
			r := keyStoreRoot(t, codeDefault)
			set(t, r, path)
			id := createdID(t, r)
			assert.FileExists(t, path)
			assert.NoFileExists(t, codeDefault, "the code default is not where the service reads")

			l := keyStoreRoot(t, codeDefault)
			set(t, l, path)
			out, err := tokenKey(t, l, "list", "--format", "json")
			require.NoError(t, err)
			assert.Contains(t, out, id)
		})
	}
}

// TestTokenKey_DefaultService pins how the verbs pick a store without
// --service: the one the registered services under auth.mode: apikey
// read, when there is exactly one; otherwise a usage error naming the
// services to choose from. The socket never counts: it has no HTTP
// listener to read an API key on.
func TestTokenKey_DefaultService(t *testing.T) {
	type tc struct {
		keys     func(a, b string) map[string]any
		wantPath int // 1 → a, 2 → b, 0 → refused
		wantMsg  []string
		wantFix  string
	}
	for name, c := range map[string]tc{
		"the one service under apikey": {
			keys: func(a, _ string) map[string]any {
				return map[string]any{"services.mcp.auth.mode": "apikey", "services.mcp.auth.apikey.path": a}
			},
			wantPath: 1,
		},
		"beside the socket under peer": {
			keys: func(a, _ string) map[string]any {
				return map[string]any{"services.socket.auth.mode": "peer",
					"services.mcp.auth.mode": "apikey", "services.mcp.auth.apikey.path": a}
			},
			wantPath: 1,
		},
		"services sharing one store": {
			keys: func(a, _ string) map[string]any {
				return map[string]any{"services.all.auth.mode": "apikey", "services.all.auth.apikey.path": a}
			},
			wantPath: 1,
		},
		"two services, two stores": {
			keys: func(a, b string) map[string]any {
				return map[string]any{
					"services.api.auth.mode": "apikey", "services.api.auth.apikey.path": a,
					"services.mcp.auth.mode": "apikey", "services.mcp.auth.apikey.path": b,
				}
			},
			wantMsg: []string{"token key create: services api, mcp use auth.mode: apikey with different stores",
				"--service"},
			wantFix: "pass --service api or --service mcp",
		},
		"no service under apikey": {
			keys: func(string, string) map[string]any { return nil },
			wantMsg: []string{"token key create: no service uses auth.mode: apikey",
				"--service"},
			wantFix: "set services.<service>.auth.mode: apikey, or pass --service naming one of api, mcp",
		},
	} {
		t.Run(name, func(t *testing.T) {
			isolateHome(t)
			dir := t.TempDir()
			a, b := filepath.Join(dir, "a", "keys.db"), filepath.Join(dir, "b", "keys.db")
			codeDefault := filepath.Join(dir, "default", "keys.db")
			r := keyStoreRoot(t, codeDefault)
			setKeys(r, c.keys(a, b))
			_, err := tokenKey(t, r, "create", "--sub", "alice")
			switch c.wantPath {
			case 0:
				var oe *output.Error
				require.ErrorAs(t, err, &oe)
				assert.Equal(t, output.ExitUsage, oe.ExitCode)
				for _, m := range c.wantMsg {
					assert.Contains(t, oe.Message, m)
				}
				assert.Equal(t, c.wantFix, oe.SuggestedFix)
				for _, p := range []string{a, b, codeDefault} {
					assert.NoFileExists(t, p, "a refusal opens no store")
				}
			case 1:
				require.NoError(t, err)
				assert.FileExists(t, a)
				assert.NoFileExists(t, codeDefault)
			}
		})
	}
}

// TestTokenKey_ServiceFlagStillNamesOne pins that --service picks the
// store whatever the defaulting would say: with no service under
// auth.mode: apikey, and with two reading different stores.
func TestTokenKey_ServiceFlagStillNamesOne(t *testing.T) {
	isolateHome(t)
	dir := t.TempDir()
	codeDefault := filepath.Join(dir, "default", "keys.db")
	r := keyStoreRoot(t, codeDefault)
	createdID(t, r, "--service", "mcp")
	assert.FileExists(t, codeDefault, "no auth.apikey for mcp: the code default")

	a, b := filepath.Join(dir, "a", "keys.db"), filepath.Join(dir, "b", "keys.db")
	r = keyStoreRoot(t, codeDefault)
	setKeys(r, map[string]any{
		"services.api.auth.mode": "apikey", "services.api.auth.apikey.path": a,
		"services.mcp.auth.mode": "apikey", "services.mcp.auth.apikey.path": b,
	})
	createdID(t, r, "--service", "mcp")
	assert.FileExists(t, b)
	assert.NoFileExists(t, a)
}
