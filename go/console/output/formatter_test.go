package output_test

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"hop.top/kit/go/console/output"
)

func specs() []output.OptionSpec {
	return []output.OptionSpec{
		{Name: "delimiter", Type: output.OptString, Default: ",", Usage: "field separator"},
		{Name: "limit", Type: output.OptInt, Default: 0, Usage: "row limit"},
		{Name: "no-header", Type: output.OptBool, Default: false, Usage: "drop header"},
		{Name: "style", Type: output.OptEnum, Default: "kv", Enum: []string{"kv", "lines", "paragraph"}, Usage: "text style"},
	}
}

func TestParseOptions_Defaults(t *testing.T) {
	o, err := output.ParseOptions(nil, specs())
	require.NoError(t, err)
	assert.Equal(t, ",", o.GetString("delimiter"))
	assert.Equal(t, 0, o.GetInt("limit"))
	assert.False(t, o.GetBool("no-header"))
	assert.Equal(t, "kv", o.GetString("style"))
}

func TestParseOptions_String(t *testing.T) {
	o, err := output.ParseOptions([]string{"delimiter=;"}, specs())
	require.NoError(t, err)
	assert.Equal(t, ";", o.GetString("delimiter"))
}

func TestParseOptions_IntCoercion(t *testing.T) {
	o, err := output.ParseOptions([]string{"limit=42"}, specs())
	require.NoError(t, err)
	assert.Equal(t, 42, o.GetInt("limit"))
}

func TestParseOptions_IntInvalid(t *testing.T) {
	_, err := output.ParseOptions([]string{"limit=abc"}, specs())
	require.Error(t, err)
	assert.Contains(t, err.Error(), "limit")
	assert.Contains(t, err.Error(), "abc")
}

func TestParseOptions_BoolCoercion(t *testing.T) {
	o, err := output.ParseOptions([]string{"no-header=true"}, specs())
	require.NoError(t, err)
	assert.True(t, o.GetBool("no-header"))
}

func TestParseOptions_BoolKeyOnly(t *testing.T) {
	o, err := output.ParseOptions([]string{"no-header"}, specs())
	require.NoError(t, err)
	assert.True(t, o.GetBool("no-header"))
}

func TestParseOptions_KeyOnlyOnNonBool(t *testing.T) {
	_, err := output.ParseOptions([]string{"delimiter"}, specs())
	require.Error(t, err)
	assert.Contains(t, err.Error(), "delimiter")
}

func TestParseOptions_Enum(t *testing.T) {
	o, err := output.ParseOptions([]string{"style=paragraph"}, specs())
	require.NoError(t, err)
	assert.Equal(t, "paragraph", o.GetString("style"))
}

func TestParseOptions_EnumRejected(t *testing.T) {
	_, err := output.ParseOptions([]string{"style=xml"}, specs())
	require.Error(t, err)
	assert.Contains(t, err.Error(), "style")
	assert.Contains(t, err.Error(), "xml")
}

func TestParseOptions_UnknownKey(t *testing.T) {
	_, err := output.ParseOptions([]string{"bogus=1"}, specs())
	require.Error(t, err)
	assert.Contains(t, err.Error(), "bogus")
	assert.Contains(t, err.Error(), "delimiter")
}

func TestParseOptions_EmptyKey(t *testing.T) {
	_, err := output.ParseOptions([]string{"=value"}, specs())
	require.Error(t, err)
}

func TestOptions_GetOr(t *testing.T) {
	o := output.Options{"a": "x"}
	assert.Equal(t, "x", o.GetOr("a", "fallback"))
	assert.Equal(t, "fallback", o.GetOr("missing", "fallback"))
}
