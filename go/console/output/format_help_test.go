package output_test

import (
	"bytes"
	"io"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"hop.top/kit/go/console/output"
)

// fmtWithOpts is a Formatter declaring options so FormatOptions has rows
// to render. Only used by format_help tests.
type fmtWithOpts struct{}

func (fmtWithOpts) Key() string          { return "fancy" }
func (fmtWithOpts) Extensions() []string { return []string{".fancy"} }
func (fmtWithOpts) Options() []output.OptionSpec {
	return []output.OptionSpec{
		{Name: "limit", Type: output.OptInt, Default: 10, Usage: "row limit"},
		{Name: "style", Type: output.OptEnum, Default: "kv",
			Enum: []string{"kv", "lines"}, Usage: "rendering style"},
	}
}
func (fmtWithOpts) Render(_ io.Writer, _ any, _ output.Options, _ []string) error {
	return nil
}

func TestListFormats_NoArg(t *testing.T) {
	r := output.NewRegistry()
	r.Register(fmtWithOpts{})
	r.Register(fakeFormatter{key: "plain", exts: []string{".plain"}})
	rows := output.ListFormats(r)
	require.Len(t, rows, 2)
	keys := []string{rows[0].Key, rows[1].Key}
	assert.Contains(t, keys, "fancy")
	assert.Contains(t, keys, "plain")
	// fancy must list its options.
	for _, row := range rows {
		if row.Key == "fancy" {
			assert.Contains(t, row.Options, "limit")
			assert.Contains(t, row.Options, "style")
			assert.Equal(t, ".fancy", row.Extensions)
		}
	}
}

func TestFormatOptions_PerFormat(t *testing.T) {
	r := output.NewRegistry()
	r.Register(fmtWithOpts{})
	rows, err := output.FormatOptions(r, "fancy")
	require.NoError(t, err)
	require.Len(t, rows, 2)
	assert.Equal(t, "limit", rows[0].Name)
	assert.Equal(t, "int", rows[0].Type)
	assert.Equal(t, "10", rows[0].Default)
	assert.Equal(t, "style", rows[1].Name)
	assert.Equal(t, "enum", rows[1].Type)
	assert.Equal(t, "kv, lines", rows[1].Enum)
}

func TestFormatOptions_UnknownFormat(t *testing.T) {
	r := output.NewRegistry()
	r.Register(fmtWithOpts{})
	_, err := output.FormatOptions(r, "missing")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "unknown format")
	assert.Contains(t, err.Error(), "fancy")
}

func TestRenderFormatHelp_NoArg(t *testing.T) {
	r := output.NewRegistry()
	r.Register(fmtWithOpts{})
	r.Register(fakeFormatter{key: "plain", exts: []string{".plain"}})
	var buf bytes.Buffer
	require.NoError(t, output.RenderFormatHelp(&buf, r, ""))
	out := buf.String()
	assert.Contains(t, out, "FORMAT")
	assert.Contains(t, out, "fancy")
	assert.Contains(t, out, "plain")
}

func TestRenderFormatHelp_PerFormat(t *testing.T) {
	r := output.NewRegistry()
	r.Register(fmtWithOpts{})
	var buf bytes.Buffer
	require.NoError(t, output.RenderFormatHelp(&buf, r, "fancy"))
	out := buf.String()
	assert.Contains(t, out, "limit")
	assert.Contains(t, out, "style")
}

func TestRenderFormatHelp_NoOptions(t *testing.T) {
	r := output.NewRegistry()
	r.Register(fakeFormatter{key: "bare"})
	var buf bytes.Buffer
	require.NoError(t, output.RenderFormatHelp(&buf, r, "bare"))
	assert.Contains(t, buf.String(), "no options")
}

func TestRenderFormatHelp_UnknownErrors(t *testing.T) {
	r := output.NewRegistry()
	r.Register(fmtWithOpts{})
	var buf bytes.Buffer
	err := output.RenderFormatHelp(&buf, r, "nope")
	require.Error(t, err)
	assert.True(t, strings.Contains(err.Error(), "unknown format"))
}

func TestRenderFormatHelp_NilRegistryFallsBackToDefault(t *testing.T) {
	var buf bytes.Buffer
	require.NoError(t, output.RenderFormatHelp(&buf, nil, ""))
	// Default has json/yaml/table at minimum.
	out := buf.String()
	for _, k := range []string{"json", "yaml", "table"} {
		assert.Contains(t, out, k)
	}
}
