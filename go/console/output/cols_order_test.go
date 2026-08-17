package output_test

import (
	"bytes"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"hop.top/kit/go/console/output"
)

// orderRow's declaration order is alpha, mid, zeta — which is also its
// alphabetical order. Every --cols list below therefore disagrees with
// BOTH declaration order and alphabetical order, so an assertion on the
// emitted order can only pass when the user's order is honored.
type orderRow struct {
	Alpha string `table:"alpha" json:"alpha" yaml:"alpha"`
	Mid   string `table:"mid" json:"mid" yaml:"mid"`
	Zeta  string `table:"zeta" json:"zeta" yaml:"zeta"`
}

func renderCols(t *testing.T, key string, data any, optPairs, cols []string) (string, error) {
	t.Helper()
	f, ok := output.Default.Lookup(key)
	require.True(t, ok, "formatter %q must be registered on Default", key)
	opts, err := output.ParseOptions(optPairs, f.Options())
	require.NoError(t, err)
	var buf bytes.Buffer
	if err := f.Render(&buf, data, opts, cols); err != nil {
		return buf.String(), err
	}
	return buf.String(), nil
}

func orderData() []orderRow {
	return []orderRow{{Alpha: "a", Mid: "m", Zeta: "z"}}
}

// --- cause 1: filterColumns must emit in selected order (table/csv/text) ---

func TestCols_Reorder_CSV(t *testing.T) {
	out, err := renderCols(t, output.CSV, orderData(), nil, []string{"zeta", "mid"})
	require.NoError(t, err)
	lines := strings.Split(strings.TrimRight(out, "\n"), "\n")
	require.Len(t, lines, 2)
	assert.Equal(t, "zeta,mid", lines[0])
	assert.Equal(t, "z,m", lines[1])
}

func TestCols_Reorder_CSV_AllThree(t *testing.T) {
	out, err := renderCols(t, output.CSV, orderData(), nil, []string{"zeta", "alpha", "mid"})
	require.NoError(t, err)
	lines := strings.Split(strings.TrimRight(out, "\n"), "\n")
	require.Len(t, lines, 2)
	assert.Equal(t, "zeta,alpha,mid", lines[0])
	assert.Equal(t, "z,a,m", lines[1])
}

func TestCols_Reorder_Table(t *testing.T) {
	out, err := renderCols(t, output.Table, orderData(), nil, []string{"zeta", "mid"})
	require.NoError(t, err)
	lines := strings.Split(strings.TrimRight(out, "\n"), "\n")
	require.Len(t, lines, 2)
	assert.Equal(t, []string{"zeta", "mid"}, strings.Fields(lines[0]))
	assert.Equal(t, []string{"z", "m"}, strings.Fields(lines[1]))
}

func TestCols_Reorder_TextKV(t *testing.T) {
	out, err := renderCols(t, output.Text, orderData(), nil, []string{"zeta", "mid"})
	require.NoError(t, err)
	assert.Equal(t, "zeta=z\nmid=m\n", out)
}

func TestCols_Reorder_TextLines(t *testing.T) {
	out, err := renderCols(t, output.Text, orderData(), []string{"style=lines"},
		[]string{"zeta", "alpha", "mid"})
	require.NoError(t, err)
	assert.Equal(t, "z\ta\tm\n", out)
}

func TestCols_Reorder_TextParagraph(t *testing.T) {
	out, err := renderCols(t, output.Text, orderData(), []string{"style=paragraph"},
		[]string{"zeta", "mid"})
	require.NoError(t, err)
	assert.Equal(t, "Record 1:\n  zeta: z\n  mid: m\n", out)
}

// --- cause 2: structToMap must be an order-preserving carrier (json/yaml) ---

func TestCols_Reorder_JSON(t *testing.T) {
	out, err := renderCols(t, output.JSON, orderData(), nil, []string{"zeta", "mid"})
	require.NoError(t, err)
	assert.Equal(t, "[\n  {\n    \"zeta\": \"z\",\n    \"mid\": \"m\"\n  }\n]\n", out)
}

func TestCols_Reorder_JSON_AllThree(t *testing.T) {
	out, err := renderCols(t, output.JSON, orderData(), nil, []string{"zeta", "alpha", "mid"})
	require.NoError(t, err)
	assert.Equal(t,
		"[\n  {\n    \"zeta\": \"z\",\n    \"alpha\": \"a\",\n    \"mid\": \"m\"\n  }\n]\n", out)
}

func TestCols_Reorder_JSON_SingleStruct(t *testing.T) {
	out, err := renderCols(t, output.JSON, orderData()[0], nil, []string{"zeta", "mid"})
	require.NoError(t, err)
	assert.Equal(t, "{\n  \"zeta\": \"z\",\n  \"mid\": \"m\"\n}\n", out)
}

func TestCols_Reorder_YAML(t *testing.T) {
	out, err := renderCols(t, output.YAML, orderData(), nil, []string{"zeta", "mid"})
	require.NoError(t, err)
	assert.Equal(t, "- zeta: z\n  mid: m\n", out)
}

func TestCols_Reorder_YAML_AllThree(t *testing.T) {
	out, err := renderCols(t, output.YAML, orderData(), nil, []string{"zeta", "alpha", "mid"})
	require.NoError(t, err)
	assert.Equal(t, "- zeta: z\n  alpha: a\n  mid: m\n", out)
}

func TestCols_Reorder_YAML_SingleStruct(t *testing.T) {
	out, err := renderCols(t, output.YAML, orderData()[0], nil, []string{"zeta", "mid"})
	require.NoError(t, err)
	assert.Equal(t, "zeta: z\nmid: m\n", out)
}

// --- invariants that must survive the reorder ---

func TestCols_Reorder_NoCols_KeepsDeclarationOrder(t *testing.T) {
	// Rule 1: with no --cols, order falls back to struct declaration order.
	out, err := renderCols(t, output.CSV, orderData(), nil, nil)
	require.NoError(t, err)
	assert.Equal(t, "alpha,mid,zeta\n", strings.SplitAfter(out, "\n")[0])
}

func TestCols_Reorder_UnknownColumn_ErrorTextUnchanged(t *testing.T) {
	for _, key := range []string{output.CSV, output.Text, output.Table} {
		_, err := renderCols(t, key, orderData(), nil, []string{"zeta", "Bogus"})
		require.Error(t, err, "formatter %q", key)
		assert.Equal(t,
			`unknown column "Bogus" (valid: alpha, mid, zeta)`, err.Error(),
			"formatter %q", key)
	}
}

func TestCols_Reorder_DuplicateSelected_EmittedOnce(t *testing.T) {
	// resolveCols dedupes upstream, but filterColumns must not double-emit
	// if a caller passes a repeated name directly.
	out, err := renderCols(t, output.CSV, orderData(), nil, []string{"zeta", "mid", "zeta"})
	require.NoError(t, err)
	assert.Equal(t, "zeta,mid\n", strings.SplitAfter(out, "\n")[0])
}
