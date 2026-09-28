package main

import (
	"errors"
	"net/http"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"hop.top/kit/go/console/output"
)

// writePolicy writes body as $XDG_CONFIG_HOME/served/policies/<name>.yaml
// in a config home of the test's own.
func writePolicy(t *testing.T, name, body string) {
	t.Helper()
	home := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", home)
	dir := filepath.Join(home, "served", "policies")
	require.NoError(t, os.MkdirAll(dir, 0o700))
	require.NoError(t, os.WriteFile(filepath.Join(dir, name+".yaml"), []byte(body), 0o600))
}

// TestPermissionRulesRefuseOverREST pins the permissions: block of the
// --policy file on the api service: a rule reading the command and its
// arguments refuses the invocation it names, 403 permission_denied
// with the rule's name and message, and lets every other one run.
// Discovery keeps the command: the verdict depends on the call.
func TestPermissionRulesRefuseOverREST(t *testing.T) {
	writePolicy(t, "stock", `
permissions:
  - name: no-washers
    when: resource.id == "item add" && "washer" in payload.args
    effect: deny
    otherwise: allow
    message: washers are out of stock
`)
	run := startServe(t, options{}, "api", "--addr", "127.0.0.1:0", "--policy", "stock")
	base := "http://" + run.waitReady(t, "api").Address

	status, body := httpDo(t, http.MethodPost, base+"/v1/commands/item/add", `{"args":["washer"]}`)
	assert.Equal(t, http.StatusForbidden, status, string(body))
	assert.Contains(t, string(body), `"code":"permission_denied"`)
	assert.Contains(t, string(body), `permission rule \"no-washers\": washers are out of stock`)

	status, body = httpDo(t, http.MethodPost, base+"/v1/commands/item/add", `{"args":["bolt"]}`)
	assert.Equal(t, http.StatusOK, status, string(body))

	assert.Equal(t, verdict{Invocable: true}, discover(t, base)["item add"])
}

// TestPermissionRuleThatDoesNotCompileRefusesToServe pins load-time
// compilation: the service never starts, and the usage error (exit 2)
// names the rule.
func TestPermissionRuleThatDoesNotCompileRefusesToServe(t *testing.T) {
	writePolicy(t, "broken", `
permissions:
  - name: half-written
    when: principal.id ==
    effect: deny
    otherwise: allow
`)
	root := newRoot(options{})
	root.Cmd.SetErr(&safeBuffer{})
	root.Cmd.SetOut(&safeBuffer{})
	err := runToCompletion(t, root, []string{"serve", "api", "--addr", "127.0.0.1:0", "--policy", "broken"}, 5*time.Second)
	require.Error(t, err)
	var ce *output.Error
	require.True(t, errors.As(err, &ce), "a usage error: %v", err)
	assert.Equal(t, output.ExitUsage, ce.ExitCode)
	assert.Contains(t, err.Error(), `permission rule "half-written" does not compile`)
}
