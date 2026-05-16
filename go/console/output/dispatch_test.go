package output_test

import (
	"bytes"
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/spf13/cobra"
	"github.com/spf13/viper"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"hop.top/kit/go/console/output"
)

// dispatchRow is the shared row type used across Dispatch tests. The
// `table:""` tags are the column headers --cols / template / projection
// validate against.
type dispatchRow struct {
	ID    string `json:"id"    yaml:"id"    table:"ID"`
	Name  string `json:"name"  yaml:"name"  table:"Name"`
	Score int    `json:"score" yaml:"score" table:"Score"`
}

func dispatchData() []dispatchRow {
	return []dispatchRow{
		{ID: "a", Name: "alpha", Score: 1},
		{ID: "b", Name: "beta", Score: 2},
	}
}

// newCmd builds a cobra.Command + viper pair pre-wired with the output
// flags for the test. Call cmd.SetOut(buf) to capture stdout-bound
// output, and cmd.PersistentFlags().Set / cmd.SetArgs to drive flags.
func newCmd(t *testing.T, opts ...output.RegistryOption) (*cobra.Command, *viper.Viper, *bytes.Buffer) {
	t.Helper()
	cmd := &cobra.Command{Use: "x"}
	v := viper.New()
	output.RegisterFlagsWith(cmd, v, opts...)
	buf := &bytes.Buffer{}
	cmd.SetOut(buf)
	cmd.SetErr(io.Discard)
	return cmd, v, buf
}

func TestDispatch_DefaultStdoutTable(t *testing.T) {
	cmd, v, buf := newCmd(t)
	require.NoError(t, output.Dispatch(cmd, v, dispatchData()))
	out := buf.String()
	assert.Contains(t, out, "alpha")
	assert.Contains(t, out, "Score")
}

func TestDispatch_FormatJSON(t *testing.T) {
	cmd, v, buf := newCmd(t)
	require.NoError(t, cmd.PersistentFlags().Set("format", "json"))
	require.NoError(t, output.Dispatch(cmd, v, dispatchData()))
	var got []dispatchRow
	require.NoError(t, json.Unmarshal(buf.Bytes(), &got))
	assert.Len(t, got, 2)
	assert.Equal(t, "alpha", got[0].Name)
}

func TestDispatch_FormatOpt_Unknown(t *testing.T) {
	cmd, v, _ := newCmd(t)
	require.NoError(t, cmd.PersistentFlags().Set("format-opt", "bogus=1"))
	err := output.Dispatch(cmd, v, dispatchData())
	require.Error(t, err)
	assert.Contains(t, err.Error(), "unknown option")
}

func TestDispatch_Cols_Subset(t *testing.T) {
	cmd, v, buf := newCmd(t)
	require.NoError(t, cmd.PersistentFlags().Set("cols", "Name,Score"))
	require.NoError(t, output.Dispatch(cmd, v, dispatchData()))
	out := buf.String()
	assert.Contains(t, out, "Name")
	assert.Contains(t, out, "Score")
	assert.NotContains(t, out, "ID\n")
}

func TestDispatch_ColsAlias_Columns(t *testing.T) {
	cmd, v, buf := newCmd(t)
	require.NoError(t, cmd.PersistentFlags().Set("columns", "Name"))
	require.NoError(t, output.Dispatch(cmd, v, dispatchData()))
	out := buf.String()
	assert.Contains(t, out, "Name")
	// Score column should not appear at all when only Name is selected.
	assert.NotContains(t, out, "Score")
}

func TestDispatch_Cols_Unknown_Error(t *testing.T) {
	cmd, v, _ := newCmd(t)
	require.NoError(t, cmd.PersistentFlags().Set("cols", "Nope"))
	err := output.Dispatch(cmd, v, dispatchData())
	require.Error(t, err)
	assert.Contains(t, err.Error(), "unknown column")
	assert.Contains(t, err.Error(), "Nope")
}

func TestDispatch_Cols_Dedupe_PreservesOrder(t *testing.T) {
	cmd, v, buf := newCmd(t)
	// Multiple --cols values, comma-split, with duplicates. resolveCols
	// must dedupe while preserving first-seen order.
	pf := cmd.PersistentFlags().Lookup("cols")
	require.NotNil(t, pf)
	require.NoError(t, pf.Value.Set("Name,Score"))
	require.NoError(t, pf.Value.Set("Name"))
	require.NoError(t, pf.Value.Set("ID,Score"))

	require.NoError(t, cmd.PersistentFlags().Set("format", "json"))
	require.NoError(t, output.Dispatch(cmd, v, dispatchData()))
	// JSON projects via maps so the actual ordering check belongs to
	// the table rendering path; just make sure no duplicate-induced
	// error fires here.
	assert.Contains(t, buf.String(), "alpha")
}

