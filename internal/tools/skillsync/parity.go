package main

import (
	"fmt"
	"os"
	"regexp"
	"slices"
	"strconv"
	"strings"
)

// Status words of the parity page. The page's vocabulary table must define
// exactly these; the generator's grouping rules are written against them.
const (
	stDone       = "done"
	stPartial    = "partial"
	stNone       = "none"
	stNA         = "n/a"
	stUnverified = "unverified"
)

// statusOrder is the order statuses are listed in generated text.
var statusOrder = []string{stDone, stPartial, stUnverified, stNone, stNA}

const capabilityColumns = 7 // Capability, five SDK columns, Evidence

// Capability is one row of a parity table.
type Capability struct {
	Name     string
	Status   []string // one per SDK column, in page order
	Evidence string
	Line     int
}

// goOnly reports whether every status except the reference column's is none
// or n/a.
func (c Capability) goOnly(ref int) bool {
	for i, s := range c.Status {
		if i != ref && s != stNone && s != stNA {
			return false
		}
	}
	return true
}

// Section is one `##` heading with a capability table.
type Section struct {
	Title string
	Caps  []Capability
}

// Parity is the parsed capability parity page.
type Parity struct {
	Path     string
	Columns  []string          // SDK column names, page order
	Vocab    map[string]string // status word -> meaning
	Sections []Section
	Summary  map[string]summaryRow // SDK label -> row
}

type summaryRow struct {
	Line   int
	Counts map[string]int
}

func loadParity(path string) (*Parity, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	return parseParity(path, string(b))
}

func parseParity(path, text string) (*Parity, error) {
	p := &Parity{Path: path, Vocab: map[string]string{}, Summary: map[string]summaryRow{}}
	fail := func(line int, format string, a ...any) error {
		return fmt.Errorf("%s:%d: %s", path, line, fmt.Sprintf(format, a...))
	}
	seenVocab, seenSummary := false, false
	names := map[string]int{}
	for _, t := range scanTables(text) {
		switch {
		case len(t.Header) == 2 && t.Header[0] == "Status":
			seenVocab = true
			for _, r := range t.Rows {
				if len(r.Cells) != 2 {
					return nil, fail(r.Line, "vocabulary row has %d columns, want 2", len(r.Cells))
				}
				p.Vocab[r.Cells[0]] = r.Cells[1]
			}
		case len(t.Header) == 2 && t.Header[0] == "SDK":
			seenSummary = true
			for _, r := range t.Rows {
				if len(r.Cells) != 2 {
					return nil, fail(r.Line, "Summary row has %d columns, want 2", len(r.Cells))
				}
				counts, err := parseCounts(r.Cells[1])
				if err != nil {
					return nil, fail(r.Line, "Summary row %q: %v", r.Cells[0], err)
				}
				p.Summary[r.Cells[0]] = summaryRow{Line: r.Line, Counts: counts}
			}
		case len(t.Header) > 0 && t.Header[0] == "Capability":
			if len(t.Header) != capabilityColumns || t.Header[capabilityColumns-1] != "Evidence" {
				return nil, fail(t.HeaderLine, "capability table header must have %d columns ending in Evidence, got %d: %v", capabilityColumns, len(t.Header), t.Header)
			}
			cols := t.Header[1 : capabilityColumns-1]
			if p.Columns == nil {
				p.Columns = slices.Clone(cols)
			} else if !slices.Equal(p.Columns, cols) {
				return nil, fail(t.HeaderLine, "capability table header %v differs from the first table's %v", cols, p.Columns)
			}
			sec := Section{Title: t.Heading}
			for _, r := range t.Rows {
				if len(r.Cells) != capabilityColumns {
					return nil, fail(r.Line, "capability row %q: expected %d columns, got %d", r.Cells[0], capabilityColumns, len(r.Cells))
				}
				if r.Cells[0] == "" {
					return nil, fail(r.Line, "capability row has an empty name")
				}
				if first, dup := names[r.Cells[0]]; dup {
					return nil, fail(r.Line, "duplicate capability %q (first at line %d)", r.Cells[0], first)
				}
				names[r.Cells[0]] = r.Line
				c := Capability{Name: r.Cells[0], Evidence: r.Cells[capabilityColumns-1], Line: r.Line}
				for i, s := range r.Cells[1 : capabilityColumns-1] {
					if !slices.Contains(statusOrder, s) {
						return nil, fail(r.Line, "unknown status %q for %q in the %s column", s, c.Name, cols[i])
					}
					c.Status = append(c.Status, s)
				}
				sec.Caps = append(sec.Caps, c)
			}
			p.Sections = append(p.Sections, sec)
		}
	}
	if !seenVocab {
		return nil, fmt.Errorf("%s: no status vocabulary table (header: Status | Meaning)", path)
	}
	if !seenSummary {
		return nil, fmt.Errorf("%s: no Summary table (header: SDK | Counts)", path)
	}
	if len(p.Sections) == 0 {
		return nil, fmt.Errorf("%s: no capability tables (header starting with Capability)", path)
	}
	for _, w := range statusOrder {
		if _, ok := p.Vocab[w]; !ok {
			return nil, fmt.Errorf("%s: status vocabulary lacks %q", path, w)
		}
	}
	for w := range p.Vocab {
		if !slices.Contains(statusOrder, w) {
			return nil, fmt.Errorf("%s: status vocabulary has %q, which the generator does not know", path, w)
		}
	}
	return p, nil
}

