package output

import (
	"bytes"
	"encoding/json"
	"io"
	"strings"
	"testing"
	"time"
)

// fixedMetadata returns a Metadata with deterministic Source/FetchedAt/Method
// for assertions that compare against literal strings.
func fixedMetadata() Metadata {
	return Metadata{
		Source:    "spacex.example",
		FetchedAt: time.Date(2026, 5, 1, 12, 0, 0, 0, time.UTC),
		Method:    "GET /v1/missions",
	}
}

// withCapturedStderr swaps the package-level stderrWriter for a buffer,
// runs fn, restores the original, and returns whatever fn wrote.
func withCapturedStderr(t *testing.T, fn func() error) (string, error) {
	t.Helper()
	var buf bytes.Buffer
	prev := stderrWriter
	stderrWriter = &buf
	t.Cleanup(func() { stderrWriter = prev })
	err := fn()
	return buf.String(), err
}

func TestRender_WithProvenance_JSON_WrapsInDataMetaEnvelope(t *testing.T) {
	var w bytes.Buffer
	data := map[string]string{"name": "Demo-1"}
	m := fixedMetadata()

	if err := Render(&w, JSON, data, WithProvenance(m)); err != nil {
		t.Fatalf("Render: %v", err)
	}

	var got struct {
		Data map[string]string `json:"data"`
		Meta Metadata          `json:"_meta"`
	}
	if err := json.Unmarshal(w.Bytes(), &got); err != nil {
		t.Fatalf("decode envelope: %v\nraw: %s", err, w.String())
	}
	if got.Data["name"] != "Demo-1" {
		t.Errorf("data.name = %q, want Demo-1", got.Data["name"])
	}
	if got.Meta.Source != m.Source {
		t.Errorf("_meta.source = %q, want %q", got.Meta.Source, m.Source)
	}
	if !got.Meta.FetchedAt.Equal(m.FetchedAt) {
		t.Errorf("_meta.fetched_at = %v, want %v", got.Meta.FetchedAt, m.FetchedAt)
	}
	if got.Meta.Method != m.Method {
		t.Errorf("_meta.method = %q, want %q", got.Meta.Method, m.Method)
	}
}

func TestRender_WithProvenance_Table_EmitsStderrFooter(t *testing.T) {
	type row struct {
		Name string `table:"NAME"`
	}
	rows := []row{{Name: "Demo-1"}}
	m := fixedMetadata()

	var stdout bytes.Buffer
	stderrOut, err := withCapturedStderr(t, func() error {
		return Render(&stdout, Table, rows, WithProvenance(m))
	})
	if err != nil {
		t.Fatalf("Render: %v", err)
	}

	// Stdout still carries the table; provenance never steals column space.
	if !strings.Contains(stdout.String(), "NAME") {
		t.Errorf("table missing header; stdout=%q", stdout.String())
	}
	if !strings.Contains(stdout.String(), "Demo-1") {
		t.Errorf("table missing row; stdout=%q", stdout.String())
	}
	// _meta JSON wrapping must NOT leak into table mode.
	if strings.Contains(stdout.String(), "_meta") {
		t.Errorf("table stdout leaked _meta envelope: %q", stdout.String())
	}

	wantPrefix := "Source: " + m.Source
	if !strings.HasPrefix(stderrOut, wantPrefix) {
		t.Errorf("stderr footer = %q, want prefix %q", stderrOut, wantPrefix)
	}
	if !strings.Contains(stderrOut, m.FetchedAt.Format(time.RFC3339)) {
		t.Errorf("stderr footer missing fetched_at: %q", stderrOut)
	}
	if !strings.Contains(stderrOut, "method="+m.Method) {
		t.Errorf("stderr footer missing method=: %q", stderrOut)
	}
	if lines := strings.Count(strings.TrimRight(stderrOut, "\n"), "\n"); lines != 0 {
		t.Errorf("stderr footer must be a single line, got %d extra newlines: %q", lines, stderrOut)
	}
}

func TestRender_WithoutProvenance_Unchanged(t *testing.T) {
	data := map[string]string{"name": "Demo-1"}

	// Reference: render with no opts.
	var ref bytes.Buffer
	if err := Render(&ref, JSON, data); err != nil {
		t.Fatalf("ref Render: %v", err)
	}

	// New variadic call site, still no opts -> identical output.
	var got bytes.Buffer
	if err := Render(&got, JSON, data); err != nil {
		t.Fatalf("got Render: %v", err)
	}
	if got.String() != ref.String() {
		t.Errorf("no-opt render differs:\n got %q\nwant %q", got.String(), ref.String())
	}

	// Sanity: the no-opt JSON must NOT carry an _meta envelope.
	if strings.Contains(got.String(), "_meta") {
		t.Errorf("no-opt render leaked _meta: %q", got.String())
	}

	// Sanity: signature is still satisfied by the original 3-arg form
	// (existing callers compile unchanged). Compile-time check via assignment.
	//nolint:staticcheck // QF1011 — explicit type asserts Render's exact signature
	var _ func(io.Writer, Format, any, ...RenderOption) error = Render
}

