package output

import (
	"reflect"
	"testing"
)

func TestParseTableTag(t *testing.T) {
	cases := []struct {
		tag      string
		wantHdr  string
		wantPrio int
	}{
		{"ID", "ID", 5},
		{"ID,priority=9", "ID", 9},
		{"ID,priority=0", "ID", 0},
		{"Notes,priority=2", "Notes", 2},
		{"X, priority=7", "X", 7},
		{"X,priority=99", "X", 9},
		{"X,priority=-3", "X", 0},
		{"X,priority=foo", "X", 5},
		{"X,unknown=1", "X", 5},
		{"X,priority=4,extra", "X", 4},
	}
	for _, c := range cases {
		gotHdr, gotPrio := parseTableTag(c.tag)
		if gotHdr != c.wantHdr || gotPrio != c.wantPrio {
			t.Errorf("parseTableTag(%q) = (%q, %d), want (%q, %d)",
				c.tag, gotHdr, gotPrio, c.wantHdr, c.wantPrio)
		}
	}
}

type widthRow struct {
	ID       string `table:"ID,priority=9"`
	Title    string `table:"Title,priority=8"`
	Type     string `table:"Type,priority=4"`
	Status   string `table:"Status,priority=7"`
	Assignee string `table:"Assignee,priority=2"`
}

func TestSelectVisibleColumns_FitsAll(t *testing.T) {
	cols := tableColumns(reflect.TypeOf(widthRow{}))
	rows := [][]string{
		{"T-001", "Hello", "feat", "todo", "alice"},
	}
	visible := selectVisibleColumns(cols, rows, 200)
	if len(visible) != len(cols) {
		t.Fatalf("expected all %d columns, got %d", len(cols), len(visible))
	}
}

func TestSelectVisibleColumns_DropsLowestPriority(t *testing.T) {
	cols := tableColumns(reflect.TypeOf(widthRow{}))
	rows := [][]string{
		{"T-001", "A short title", "feature", "active", "alice"},
		{"T-002", "Another title", "bug", "todo", "bob"},
	}
	// Natural width ~ 5+13+7+6+5 + 4*2 = 44; force budget below.
	visible := selectVisibleColumns(cols, rows, 30)
	headerSet := map[string]bool{}
	for _, c := range visible {
		headerSet[c.header] = true
	}
	if headerSet["Assignee"] {
		t.Errorf("Assignee (priority 2) should be hidden first; visible=%v", headerSet)
	}
	// ID (9) and Title (8) must survive.
	if !headerSet["ID"] || !headerSet["Title"] {
		t.Errorf("highest-priority columns missing; visible=%v", headerSet)
	}
}

func TestSelectVisibleColumns_StableOrder(t *testing.T) {
	cols := tableColumns(reflect.TypeOf(widthRow{}))
	rows := [][]string{
		{"T-001", "Title", "feature", "active", "alice"},
	}
	visible := selectVisibleColumns(cols, rows, 30)
	// Whatever survives must remain in struct order.
	for i := 1; i < len(visible); i++ {
		if visible[i-1].colIdx >= visible[i].colIdx {
			t.Errorf("column order not preserved: %d before %d",
				visible[i-1].colIdx, visible[i].colIdx)
		}
	}
}

func TestSelectVisibleColumns_TooNarrow(t *testing.T) {
	cols := tableColumns(reflect.TypeOf(widthRow{}))
	rows := [][]string{{"T-001", "Title", "feature", "active", "alice"}}
	// Width 5 — even the highest-priority single column won't fit cleanly.
	visible := selectVisibleColumns(cols, rows, 5)
	if len(visible) == 0 {
		t.Errorf("must return at least the original column set on extreme narrowness")
	}
}

func TestSelectVisibleColumns_ZeroWidth(t *testing.T) {
	cols := tableColumns(reflect.TypeOf(widthRow{}))
	rows := [][]string{{"T-001", "Title", "feature", "active", "alice"}}
	visible := selectVisibleColumns(cols, rows, 0)
	if len(visible) != len(cols) {
		t.Errorf("ttyWidth=0 must mean 'unknown — render all', got %d/%d",
			len(visible), len(cols))
	}
}

type compatRow struct {
	ID   string `table:"ID"`
	Name string `table:"Name"`
}

func TestTableColumns_BackwardCompatibleNoPriority(t *testing.T) {
	cols := tableColumns(reflect.TypeOf(compatRow{}))
	if len(cols) != 2 {
		t.Fatalf("expected 2 columns, got %d", len(cols))
	}
	for _, c := range cols {
		if c.priority != defaultPriority {
			t.Errorf("col %q priority = %d, want %d (default)",
				c.header, c.priority, defaultPriority)
		}
	}
}

type ignoredRow struct {
	Visible string `table:"V,priority=3"`
	Skip    string `table:"-"`
	Untag   string
}

func TestTableColumns_IgnoresDashAndUntagged(t *testing.T) {
	cols := tableColumns(reflect.TypeOf(ignoredRow{}))
	if len(cols) != 1 || cols[0].header != "V" {
		t.Errorf("expected only 'V' column, got %+v", cols)
	}
}