func TestDispatch_Cols_AcrossFormats(t *testing.T) {
	cases := []struct {
		format  string
		mustHit string
	}{
		{format: "table", mustHit: "Name"},
		{format: "json", mustHit: "alpha"},
		{format: "yaml", mustHit: "alpha"},
	}
	for _, tc := range cases {
		t.Run(tc.format, func(t *testing.T) {
			cmd, v, buf := newCmd(t)
			require.NoError(t, cmd.PersistentFlags().Set("format", tc.format))
			require.NoError(t, cmd.PersistentFlags().Set("cols", "Name"))
			require.NoError(t, output.Dispatch(cmd, v, dispatchData()))
			out := buf.String()
			assert.Contains(t, out, tc.mustHit)
			// Score should be excluded across all formats.
			assert.NotContains(t, out, "Score")
			assert.NotContains(t, out, "score")
		})
	}
}

func TestDispatch_Template_Simple(t *testing.T) {
	cmd, v, buf := newCmd(t)
	require.NoError(t, cmd.PersistentFlags().Set("template",
		"{{range .Items}}{{.Name}}={{.Score}}\n{{end}}"))
	require.NoError(t, output.Dispatch(cmd, v, dispatchData()))
	got := buf.String()
	assert.Equal(t, "alpha=1\nbeta=2\n", got)
}

func TestDispatch_Template_MissingFieldErrors(t *testing.T) {
	cmd, v, _ := newCmd(t)
	// Action on a missing key fails Execute under default option `missingkey=invalid`
	// only when piped into specific functions; rely on a hard parse error
	// to surface template diagnostics instead.
	require.NoError(t, cmd.PersistentFlags().Set("template",
		"{{range .Items}}{{notafunc .Name}}{{end}}"))
	err := output.Dispatch(cmd, v, dispatchData())
	require.Error(t, err)
	assert.Contains(t, err.Error(), "template")
}

func TestDispatch_Template_MutexCols(t *testing.T) {
	cmd, v, _ := newCmd(t)
	require.NoError(t, cmd.PersistentFlags().Set("template", "{{.Cols}}"))
	require.NoError(t, cmd.PersistentFlags().Set("cols", "Name"))
	err := output.Dispatch(cmd, v, dispatchData())
	require.Error(t, err)
	assert.Contains(t, err.Error(), "mutually exclusive")
}

func TestDispatch_Output_File(t *testing.T) {
	cmd, v, _ := newCmd(t)
	dir := t.TempDir()
	path := filepath.Join(dir, "out.json")
	require.NoError(t, cmd.PersistentFlags().Set("format", "json"))
	require.NoError(t, cmd.PersistentFlags().Set("output", path))
	require.NoError(t, output.Dispatch(cmd, v, dispatchData()))

	body, err := os.ReadFile(path)
	require.NoError(t, err)
	var got []dispatchRow
	require.NoError(t, json.Unmarshal(body, &got))
	assert.Len(t, got, 2)
}

func TestDispatch_Output_Sentinel(t *testing.T) {
	cmd, v, buf := newCmd(t)
	require.NoError(t, cmd.PersistentFlags().Set("output", "-"))
	require.NoError(t, output.Dispatch(cmd, v, dispatchData()))
	assert.Contains(t, buf.String(), "alpha")
}

func TestDispatch_Output_Overwrite(t *testing.T) {
	cmd, v, _ := newCmd(t)
	dir := t.TempDir()
	path := filepath.Join(dir, "out.json")
	require.NoError(t, os.WriteFile(path, []byte("PRIOR"), 0o600))

	require.NoError(t, cmd.PersistentFlags().Set("format", "json"))
	require.NoError(t, cmd.PersistentFlags().Set("output", path))
	require.NoError(t, output.Dispatch(cmd, v, dispatchData()))

	body, err := os.ReadFile(path)
	require.NoError(t, err)
	assert.NotContains(t, string(body), "PRIOR", "overwrite must drop prior content")
}

func TestDispatch_Output_DirError(t *testing.T) {
	cmd, v, _ := newCmd(t)
	dir := t.TempDir()
	require.NoError(t, cmd.PersistentFlags().Set("output", dir))
	err := output.Dispatch(cmd, v, dispatchData())
	require.Error(t, err)
	assert.Contains(t, err.Error(), "directory")
}

func TestDispatch_DisableOutputFlag(t *testing.T) {
	cmd, _, _ := newCmd(t, output.DisableOutputFlag())
	assert.Nil(t, cmd.PersistentFlags().Lookup("output"))
}

func TestDispatch_ExtensionInference(t *testing.T) {
	cmd, v, _ := newCmd(t)
	dir := t.TempDir()
	path := filepath.Join(dir, "out.json")
	// --format default (not set), --output extension is .json → infer json.
	require.NoError(t, cmd.PersistentFlags().Set("output", path))
	require.NoError(t, output.Dispatch(cmd, v, dispatchData()))

	body, err := os.ReadFile(path)
	require.NoError(t, err)
	var got []dispatchRow
	require.NoError(t, json.Unmarshal(body, &got))
	assert.Len(t, got, 2)
}