// TestRender_WithProvenance_WithCols_JSON_KeepsEnvelope pins the bug
// where WithCols projected the {Data, Meta} envelope struct itself
// (instead of the payload underneath it) because the envelope carries no
// `table:""` tags. Projecting an untagged struct resolves zero columns,
// so the envelope collapsed to "{}" and both data and _meta vanished.
func TestRender_WithProvenance_WithCols_JSON_KeepsEnvelope(t *testing.T) {
	type row struct {
		Name  string `table:"NAME"`
		Score int    `table:"SCORE"`
	}
	rows := []row{{Name: "Demo-1", Score: 10}, {Name: "Demo-2", Score: 20}}
	m := fixedMetadata()

	var buf bytes.Buffer
	err := Render(&buf, JSON, rows, WithProvenance(m), WithCols([]string{"NAME"}))
	if err != nil {
		t.Fatalf("Render: %v", err)
	}

	var got struct {
		Data []map[string]any `json:"data"`
		Meta *Metadata        `json:"_meta"`
	}
	if err := json.Unmarshal(buf.Bytes(), &got); err != nil {
		t.Fatalf("decode envelope: %v\nraw: %s", err, buf.String())
	}
	if got.Meta == nil {
		t.Fatalf("_meta missing entirely; raw: %s", buf.String())
	}
	if got.Meta.Source != m.Source {
		t.Errorf("_meta.source = %q, want %q", got.Meta.Source, m.Source)
	}
	if len(got.Data) != 2 {
		t.Fatalf("data length = %d, want 2; raw: %s", len(got.Data), buf.String())
	}
	if _, ok := got.Data[0]["NAME"]; !ok {
		t.Errorf("data[0] missing projected column NAME; raw: %s", buf.String())
	}
	if _, ok := got.Data[0]["SCORE"]; ok {
		t.Errorf("data[0] retained unselected column SCORE; raw: %s", buf.String())
	}
}

// TestRender_WithProvenance_WithCols_YAML_KeepsEnvelope is the YAML twin
// of the JSON envelope-collapse regression above.
func TestRender_WithProvenance_WithCols_YAML_KeepsEnvelope(t *testing.T) {
	type row struct {
		Name  string `table:"NAME"`
		Score int    `table:"SCORE"`
	}
	rows := []row{{Name: "Demo-1", Score: 10}}
	m := fixedMetadata()

	var buf bytes.Buffer
	err := Render(&buf, YAML, rows, WithProvenance(m), WithCols([]string{"NAME"}))
	if err != nil {
		t.Fatalf("Render: %v", err)
	}

	out := buf.String()
	if !strings.Contains(out, "_meta") {
		t.Errorf("YAML output lost the _meta envelope under WithCols: %q", out)
	}
	if !strings.Contains(out, "data") {
		t.Errorf("YAML output lost the data key under WithCols: %q", out)
	}
	if !strings.Contains(out, "NAME") && !strings.Contains(out, "Demo-1") {
		t.Errorf("YAML output lost the projected payload under WithCols: %q", out)
	}
	if strings.Contains(out, "SCORE") || strings.Contains(out, "10") {
		t.Errorf("YAML output retained unselected column under WithCols: %q", out)
	}
}

// TestRender_WithCols_UnknownColumn_JSON_Errors pins WithCols's documented
// contract ("An unknown column name ... causes Render to return an
// error") for the JSON path. Render previously never validated cols at
// all (only the Dispatch layer did via validateCols), so an unknown
// column silently produced "{}" instead of an error.
func TestRender_WithCols_UnknownColumn_JSON_Errors(t *testing.T) {
	type row struct {
		Name string `table:"NAME"`
	}
	rows := []row{{Name: "Demo-1"}}

	var buf bytes.Buffer
	err := Render(&buf, JSON, rows, WithCols([]string{"Nope"}))
	if err == nil {
		t.Fatalf("Render returned nil error for unknown column; output: %q", buf.String())
	}
}

// TestRender_WithCols_UnknownColumn_YAML_Errors is the YAML twin of the
// JSON unknown-column regression above.
func TestRender_WithCols_UnknownColumn_YAML_Errors(t *testing.T) {
	type row struct {
		Name string `table:"NAME"`
	}
	rows := []row{{Name: "Demo-1"}}

	var buf bytes.Buffer
	err := Render(&buf, YAML, rows, WithCols([]string{"Nope"}))
	if err == nil {
		t.Fatalf("Render returned nil error for unknown column; output: %q", buf.String())
	}
}

func TestCachedFromMetadata_ComputesAge(t *testing.T) {
	base := Metadata{
		Source:    "spacex.example",
		FetchedAt: time.Now().UTC(),
		Method:    "GET /v1/missions",
	}
	fetchedAt := time.Now().Add(-30 * time.Second)

	got := CachedFromMetadata(base, fetchedAt)

	if !got.Cached {
		t.Errorf("Cached = false, want true")
	}
	// Allow a generous margin for slow CI; the helper computes
	// time.Since(fetchedAt), so the result must be >= 30s and within
	// a few seconds of it.
	if got.CacheAge < 30*time.Second {
		t.Errorf("CacheAge = %v, want >= 30s", got.CacheAge)
	}
	if got.CacheAge > 60*time.Second {
		t.Errorf("CacheAge = %v, want < 60s (sanity)", got.CacheAge)
	}
	// Required fields preserved.
	if got.Source != base.Source || got.Method != base.Method {
		t.Errorf("required fields mutated: got %+v, base %+v", got, base)
	}
}
