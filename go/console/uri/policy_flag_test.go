package uri_test

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	kitcli "hop.top/kit/go/console/cli"
	uricmd "hop.top/kit/go/console/uri"
)

func runUnderKitRoot(t *testing.T, args ...string) (string, string, error) {
	t.Helper()
	root := kitcli.New(kitcli.Config{Name: "fixture", Short: "fixture", DisableValidate: true})
	uricmd.Register(root.Cmd, testConfig())
	var out, errOut bytes.Buffer
	root.Cmd.SetOut(&out)
	root.Cmd.SetErr(&errOut)
	root.Cmd.SetArgs(args)
	err := root.Execute(context.Background())
	return out.String(), errOut.String(), err
}

// A uri leaf's own --policy <file> must work under a cli.New root, which
// owns a global --policy <name> for delegation policy. The leaf flag is a
// policy FILE path; the root's policy engine must not read it as a
// delegation policy name (and the file must actually be applied).
func TestLeafPolicyFileUnderKitRoot(t *testing.T) {
	file := filepath.Join(t.TempDir(), "uri-policy.json")
	require.NoError(t, os.WriteFile(file, []byte(`{"DefaultNamespaceSegments":2}`), 0o600))

	t.Run("parse applies the file", func(t *testing.T) {
		out, errOut, err := runUnderKitRoot(t,
			"uri", "parse", "--policy", file, "foo://a/b/c", "--format", "json")
		require.NoError(t, err, errOut)
		assert.Contains(t, out, `"namespace": "a/b"`)
		assert.NotContains(t, errOut, "no policy loader")
	})

	t.Run("resolve is not rejected by the delegation gate", func(t *testing.T) {
		_, errOut, err := runUnderKitRoot(t,
			"uri", "resolve", "--policy", file, "foo://a/b/c", "--format", "json")
		// No action on the URI: the leaf's own error, not the root's
		// "--policy is set but no policy loader" usage error.
		if err != nil {
			assert.NotContains(t, err.Error(), "no policy loader")
		}
		assert.NotContains(t, errOut, "no policy loader")
	})
}
