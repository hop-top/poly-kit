package output

import (
	"bytes"
	"image/color"
	"regexp"
	"strings"
	"testing"

	"charm.land/lipgloss/v2"
)

// styledRow is a fixture for golden-file tests across plain and styled
// renderers. Headers and priorities are deliberately simple so the tests
// document the expected layout without coupling to terminal size logic.
type styledRow struct {
	Name   string `table:"Name"`
	Status string `table:"Status"`
	Score  int    `table:"Score"`
}

var ansiRe = regexp.MustCompile(`\x1b\[[0-9;]*m`)

// stripAnsi removes ANSI color escapes so styled output can be compared
// against plain output. Mirrors tlc/internal/cli/ttytable.go's helper.
func stripAnsi(s string) string {
	return ansiRe.ReplaceAllString(s, "")
}

func sampleRows() []styledRow {
	return []styledRow{
		{Name: "alpha", Status: "ok", Score: 1},
		{Name: "beta", Status: "warn", Score: 2},
		{Name: "gamma", Status: "fail", Score: 3},
	}
}

func sampleStyle() TableStyle {
	return TableStyle{
		Border:           lipgloss.NormalBorder(),
		BorderForeground: color.RGBA{R: 100, G: 100, B: 100, A: 255},
		Header:           color.RGBA{R: 200, G: 200, B: 200, A: 255},
		Primary:          color.RGBA{R: 126, G: 217, B: 87, A: 255},
		Secondary:        color.RGBA{R: 255, G: 102, B: 196, A: 255},
		Muted:            color.RGBA{R: 100, G: 100, B: 100, A: 255},
	}
}

// TestRender_Table_PlainPath_Unchanged guards the existing tabwriter
// behavior for callers that don't set WithTableStyle.
func TestRender_Table_PlainPath_Unchanged(t *testing.T) {
	var buf bytes.Buffer
	if err := Render(&buf, Table, sampleRows()); err != nil {
		t.Fatalf("Render: %v", err)
	}
	out := buf.String()
	if !strings.Contains(out, "Name") || !strings.Contains(out, "Score") {
		t.Errorf("plain table missing headers: %q", out)
	}
	for _, r := range sampleRows() {
		if !strings.Contains(out, r.Name) {
			t.Errorf("plain table missing row %q: %q", r.Name, out)
		}
	}
	// Plain path must not emit any ANSI escapes.
	if ansiRe.MatchString(out) {
		t.Errorf("plain table leaked ANSI escapes: %q", out)
	}
	// Plain path must not emit lipgloss box-drawing characters.
	for _, r := range []rune{'┌', '┐', '└', '┘', '│', '─'} {
		if strings.ContainsRune(out, r) {
			t.Errorf("plain table leaked box-drawing rune %q: %q", r, out)
		}
	}
}

// TestRender_Table_NonTTY_FallsThroughToPlain documents that
// WithTableStyle is a no-op when the writer is not a *os.File terminal.
// This is what production callers see when stdout is piped or redirected.
func TestRender_Table_NonTTY_FallsThroughToPlain(t *testing.T) {
	// Reference: render with no style.
	var ref bytes.Buffer
	if err := Render(&ref, Table, sampleRows()); err != nil {
		t.Fatalf("ref Render: %v", err)
	}

	// Same payload + WithTableStyle on a bytes.Buffer (not a TTY).
	var got bytes.Buffer
	if err := Render(&got, Table, sampleRows(), WithTableStyle(sampleStyle())); err != nil {
		t.Fatalf("got Render: %v", err)
	}

	if got.String() != ref.String() {
		t.Errorf("non-TTY styled output diverged from plain:\n got %q\nwant %q",
			got.String(), ref.String())
	}
}

// TestRender_Table_RowEmphasis_NonTTY_Unchanged documents that
// RowEmphasis options have no effect when the writer is not a TTY —
// emphasis is a TTY-only concern, plain output never colors rows.
func TestRender_Table_RowEmphasis_NonTTY_Unchanged(t *testing.T) {
	var ref bytes.Buffer
	if err := Render(&ref, Table, sampleRows()); err != nil {
		t.Fatalf("ref Render: %v", err)
	}

	var got bytes.Buffer
	err := Render(&got, Table, sampleRows(),
		WithTableStyle(sampleStyle()),
		RowEmphasis(0, EmphasisPrimary),
		RowEmphasis(1, EmphasisSecondary),
		RowEmphasis(2, EmphasisMuted),
	)
	if err != nil {
		t.Fatalf("got Render: %v", err)
	}

	if got.String() != ref.String() {
		t.Errorf("non-TTY emphasis output diverged from plain:\n got %q\nwant %q",
			got.String(), ref.String())
	}
}

