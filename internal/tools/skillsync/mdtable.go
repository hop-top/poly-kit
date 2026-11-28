package main

import "strings"

// mdRow is one table row with its 1-based line number in the source.
type mdRow struct {
	Line  int
	Cells []string
}

// mdTable is a pipe table: header cells, rows, and the nearest `##` heading
// above it.
type mdTable struct {
	Heading    string
	HeaderLine int
	Header     []string
	Rows       []mdRow
}

// scanTables returns every pipe table in text. A table is a line starting
// with `|` followed by a separator line (`|---|...`); its rows run until the
// first line that does not start with `|`.
func scanTables(text string) []mdTable {
	lines := strings.Split(text, "\n")
	var tables []mdTable
	heading := ""
	for i := 0; i < len(lines); i++ {
		l := lines[i]
		if strings.HasPrefix(l, "## ") {
			heading = strings.TrimSpace(strings.TrimPrefix(l, "## "))
			continue
		}
		if !strings.HasPrefix(l, "|") || i+1 >= len(lines) || !isSeparator(lines[i+1]) {
			continue
		}
		t := mdTable{Heading: heading, HeaderLine: i + 1, Header: splitRow(l)}
		j := i + 2
		for ; j < len(lines) && strings.HasPrefix(lines[j], "|"); j++ {
			t.Rows = append(t.Rows, mdRow{Line: j + 1, Cells: splitRow(lines[j])})
		}
		tables = append(tables, t)
		i = j - 1
	}
	return tables
}

func isSeparator(l string) bool {
	if !strings.HasPrefix(l, "|") {
		return false
	}
	for _, c := range splitRow(l) {
		c = strings.Trim(c, ": ")
		if c == "" || strings.Trim(c, "-") != "" {
			return false
		}
	}
	return true
}

// splitRow splits a pipe row into trimmed cells; `\|` does not split.
func splitRow(l string) []string {
	l = strings.TrimSpace(l)
	l = strings.TrimPrefix(l, "|")
	l = strings.TrimSuffix(l, "|")
	var cells []string
	var b strings.Builder
	for i := 0; i < len(l); i++ {
		switch {
		case l[i] == '\\' && i+1 < len(l) && l[i+1] == '|':
			b.WriteByte('|')
			i++
		case l[i] == '|':
			cells = append(cells, strings.TrimSpace(b.String()))
			b.Reset()
		default:
			b.WriteByte(l[i])
		}
	}
	return append(cells, strings.TrimSpace(b.String()))
}

// escapeCell makes s safe inside a pipe-table cell.
func escapeCell(s string) string { return strings.ReplaceAll(s, "|", `\|`) }
