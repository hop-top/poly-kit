package output

import (
	"reflect"
	"strings"
	"testing"
)

type truncRow struct {
	ID      string `table:"ID"`
	Title   string `table:"Title"`
	Status  string `table:"Status"`
	Blocked string `table:"Blocked"`
}

// headersOf collapses a visible column set down to its headers for
// comparison against the full schema.
func headersOf(cols []column) []string {
	out := make([]string, len(cols))
	for i, c := range cols {
		out[i] = c.header
	}
	return out
}

// TestSelectVisibleColumns_OneOverlongCellKeepsEveryColumn pins the primary
// reported shape: a single pathological cell must not evict its neighbors.
// No subset containing that column can ever fit, so dropping is futile and
// the renderer must truncate instead.
//
// Asserted across the width boundary rather than at one size: a fix that
// merely shifted the threshold would still fail here.
func TestSelectVisibleColumns_OneOverlongCellKeepsEveryColumn(t *testing.T) {
	cols := tableColumns(reflect.TypeOf(truncRow{}))
	rows := [][]string{
		{"T-0001", strings.Repeat("x", 250), "TODO", "-"},
		{"T-0002", "short", "TODO", "-"},
	}
	for _, width := range []int{199, 200, 201, 400} {
		visible := selectVisibleColumns(cols, rows, width)
		if len(visible) != len(cols) {
			t.Errorf("width=%d: got %d columns %v, want all %d %v",
				width, len(visible), headersOf(visible),
				len(cols), headersOf(cols))
		}
	}
}

// TestSelectVisibleColumns_ManyNarrowRowsKeepEveryColumn pins the second
// shape: row COUNT must never influence column visibility. Only the widest
// cell per column contributes to the budget.
func TestSelectVisibleColumns_ManyNarrowRowsKeepEveryColumn(t *testing.T) {
	cols := tableColumns(reflect.TypeOf(truncRow{}))
	rows := make([][]string, 5000)
	for i := range rows {
		rows[i] = []string{"T-0001", "short", "TODO", "-"}
	}
	for _, width := range []int{199, 200, 201, 400} {
		visible := selectVisibleColumns(cols, rows, width)
		if len(visible) != len(cols) {
			t.Errorf("width=%d: got %d columns %v, want all %d %v",
				width, len(visible), headersOf(visible),
				len(cols), headersOf(cols))
		}
	}
}

// TestRenderTable_OverlongCellKeepsHeaders exercises the same defect through
// the public render path, where it was originally observed: the header row
// collapsed to "ID" alone.
func TestRenderTable_OverlongCellKeepsHeaders(t *testing.T) {
	rows := []truncRow{
		{"T-0001", strings.Repeat("x", 250), "TODO", "-"},
		{"T-0002", "short", "TODO", "-"},
	}
	var sb strings.Builder
	if err := renderTable(&sb, rows, nil); err != nil {
		t.Fatalf("renderTable: %v", err)
	}
	header := strings.SplitN(sb.String(), "\n", 2)[0]
	for _, want := range []string{"ID", "Title", "Status", "Blocked"} {
		if !strings.Contains(header, want) {
			t.Errorf("header %q missing column %q", header, want)
		}
	}
}

// TestRenderTable_OverlongCellFitsBudget asserts the overflow is actually
// absorbed rather than merely tolerated: every emitted line must respect the
// width budget once truncation is in play.
func TestRenderTable_OverlongCellFitsBudget(t *testing.T) {
	rows := []truncRow{
		{"T-0001", strings.Repeat("x", 250), "TODO", "-"},
		{"T-0002", "short", "TODO", "-"},
	}
	var sb strings.Builder
	if err := renderTable(&sb, rows, nil); err != nil {
		t.Fatalf("renderTable: %v", err)
	}
	for _, line := range strings.Split(strings.TrimRight(sb.String(), "\n"), "\n") {
		if w := displayWidth(line); w > fallbackWidth {
			t.Errorf("line width %d exceeds budget %d: %q", w, fallbackWidth, line)
		}
	}
}

// TestFitCellsToWidth_PreservesNarrowCells guards against over-eager
// truncation: a table that already fits must pass through byte-identical.
func TestFitCellsToWidth_PreservesNarrowCells(t *testing.T) {
	cols := tableColumns(reflect.TypeOf(truncRow{}))
	rows := [][]string{
		{"T-0001", "short", "TODO", "-"},
		{"T-0002", "shorter", "DONE", "-"},
	}
	got := fitCellsToWidth(cols, rows, 200)
	if !reflect.DeepEqual(got, rows) {
		t.Errorf("narrow rows were altered:\n got %v\nwant %v", got, rows)
	}
}

// TestFitCellsToWidth_WideRunes pins width-aware (not byte-aware) ellipsis.
// Each CJK rune occupies two display cells, so a byte-based or rune-count
// based truncation would overshoot the budget.
func TestFitCellsToWidth_WideRunes(t *testing.T) {
	type wideRow struct {
		ID   string `table:"ID"`
		Text string `table:"Text"`
	}
	cols := tableColumns(reflect.TypeOf(wideRow{}))
	rows := [][]string{{"T-1", strings.Repeat("世", 100)}}

	const budget = 40
	got := fitCellsToWidth(cols, rows, budget)
	total := 0
	for i, cell := range got[0] {
		total += displayWidth(cell)
		if i < len(got[0])-1 {
			total += columnSeparator
		}
	}
	if total > budget {
		t.Errorf("wide-rune row measured %d display cells, budget %d: %q",
			total, budget, got[0])
	}
	// Truncation must not split a rune mid-sequence.
	if !strings.ContainsRune(got[0][1], '世') {
		t.Errorf("expected surviving wide runes, got %q", got[0][1])
	}
}

// TestSelectVisibleColumns_DropStillHelpsWhenItCan keeps the legitimate half
// of the feature alive: many narrow columns on a genuinely narrow terminal
// should still shed the lowest-priority ones.
func TestSelectVisibleColumns_DropStillHelpsWhenItCan(t *testing.T) {
	cols := tableColumns(reflect.TypeOf(widthRow{}))
	rows := [][]string{
		{"T-001", "A short title", "feature", "active", "alice"},
		{"T-002", "Another title", "bug", "todo", "bob"},
	}
	visible := selectVisibleColumns(cols, rows, 30)
	if len(visible) >= len(cols) {
		t.Errorf("expected some columns dropped at width 30, got %v",
			headersOf(visible))
	}
	seen := map[string]bool{}
	for _, c := range visible {
		seen[c.header] = true
	}
	if seen["Assignee"] {
		t.Errorf("lowest-priority Assignee should drop first; got %v",
			headersOf(visible))
	}
	if !seen["ID"] || !seen["Title"] {
		t.Errorf("highest-priority columns must survive; got %v",
			headersOf(visible))
	}
}
