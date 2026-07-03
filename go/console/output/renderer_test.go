package output_test

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"

	"github.com/spf13/cobra"
	"github.com/spf13/viper"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gopkg.in/yaml.v3"
	"hop.top/kit/go/console/output"
)

type row struct {
	Name  string `json:"name"  yaml:"name"  table:"Name"`
	Score int    `json:"score" yaml:"score" table:"Score"`
}

func TestRender_JSON(t *testing.T) {
	var buf bytes.Buffer
	require.NoError(t, output.Render(&buf, output.JSON, row{Name: "xray", Score: 95}))
	var got row
	require.NoError(t, json.Unmarshal(buf.Bytes(), &got))
	assert.Equal(t, "xray", got.Name)
}

func TestRender_YAML(t *testing.T) {
	var buf bytes.Buffer
	require.NoError(t, output.Render(&buf, output.YAML, row{Name: "grep", Score: 42}))
	var got row
	require.NoError(t, yaml.Unmarshal(buf.Bytes(), &got))
	assert.Equal(t, "grep", got.Name)
}

func TestRender_Table_ContainsValues(t *testing.T) {
	var buf bytes.Buffer
	require.NoError(t, output.Render(&buf, output.Table, []row{
		{Name: "xray", Score: 95},
		{Name: "grep", Score: 40},
	}))
	out := buf.String()
	assert.True(t, strings.Contains(out, "xray"))
	assert.True(t, strings.Contains(out, "95"))
}

func TestRender_UnknownFormat(t *testing.T) {
	var buf bytes.Buffer
	assert.Error(t, output.Render(&buf, "xml", row{}))
}

func TestRender_WithCols_RestrictsColumns(t *testing.T) {
	var buf bytes.Buffer
	require.NoError(t, output.Render(&buf, output.Table, []row{
		{Name: "xray", Score: 95},
	}, output.WithCols([]string{"Name"})))
	out := buf.String()
	assert.Contains(t, out, "Name")
	assert.Contains(t, out, "xray")
	assert.NotContains(t, out, "Score")
	assert.NotContains(t, out, "95")
}

func TestRender_WithCols_EmptyMeansAllColumns(t *testing.T) {
	var buf bytes.Buffer
	require.NoError(t, output.Render(&buf, output.Table, []row{
		{Name: "grep", Score: 42},
	}, output.WithCols(nil)))
	out := buf.String()
	assert.Contains(t, out, "Name")
	assert.Contains(t, out, "Score")
	assert.Contains(t, out, "42")
}

func TestRender_WithCols_UnknownColumnErrors(t *testing.T) {
	var buf bytes.Buffer
	assert.Error(t, output.Render(&buf, output.Table, []row{
		{Name: "xray", Score: 95},
	}, output.WithCols([]string{"Nope"})))
}

func BenchmarkRenderTable(b *testing.B) {
	data := make([]row, 1000)
	for i := range data {
		data[i] = row{Name: "item", Score: i}
	}
	var buf bytes.Buffer
	b.ReportAllocs()
	b.ResetTimer()
	for range b.N {
		buf.Reset()
		_ = output.Render(&buf, output.Table, data)
	}
}

func TestRegisterFlags(t *testing.T) {
	cmd := &cobra.Command{Use: "test"}
	v := viper.New()
	output.RegisterFlags(cmd, v)

	f := cmd.PersistentFlags().Lookup("format")
	require.NotNil(t, f, "--format flag must be registered")
	assert.Equal(t, "table", f.DefValue)

	// Simulate passing --format=json and verify viper picks it up.
	require.NoError(t, cmd.PersistentFlags().Set("format", "json"))
	assert.Equal(t, "json", v.GetString("format"))
}
