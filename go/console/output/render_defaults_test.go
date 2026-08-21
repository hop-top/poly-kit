package output_test

import (
	"bytes"
	"encoding/csv"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"hop.top/kit/go/console/output"
)

// adopterRow is the shape a typical adopter renders: a flat struct
// carrying `table:""` tags, which drive the table, csv, text, and human
// formatters alike.
type adopterRow struct {
	ID    string `table:"ID"`
	Name  string `table:"Name"`
	Score int    `table:"Score"`
}

func adopterRows() []adopterRow {
	return []adopterRow{
		{ID: "1", Name: "alpha", Score: 10},
		{ID: "2", Name: "beta", Score: 20},
	}
}

// TestRender_CSV_MaterializesDeclaredOptionDefaults is the regression
// guard for the silent-empty-csv bug.
//
// output.Render invoked Formatter.Render with a nil Options map, so
// csv's declared delimiter default (",") never materialized. Every
// adopter calling Render(w, "csv", rows) — the documented entry point —
// therefore tripped the one-character delimiter check and got an error
// or, when the caller ignored it, zero bytes and exit 0.
//
// Options defaults are declared data on the Formatter; Render must fill
// them in exactly as Dispatch does via ParseOptions.
func TestRender_CSV_MaterializesDeclaredOptionDefaults(t *testing.T) {
	var buf bytes.Buffer
	require.NoError(t, output.Render(&buf, output.CSV, adopterRows()))

	require.NotZero(t, buf.Len(),
		"csv via Render emitted 0 bytes; a declared format must never silently produce nothing")

	records, err := csv.NewReader(bytes.NewReader(buf.Bytes())).ReadAll()
	require.NoError(t, err, "csv output must parse; raw: %q", buf.String())
	require.Len(t, records, 3, "want header + 2 rows; raw: %q", buf.String())

	assert.Equal(t, []string{"ID", "Name", "Score"}, records[0])
	assert.Equal(t, []string{"1", "alpha", "10"}, records[1])
	assert.Equal(t, []string{"2", "beta", "20"}, records[2])
}

// TestRender_CSV_SingleStructMaterializesDefaults covers the non-slice
// path through the same shim: a lone struct hits the identical nil-Options
// bug, and adopters rendering a single record are just as exposed.
func TestRender_CSV_SingleStructMaterializesDefaults(t *testing.T) {
	var buf bytes.Buffer
	require.NoError(t, output.Render(&buf, output.CSV, adopterRow{ID: "7", Name: "solo", Score: 3}))

	require.NotZero(t, buf.Len(), "csv via Render emitted 0 bytes for a single struct")
	assert.Equal(t, "ID,Name,Score\n7,solo,3\n", buf.String())
}

// TestRender_Text_MaterializesDeclaredOptionDefaults pins the text
// formatter's defaults through the same shim. text declares style="kv"
// and separator="="; with nil Options both fell back to in-formatter
// hardcoded fallbacks. Those fallbacks masked the bug for text but do
// not exist for every option, so assert the declared defaults reach the
// formatter rather than relying on the duplicate.
func TestRender_Text_MaterializesDeclaredOptionDefaults(t *testing.T) {
	var buf bytes.Buffer
	require.NoError(t, output.Render(&buf, output.Text, adopterRows()))

	require.NotZero(t, buf.Len(), "text via Render emitted 0 bytes")
	assert.Contains(t, buf.String(), "ID=1")
	assert.Contains(t, buf.String(), "Name=alpha")
	assert.Contains(t, buf.String(), "Score=20")
}

// TestRender_AllRegisteredFormatsEmitOutput is the broad guard: every
// format in the Default registry must produce bytes for a tagged payload
// when reached through the shim adopters actually call. A registered
// format that emits nothing and returns nil is the worst outcome for the
// agent audience these CLIs target.
func TestRender_AllRegisteredFormatsEmitOutput(t *testing.T) {
	for _, format := range output.Default.Keys() {
		t.Run(format, func(t *testing.T) {
			var buf bytes.Buffer
			require.NoError(t, output.Render(&buf, format, adopterRows()))
			require.NotZero(t, buf.Len(),
				"format %q emitted 0 bytes on a tagged payload", format)
			assert.Contains(t, buf.String(), "alpha",
				"format %q output is missing the payload", format)
		})
	}
}

// TestRender_WithProvenance_TagDrivenFormatsStillEmitPayload guards the
// second half of the bug.
//
// Render wrapped the payload in an untagged {Data, Meta} struct for every
// format except table. csv, text, and human derive their columns from
// `table:""` tags, so they resolved zero columns on the wrapper and
// returned nil having written nothing — silently, with exit 0.
//
// Those formats are flat projections with nowhere to nest an envelope, so
// they must render the payload unwrapped and receive provenance the way
// table does: as a trailing stderr footer.
func TestRender_WithProvenance_TagDrivenFormatsStillEmitPayload(t *testing.T) {
	meta := output.Metadata{
		Source:    "example.test",
		FetchedAt: time.Date(2026, 5, 1, 12, 0, 0, 0, time.UTC),
		Method:    "GET /v1/rows",
	}

	for _, format := range []string{output.CSV, output.Text, output.Human} {
		t.Run(format, func(t *testing.T) {
			var buf bytes.Buffer
			stderr, err := captureRenderStderr(t, func() error {
				return output.Render(&buf, format, adopterRows(), output.WithProvenance(meta))
			})
			require.NoError(t, err)

			require.NotZero(t, buf.Len(),
				"format %q with provenance emitted 0 bytes; the envelope wrapper must not swallow the payload",
				format)
			assert.Contains(t, buf.String(), "alpha",
				"format %q lost the payload under WithProvenance", format)
			assert.NotContains(t, buf.String(), "_meta",
				"format %q must not inline the envelope into a flat projection", format)

			assert.Contains(t, stderr, "Source: example.test",
				"format %q dropped the provenance footer; provenance must reach the caller on every path",
				format)
		})
	}
}

// TestRender_WithProvenance_StructuredFormatsKeepEnvelope pins the
// unchanged half of the contract: json and yaml still wrap in
// {data, _meta} and must NOT gain a stderr footer.
func TestRender_WithProvenance_StructuredFormatsKeepEnvelope(t *testing.T) {
	meta := output.Metadata{
		Source:    "example.test",
		FetchedAt: time.Date(2026, 5, 1, 12, 0, 0, 0, time.UTC),
		Method:    "GET /v1/rows",
	}

	for _, format := range []string{output.JSON, output.YAML} {
		t.Run(format, func(t *testing.T) {
			var buf bytes.Buffer
			stderr, err := captureRenderStderr(t, func() error {
				return output.Render(&buf, format, adopterRows(), output.WithProvenance(meta))
			})
			require.NoError(t, err)

			assert.Contains(t, buf.String(), "_meta",
				"format %q must keep the inline provenance envelope", format)
			assert.Contains(t, buf.String(), "alpha")
			assert.Empty(t, stderr,
				"format %q carries provenance inline and must not also emit a stderr footer", format)
		})
	}
}

// captureRenderStderr redirects the package-level provenance footer
// writer for the duration of fn and returns what was written to it.
func captureRenderStderr(t *testing.T, fn func() error) (string, error) {
	t.Helper()
	var buf bytes.Buffer
	restore := output.SetStderrWriterForTest(&buf)
	t.Cleanup(restore)
	err := fn()
	return strings.TrimSpace(buf.String()), err
}