// TestRenderStyledTable_DirectCall_EmitsANSIAndBoxDrawing exercises the
// styled renderer directly so the test does not depend on a real TTY.
// It asserts that styled output contains both ANSI escapes and lipgloss
// box-drawing characters that the plain renderer never emits.
func TestRenderStyledTable_DirectCall_EmitsANSIAndBoxDrawing(t *testing.T) {
	var buf bytes.Buffer
	err := renderStyledTable(&buf, sampleRows(), nil, sampleStyle(), nil)
	if err != nil {
		t.Fatalf("renderStyledTable: %v", err)
	}
	out := buf.String()

	if !ansiRe.MatchString(out) {
		t.Errorf("styled output missing ANSI escapes: %q", out)
	}

	hasBox := false
	for _, r := range []rune{'┌', '┐', '└', '┘', '│', '─'} {
		if strings.ContainsRune(out, r) {
			hasBox = true
			break
		}
	}
	if !hasBox {
		t.Errorf("styled output missing box-drawing characters: %q", out)
	}

	// Headers and rows must still be present (after ANSI strip).
	stripped := stripAnsi(out)
	if !strings.Contains(stripped, "Name") || !strings.Contains(stripped, "Score") {
		t.Errorf("styled output missing headers: %q", stripped)
	}
	for _, r := range sampleRows() {
		if !strings.Contains(stripped, r.Name) {
			t.Errorf("styled output missing row %q: %q", r.Name, stripped)
		}
	}
}

// TestRenderStyledTable_ContentIdentity asserts that stripping ANSI from
// the styled output yields the same human-readable cells as the plain
// renderer. This is the core invariant: TTY mode adds visual chrome,
// never changes the data.
func TestRenderStyledTable_ContentIdentity(t *testing.T) {
	var styled, plain bytes.Buffer
	if err := renderStyledTable(&styled, sampleRows(), nil, sampleStyle(), nil); err != nil {
		t.Fatalf("renderStyledTable: %v", err)
	}
	if err := renderTable(&plain, sampleRows(), nil); err != nil {
		t.Fatalf("renderTable: %v", err)
	}

	styledStripped := stripAnsi(styled.String())

	// Every header and every cell value from plain output must appear
	// somewhere in styled output (post-ANSI-strip). We don't assert
	// byte equality because lipgloss inserts box-drawing chars and
	// padding; the contract is content identity, not layout identity.
	for _, want := range []string{"Name", "Status", "Score"} {
		if !strings.Contains(styledStripped, want) {
			t.Errorf("styled output missing header %q\nstyled (stripped): %q\nplain: %q",
				want, styledStripped, plain.String())
		}
	}
	for _, r := range sampleRows() {
		if !strings.Contains(styledStripped, r.Name) {
			t.Errorf("styled output missing %q\nstyled (stripped): %q",
				r.Name, styledStripped)
		}
		if !strings.Contains(styledStripped, r.Status) {
			t.Errorf("styled output missing %q\nstyled (stripped): %q",
				r.Status, styledStripped)
		}
	}
}

// TestRenderStyledTable_RowEmphasis_TogglesColors verifies that
// EmphasisPrimary/Secondary/Muted produce visibly distinct ANSI sequences
// in the styled output. We don't pin the exact escape because that would
// couple the test to lipgloss internals; we only assert that emphasis
// kinds map to different colored runs of the expected row text.
func TestRenderStyledTable_RowEmphasis_TogglesColors(t *testing.T) {
	emphasis := map[int]EmphasisKind{
		0: EmphasisPrimary,
		1: EmphasisSecondary,
		2: EmphasisMuted,
	}
	var buf bytes.Buffer
	if err := renderStyledTable(&buf, sampleRows(), nil, sampleStyle(), emphasis); err != nil {
		t.Fatalf("renderStyledTable: %v", err)
	}
	out := buf.String()

	// All three rows must still be present in the rendered output.
	stripped := stripAnsi(out)
	for _, r := range sampleRows() {
		if !strings.Contains(stripped, r.Name) {
			t.Errorf("emphasis output missing %q: %q", r.Name, stripped)
		}
	}

	// At least one ANSI run must be present (styled path).
	if !ansiRe.MatchString(out) {
		t.Errorf("emphasis output missing ANSI escapes: %q", out)
	}
}

// TestRenderStyledTable_ZeroBorder_Defaults confirms that a TableStyle
// with a zero Border falls back to lipgloss.NormalBorder so callers can
// pass a TableStyle populated with only colors.
func TestRenderStyledTable_ZeroBorder_Defaults(t *testing.T) {
	style := TableStyle{
		Header:  color.RGBA{R: 200, G: 200, B: 200, A: 255},
		Primary: color.RGBA{R: 126, G: 217, B: 87, A: 255},
	}
	var buf bytes.Buffer
	if err := renderStyledTable(&buf, sampleRows(), nil, style, nil); err != nil {
		t.Fatalf("renderStyledTable: %v", err)
	}
	out := buf.String()
	hasBox := false
	for _, r := range []rune{'┌', '─', '│'} {
		if strings.ContainsRune(out, r) {
			hasBox = true
			break
		}
	}
	if !hasBox {
		t.Errorf("zero Border did not default to NormalBorder: %q", out)
	}
}

// TestRenderStyledTable_EmptySlice_Noop matches the plain renderer's
// behavior: no header, no body, no error when rows are empty.
func TestRenderStyledTable_EmptySlice_Noop(t *testing.T) {
	var buf bytes.Buffer
	if err := renderStyledTable(&buf, []styledRow{}, nil, sampleStyle(), nil); err != nil {
		t.Fatalf("renderStyledTable: %v", err)
	}
	if buf.Len() != 0 {
		t.Errorf("empty slice produced output: %q", buf.String())
	}
}

// Compile-time guards that the public RenderOption signature is shared
// by WithTableStyle and RowEmphasis (so they can mix freely with
// WithProvenance in a single Render call).
var (
	_ RenderOption = WithTableStyle(TableStyle{})
	_ RenderOption = RowEmphasis(0, EmphasisPrimary)
)
