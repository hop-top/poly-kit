package output

import (
	"reflect"
	"strings"
	"testing"
)

// taskListRow mimics a wide list view: an overlong free-text column sitting
// between narrow ones, all at the default priority.
type taskListRow struct {
	ID       string `table:"ID"`
	Title    string `table:"Title"`
	Status   string `table:"Status"`
	Assigned string `table:"Assigned"`
	Due      string `table:"Due"`
	Stale    string `table:"Stale"`
	Blocked  string `table:"Blocked"`
}

type trackListRow struct {
	ID     string `table:"ID"`
	Name   string `table:"Name"`
	Status string `table:"Status"`
	Tasks  string `table:"Tasks"`
}

func longTaskRows() []taskListRow {
	return []taskListRow{
		{"T-0001", strings.Repeat("scaffold-the-widget ", 4)[:75], "in-progress", "alice", "2026-12-01", "no", "no"},
		{"T-0002", "short", "todo", "bob", "-", "yes", "-"},
	}
}

func longTrackRows() []trackListRow {
	return []trackListRow{
		{"TR-001", strings.Repeat("track-name-", 12)[:130], "active", "12/40"},
		{"TR-002", "tiny", "done", "3/3"},
	}
}

var policyWidths = []int{60, 80, 100, 120}

func assertNoWrap(t *testing.T, out string, width int) {
	t.Helper()
	lines := strings.Split(strings.TrimRight(out, "\n"), "\n")
	for _, l := range lines {
		if w := displayWidth(l); w > width {
			t.Errorf("width %d: line is %d cells: %q", width, w, l)
		}
	}
}

// TestRenderTable_TruncatesTitleInsteadOfDropping pins the policy: when the
// minimum widths of all columns fit, no column is dropped; the overlong cell
// is ellipsized.
func TestRenderTable_TruncatesTitleInsteadOfDropping(t *testing.T) {
	for _, width := range policyWidths {
		var sb strings.Builder
		if err := renderTableAt(&sb, longTaskRows(), nil, width); err != nil {
			t.Fatal(err)
		}
		out := sb.String()
		header := strings.SplitN(out, "\n", 2)[0]
		var last int
		for _, h := range []string{"ID", "Title", "Status", "Assigned", "Due", "Stale", "Blocked"} {
			i := strings.Index(header, h)
			if i < 0 {
				t.Errorf("width %d: header %q missing %q", width, header, h)
			}
			if i < last {
				t.Errorf("width %d: column %q out of order in %q", width, h, header)
			}
			last = i
		}
		if !strings.Contains(out, "…") {
			t.Errorf("width %d: expected ellipsized title:\n%s", width, out)
		}
		assertNoWrap(t, out, width)
		if got := len(strings.Split(strings.TrimRight(out, "\n"), "\n")); got != 3 {
			t.Errorf("width %d: want 3 lines (header+2 rows), got %d", width, got)
		}
	}
}

func TestRenderTable_TrackListKeepsColumns(t *testing.T) {
	for _, width := range policyWidths {
		var sb strings.Builder
		if err := renderTableAt(&sb, longTrackRows(), nil, width); err != nil {
			t.Fatal(err)
		}
		header := strings.SplitN(sb.String(), "\n", 2)[0]
		for _, h := range []string{"ID", "Name", "Status", "Tasks"} {
			if !strings.Contains(header, h) {
				t.Errorf("width %d: header %q missing %q", width, header, h)
			}
		}
		assertNoWrap(t, sb.String(), width)
	}
}

// TestRenderStyledTable_NeverWraps pins the TTY path: rows stay on one line
// (top border, header, separator, 2 rows, bottom border = 6 lines) and cells end in
// an ellipsis rather than wrapping mid-word.
func TestRenderStyledTable_NeverWraps(t *testing.T) {
	for _, width := range policyWidths {
		for name, rows := range map[string]any{"task": longTaskRows(), "track": longTrackRows()} {
			var sb strings.Builder
			if err := renderStyledTableAt(&sb, rows, nil, TableStyle{}, nil, width); err != nil {
				t.Fatal(err)
			}
			out := sb.String()
			assertNoWrap(t, out, width)
			if got := len(strings.Split(strings.TrimRight(out, "\n"), "\n")); got != 6 {
				t.Errorf("%s width %d: want 6 lines, got %d:\n%s", name, width, got, out)
			}
			if !strings.Contains(out, "…") {
				t.Errorf("%s width %d: expected ellipsis:\n%s", name, width, out)
			}
			for _, h := range []string{"ID", "Status"} {
				if !strings.Contains(out, h) {
					t.Errorf("%s width %d: missing %q:\n%s", name, width, h, out)
				}
			}
		}
	}
}

// TestSelectVisibleColumns_DropsOnlyWhenMinimaDontFit pins the drop rule:
// sum of per-column minimum widths decides, not natural widths.
func TestSelectVisibleColumns_DropsOnlyWhenMinimaDontFit(t *testing.T) {
	cols := tableColumns(reflect.TypeOf(taskListRow{}))
	rows := [][]string{{"T-0001", strings.Repeat("x", 75), "in-progress", "alice", "2026-12-01", "no", "no"}}
	// Minimum widths: 3+5+6+8+3+5+7 = 37, plus 6*2 separators = 49.
	if got := selectVisibleColumns(cols, rows, 49); len(got) != len(cols) {
		t.Errorf("width 49: want all columns, got %v", headersOf(got))
	}
	if got := selectVisibleColumns(cols, rows, 48); len(got) >= len(cols) {
		t.Errorf("width 48: want a column dropped, got %v", headersOf(got))
	}
}