var countRe = regexp.MustCompile(`^(\d+) (\S+)$`)

func parseCounts(s string) (map[string]int, error) {
	out := map[string]int{}
	for _, part := range strings.Split(s, ",") {
		m := countRe.FindStringSubmatch(strings.TrimSpace(part))
		if m == nil {
			return nil, fmt.Errorf("cannot read count %q (want \"<n> <status>\")", strings.TrimSpace(part))
		}
		if !slices.Contains(statusOrder, m[2]) {
			return nil, fmt.Errorf("unknown status %q", m[2])
		}
		if _, dup := out[m[2]]; dup {
			return nil, fmt.Errorf("status %q listed twice", m[2])
		}
		n, _ := strconv.Atoi(m[1])
		out[m[2]] = n
	}
	return out, nil
}

// Counts returns the status counts of column col computed from the rows.
func (p *Parity) Counts(col int) map[string]int {
	out := map[string]int{}
	for _, s := range p.Sections {
		for _, c := range s.Caps {
			out[c.Status[col]]++
		}
	}
	return out
}

// formatCounts renders counts as "11 done, 8 partial, ..." in statusOrder,
// skipping zeros.
func formatCounts(m map[string]int) string {
	var parts []string
	for _, s := range statusOrder {
		if m[s] > 0 {
			parts = append(parts, fmt.Sprintf("%d %s", m[s], s))
		}
	}
	return strings.Join(parts, ", ")
}

// checkSummary compares the Summary table with counts computed from the
// rows. labels maps each SDK column index to its Summary label. Every
// disagreement is reported, with the Summary row's line number.
func (p *Parity) checkSummary(labels []string) error {
	var problems []string
	seen := map[string]bool{}
	for col, label := range labels {
		row, ok := p.Summary[label]
		if !ok {
			problems = append(problems, fmt.Sprintf("Summary has no row for %s", label))
			continue
		}
		seen[label] = true
		rows := p.Counts(col)
		if !countsEqual(row.Counts, rows) {
			problems = append(problems, fmt.Sprintf("%s:%d: Summary says %s: %q but the rows give %q",
				p.Path, row.Line, label, formatCounts(row.Counts), formatCounts(rows)))
		}
	}
	for label, row := range p.Summary {
		if !seen[label] {
			problems = append(problems, fmt.Sprintf("%s:%d: Summary row %q matches no port", p.Path, row.Line, label))
		}
	}
	if len(problems) == 0 {
		return nil
	}
	slices.Sort(problems)
	return fmt.Errorf("%s Summary table disagrees with its rows (fix the Summary):\n  %s", p.Path, strings.Join(problems, "\n  "))
}

func countsEqual(a, b map[string]int) bool {
	for _, s := range statusOrder {
		if a[s] != b[s] {
			return false
		}
	}
	return true
}