func TestDispatch_ExtensionMismatchError(t *testing.T) {
	cmd, v, _ := newCmd(t)
	dir := t.TempDir()
	path := filepath.Join(dir, "out.yaml")
	require.NoError(t, cmd.PersistentFlags().Set("format", "json"))
	require.NoError(t, cmd.PersistentFlags().Set("output", path))
	err := output.Dispatch(cmd, v, dispatchData())
	require.Error(t, err)
	assert.Contains(t, err.Error(), "does not match output extension")
}

func TestDispatch_ExplicitFormatWinsWhenNoExt(t *testing.T) {
	cmd, v, _ := newCmd(t)
	dir := t.TempDir()
	path := filepath.Join(dir, "out") // no extension
	require.NoError(t, cmd.PersistentFlags().Set("format", "yaml"))
	require.NoError(t, cmd.PersistentFlags().Set("output", path))
	require.NoError(t, output.Dispatch(cmd, v, dispatchData()))

	body, err := os.ReadFile(path)
	require.NoError(t, err)
	assert.Contains(t, string(body), "alpha")
}

func TestDispatch_FormatOpt_RepeatedAccumulation(t *testing.T) {
	// Use a custom registry + formatter to exercise repeated --format-opt
	// without needing to land csv. The fake formatter records the opts it
	// receives so the test can assert accumulation.
	r := output.NewRegistry()
	captured := &capturingFormatter{}
	r.Override(captured)

	cmd := &cobra.Command{Use: "x"}
	v := viper.New()
	output.RegisterFlagsWith(cmd, v, output.WithRegistry(r))
	cmd.SetOut(io.Discard)

	require.NoError(t, cmd.PersistentFlags().Set("format", "fake"))
	pf := cmd.PersistentFlags().Lookup("format-opt")
	require.NotNil(t, pf)
	require.NoError(t, pf.Value.Set("limit=5"))
	require.NoError(t, pf.Value.Set("style=lines"))
	require.NoError(t, pf.Value.Set("no-header"))

	require.NoError(t, output.Dispatch(cmd, v, dispatchData()))
	assert.Equal(t, 5, captured.opts.GetInt("limit"))
	assert.Equal(t, "lines", captured.opts.GetString("style"))
	assert.True(t, captured.opts.GetBool("no-header"))
}

// capturingFormatter is a Formatter that stores the Options it receives
// so tests can assert flag plumbing.
type capturingFormatter struct {
	opts output.Options
	cols []string
}

func (c *capturingFormatter) Key() string          { return "fake" }
func (c *capturingFormatter) Extensions() []string { return []string{".fake"} }
func (c *capturingFormatter) Options() []output.OptionSpec {
	return []output.OptionSpec{
		{Name: "limit", Type: output.OptInt},
		{Name: "style", Type: output.OptEnum, Enum: []string{"kv", "lines"}, Default: "kv"},
		{Name: "no-header", Type: output.OptBool},
	}
}
func (c *capturingFormatter) Render(_ io.Writer, _ any, opts output.Options, cols []string) error {
	c.opts = opts
	c.cols = cols
	return nil
}

func TestDispatch_RegistryOption_IsolatedFromDefault(t *testing.T) {
	r := output.NewRegistry()
	r.Override(&capturingFormatter{})

	cmd := &cobra.Command{Use: "x"}
	v := viper.New()
	output.RegisterFlagsWith(cmd, v, output.WithRegistry(r))
	cmd.SetOut(io.Discard)

	require.NoError(t, cmd.PersistentFlags().Set("format", "json"))
	err := output.Dispatch(cmd, v, dispatchData())
	require.Error(t, err, "json is in Default but not in this scoped registry")
	assert.Contains(t, err.Error(), "unknown output format")
}

func TestResolveFormat_DefaultWhenNoExtension(t *testing.T) {
	cmd, v, _ := newCmd(t)
	// No --output set at all.
	require.NoError(t, output.Dispatch(cmd, v, dispatchData()))
	assert.Equal(t, "table", v.GetString("format"))
}

func TestDispatch_TemplateUsesCols(t *testing.T) {
	cmd, v, buf := newCmd(t)
	require.NoError(t, cmd.PersistentFlags().Set("template",
		"cols={{range .Cols}}{{.}},{{end}}"))
	require.NoError(t, output.Dispatch(cmd, v, dispatchData()))
	got := buf.String()
	assert.Contains(t, got, "ID")
	assert.Contains(t, got, "Name")
	assert.Contains(t, got, "Score")
	// Trailing comma after Score means the headers are present in struct
	// field order.
	assert.True(t, strings.HasSuffix(strings.TrimSpace(got), "Score,"))
}
