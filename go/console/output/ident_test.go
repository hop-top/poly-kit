package output

import (
	"bytes"
	"strings"
	"testing"
)

const (
	uuidA = "01a1e56b-bc27-77ac-854a-6a224cb16a01"
	uuidB = "01a1e56b-bc27-77ac-854a-6a224cb16a02"
	uuidC = "01a1e56b-bc27-77ac-854a-6a224cb16a03"
	uuidD = "01a1e56b-bc27-77ac-854a-6a224cb16a04"
)

// identityRow mimics `heo identity list`: four UUID columns, none marked.
type identityRow struct {
	ID      string `table:"ID"`
	Owner   string `table:"Owner"`
	Parent  string `table:"Parent"`
	Contact string `table:"Contact"`
	Label   string `table:"Label"`
}

func identityRows() []identityRow {
	return []identityRow{
		{uuidA, uuidB, uuidC, uuidD, strings.Repeat("l", 80)},
		{uuidB, uuidC, uuidD, uuidA, "x"},
	}
}

// markedRow is the same shape with identifier columns marked.
type markedRow struct {
	ID    string `table:"ID,priority=9,id"`
	Name  string `table:"Name,priority=8"`
	Notes string `table:"Notes,priority=2"`
	Ref   string `table:"Ref,priority=1,id"`
}

func markedRows() []markedRow {
	return []markedRow{
		{uuidA, strings.Repeat("n", 60), strings.Repeat("z", 80), uuidB},
		{uuidC, "short", "short", uuidD},
	}
}

func TestParseTableTagOpts_ID(t *testing.T) {
	h, p, id := parseTableTagOpts("ID, priority=9 , id")
	if h != "ID" || p != 9 || !id {
		t.Errorf("got (%q,%d,%v), want (ID,9,true)", h, p, id)
	}
	if _, _, id := parseTableTagOpts("Name,priority=3"); id {
		t.Error("untagged column marked as identifier")
	}
}

// TestRenderTable_NonTTYKeepsWholeCells is the regression: a buffer (not a
// terminal) must get every column with every cell whole, however wide.
func TestRenderTable_NonTTYKeepsWholeCells(t *testing.T) {
	var sb strings.Builder
	if err := renderTable(&sb, identityRows(), nil); err != nil {
		t.Fatal(err)
	}
	out := sb.String()
	if strings.Contains(out, ellipsis) {
		t.Errorf("non-TTY output contains ellipsis:\n%s", out)
	}
	for _, id := range []string{uuidA, uuidB, uuidC, uuidD} {
		if !strings.Contains(out, id) {
			t.Errorf("id %s missing from non-TTY output:\n%s", id, out)
		}
	}
	for _, h := range []string{"ID", "Owner", "Parent", "Contact", "Label"} {
		if !strings.Contains(strings.SplitN(out, "\n", 2)[0], h) {
			t.Errorf("column %q dropped off-TTY", h)
		}
	}
}

func TestRenderTable_NonTTYNoDropAtAnyWidthOfOutput(t *testing.T) {
	rows := []truncRow{{"T-0001", strings.Repeat("x", 500), "TODO", "-"}}
	var sb strings.Builder
	if err := renderTable(&sb, rows, nil); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(sb.String(), strings.Repeat("x", 500)) {
		t.Error("overlong cell clipped off-TTY")
	}
}

func TestOutputWidth_NonTTYUnbounded(t *testing.T) {
	if got := outputWidth(&bytes.Buffer{}); got != unboundedWidth {
		t.Errorf("buffer width = %d, want unbounded", got)
	}
}

// TestRenderTableAt_IdentifierColumnsNeverEllipsized: on a bounded width the
// other columns shrink while marked columns stay whole.
func TestRenderTableAt_IdentifierColumnsNeverEllipsized(t *testing.T) {
	// ID(36)+Ref(36)+gutters(2*3) = 78 of 100: Name/Notes must shrink.
	for _, width := range []int{100, 90, 80} {
		var sb strings.Builder
		if err := renderTableAt(&sb, markedRows(), nil, width); err != nil {
			t.Fatal(err)
		}
		out := sb.String()
		for _, id := range []string{uuidA, uuidB, uuidC, uuidD} {
			if !strings.Contains(out, id) {
				t.Errorf("width %d: id %s clipped:\n%s", width, id, out)
			}
		}
		if !strings.Contains(out, ellipsis) {
			t.Errorf("width %d: expected other columns ellipsized:\n%s", width, out)
		}
		assertNoWrap(t, out, width)
	}
}

// TestRenderTableAt_DropsOthersBeforeEllipsizingIdentifiers: when identifiers
// plus the others' minimums overflow, others drop lowest priority first,
// identifiers survive whole.
func TestRenderTableAt_DropsOthersBeforeEllipsizingIdentifiers(t *testing.T) {
	var sb strings.Builder
	width := 76 // ID+Ref+gutter = 74; no room for Name/Notes minimums
	if err := renderTableAt(&sb, markedRows(), nil, width); err != nil {
		t.Fatal(err)
	}
	out := sb.String()
	header := strings.SplitN(out, "\n", 2)[0]
	if strings.Contains(header, "Notes") || strings.Contains(header, "Name") {
		t.Errorf("non-identifier columns not dropped: %q", header)
	}
	if strings.Contains(out, ellipsis) {
		t.Errorf("identifier ellipsized:\n%s", out)
	}
	for _, id := range []string{uuidA, uuidB, uuidC, uuidD} {
		if !strings.Contains(out, id) {
			t.Errorf("id %s missing:\n%s", id, out)
		}
	}
}

// TestLowestPriorityIndex_IdentifiersDropLast: an identifier with the lowest
// priority still outlives non-identifier columns.
func TestLowestPriorityIndex_IdentifiersDropLast(t *testing.T) {
	cols := []column{
		{header: "ID", priority: 0, ident: true},
		{header: "Name", priority: 5},
	}
	if got := lowestPriorityIndex(cols); got != 1 {
		t.Errorf("dropped index %d, want 1 (non-identifier)", got)
	}
	cols = []column{{header: "Name", priority: 5}, {header: "ID", priority: 0, ident: true}}
	if got := lowestPriorityIndex(cols); got != 0 {
		t.Errorf("dropped index %d, want 0 (non-identifier)", got)
	}
}

func TestRenderStyledTableAt_IdentifierColumnsNeverEllipsized(t *testing.T) {
	var sb strings.Builder
	if err := renderStyledTableAt(&sb, markedRows(), nil, TableStyle{}, nil, 100); err != nil {
		t.Fatal(err)
	}
	out := stripAnsi(sb.String())
	for _, id := range []string{uuidA, uuidB, uuidC, uuidD} {
		if !strings.Contains(out, id) {
			t.Errorf("styled: id %s clipped:\n%s", id, out)
		}
	}
	assertNoWrap(t, out, 100)
}

func TestRenderStyledTableAt_UnboundedKeepsWholeCells(t *testing.T) {
	var sb strings.Builder
	if err := renderStyledTableAt(&sb, identityRows(), nil, TableStyle{}, nil, unboundedWidth); err != nil {
		t.Fatal(err)
	}
	out := stripAnsi(sb.String())
	if strings.Contains(out, ellipsis) {
		t.Errorf("unbounded styled output ellipsized:\n%s", out)
	}
	if !strings.Contains(out, uuidD) {
		t.Errorf("uuid missing:\n%s", out)
	}
}
