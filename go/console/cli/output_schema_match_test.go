package cli

import (
	"bytes"
	"encoding/json"
	"testing"

	"github.com/santhosh-tekuri/jsonschema/v5"
	"github.com/stretchr/testify/require"
)

// assertMatchesOutputSchema validates out — a command's --format json
// output — against the schema the command at path declares on a fresh
// kit root with every group that declares one mounted. It is the
// in-package counterpart of conformance/harness.AssertJSONSchema,
// which this package cannot import (harness imports cli).
func assertMatchesOutputSchema(t *testing.T, path []string, out string) {
	t.Helper()
	r := New(Config{Name: "tool", Version: "1.0.0", DisableValidate: true},
		WithAPI(APIConfig{}), WithAPIKeys(APIKeysConfig{Path: t.TempDir() + "/keys.db"}),
		WithQuotaCommand())
	cmd, _, err := r.Cmd.Find(path)
	require.NoError(t, err)
	raw, _, ok := GetOutputSchemaJSON(cmd)
	require.True(t, ok, "%v declares no output schema", path)

	c := jsonschema.NewCompiler()
	c.Draft = jsonschema.Draft2020
	require.NoError(t, c.AddResource("schema.json", bytes.NewReader(raw)))
	schema, err := c.Compile("schema.json")
	require.NoError(t, err)
	var doc any
	require.NoError(t, json.Unmarshal([]byte(out), &doc), out)
	require.NoError(t, schema.Validate(doc), "%v output does not match its schema:\n%s", path, out)
}
