// Command order is the Go runner for the cross-language column-ordering
// conformance harness.
//
// Go is the REFERENCE runtime and participates only where a `table:""`
// struct can express the case. It has no ColumnSpec: the struct IS the spec,
// and field declaration order is what the payload SDKs encode as ColumnSpec
// list order. Concretely:
//
//   - "spec order" is expressed by declaring fields in that order.
//   - "payload key order differs from spec order" is NOT expressible: a Go
//     struct has exactly one field order, so the two notions coincide. The
//     case still runs, and passing means Go agrees with the ordering the
//     payload SDKs derive from their ColumnSpec.
//   - header != key is NOT expressible at all — a `table:""` tag supplies the
//     header while the lookup comes from the struct field. That inexpressibility
//     is the REASON contract rule 3 exists, so Go satisfies it by construction
//     and reports "n/a" rather than a pass.
//
// The runner renders each case, then RE-PARSES its own rendered bytes to
// observe the column sequence actually serialized. Nothing here sorts keys.
package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"

	"hop.top/kit/go/console/output"
)

// Row shapes. Field order is the Go analog of ColumnSpec list order.

// zetaAlphaMid declares fields anti-alphabetically so a runtime that sorts
// its columns is caught.
type zetaAlphaMid struct {
	Zeta  string `json:"zeta"  yaml:"zeta"  table:"zeta"`
	Alpha string `json:"alpha" yaml:"alpha" table:"alpha"`
	Mid   string `json:"mid"   yaml:"mid"   table:"mid"`
}

type fixtureDoc struct {
	PortableFormats []string `json:"portable_formats"`
	ExtendedFormats []string `json:"extended_formats"`
	Cases           []struct {
		Name    string              `json:"name"`
		Spec    []string            `json:"spec"`
		Rows    []map[string]string `json:"rows"`
		Cols    []string            `json:"cols"`
		Formats string              `json:"formats"`
		Go      bool                `json:"go"`
	} `json:"cases"`
}

type record struct {
	Case     string   `json:"case"`
	Format   string   `json:"format"`
	Status   string   `json:"status"`
	Sequence []string `json:"sequence,omitempty"`
	Empty    *bool    `json:"empty,omitempty"`
	Rejected *bool    `json:"rejected,omitempty"`
}

func seqFromTable(text string) ([]string, bool) {
	lines := nonEmptyLines(text)
	if len(lines) == 0 {
		return []string{}, true
	}
	return strings.Fields(lines[0]), false
}

func seqFromCSV(text string) ([]string, bool) {
	lines := nonEmptyLines(text)
	if len(lines) == 0 {
		return []string{}, true
	}
	parts := strings.Split(lines[0], ",")
	out := make([]string, 0, len(parts))
	for _, p := range parts {
		out = append(out, strings.TrimSpace(p))
	}
	return out, false
}

func seqFromText(text string) ([]string, bool) {
	keys := []string{}
	for _, ln := range strings.Split(text, "\n") {
		if strings.TrimSpace(ln) == "" {
			break
		}
		i := strings.Index(ln, "=")
		if i < 0 {
			continue
		}
		keys = append(keys, strings.TrimSpace(ln[:i]))
	}
	return keys, len(keys) == 0
}

var jsonKeyRe = regexp.MustCompile(`"([A-Za-z0-9_.\-]+)"\s*:`)

// seqFromJSON reads the first record's key order out of the emitted bytes.
//
// Unmarshalling is deliberately avoided: encoding/json decodes objects into
// map[string]any, which would destroy the very ordering we are here to
// observe. Scanning the raw bytes of the first {...} block reports the keys
// in the order they were actually written to the wire.
func seqFromJSON(text string) ([]string, bool) {
	if strings.TrimSpace(text) == "" {
		return []string{}, true
	}
	start := strings.Index(text, "{")
	if start < 0 {
		return []string{}, true
	}
	depth := 0
	end := -1
	for i := start; i < len(text); i++ {
		switch text[i] {
		case '{':
			depth++
		case '}':
			depth--
			if depth == 0 {
				end = i
			}
		}
		if end != -1 {
			break
		}
	}
	if end < 0 {
		return []string{}, true
	}
	block := text[start : end+1]
	ms := jsonKeyRe.FindAllStringSubmatch(block, -1)
	keys := make([]string, 0, len(ms))
	for _, m := range ms {
		keys = append(keys, m[1])
	}
	return keys, len(keys) == 0
}

var yamlKeyRe = regexp.MustCompile(`^([A-Za-z0-9_.\-]+):`)

func seqFromYAML(text string) ([]string, bool) {
	lines := nonEmptyLines(text)
	if len(lines) == 0 {
		return []string{}, true
	}
	if len(lines) == 1 && (strings.TrimSpace(lines[0]) == "[]" || strings.TrimSpace(lines[0]) == "null") {
		return []string{}, true
	}
	keys := []string{}
	baseIndent := -1
	for _, raw := range lines {
		ln := raw
		indent := len(ln) - len(strings.TrimLeft(ln, " "))
		ln = strings.TrimLeft(ln, " ")
		if strings.HasPrefix(ln, "- ") {
			if len(keys) > 0 {
				break
			}
			indent += 2
			ln = ln[2:]
		} else if ln == "-" {
			if len(keys) > 0 {
				break
			}
			continue
		}
		m := yamlKeyRe.FindStringSubmatch(ln)
		if m == nil {
			continue
		}
		if baseIndent == -1 {
			baseIndent = indent
		}
		if indent != baseIndent {
			continue
		}
		keys = append(keys, m[1])
	}
	return keys, len(keys) == 0
}

func nonEmptyLines(text string) []string {
	out := []string{}
	for _, ln := range strings.Split(text, "\n") {
		if strings.TrimSpace(ln) != "" {
			out = append(out, ln)
		}
	}
	return out
}

// buildRows maps the fixture's generic rows onto the Go struct shape.
func buildRows(rows []map[string]string) []zetaAlphaMid {
	out := make([]zetaAlphaMid, 0, len(rows))
	for _, r := range rows {
		out = append(out, zetaAlphaMid{Zeta: r["zeta"], Alpha: r["alpha"], Mid: r["mid"]})
	}
	return out
}

func main() {
	// The orchestrator exports the fixture path; fall back to the cross-lang
	// dir relative to cwd so the runner stays usable by hand.
	fixturePath := os.Getenv("KIT_CROSS_LANG_ORDER_FIXTURE")
	if fixturePath == "" {
		here, err := os.Getwd()
		if err != nil {
			panic(err)
		}
		fixturePath = filepath.Join(here, "fixtures", "ordering.json")
	}
	raw, err := os.ReadFile(fixturePath)
	if err != nil {
		panic(err)
	}
	var doc fixtureDoc
	if err := json.Unmarshal(raw, &doc); err != nil {
		panic(err)
	}

	outPath := os.Getenv("KIT_CROSS_LANG_ORDER_OUT")
	if outPath == "" {
		fmt.Fprintln(os.Stderr, "KIT_CROSS_LANG_ORDER_OUT unset")
		os.Exit(1)
	}

	records := []record{}
	for _, c := range doc.Cases {
		if !c.Go {
			continue
		}
		formats := doc.PortableFormats
		if c.Formats != "portable" {
			formats = doc.ExtendedFormats
		}
		rows := buildRows(c.Rows)

		for _, fmtName := range formats {
			f, ok := output.Default.Lookup(fmtName)
			if !ok {
				records = append(records, record{Case: c.Name, Format: fmtName, Status: "unsupported"})
				continue
			}
			opts, err := output.ParseOptions(nil, f.Options())
			if err != nil {
				panic(err)
			}
			var buf bytes.Buffer
			// Go has no ColumnSpec: with no --cols it renders every tagged
			// field in declaration order, which IS the default-order rule.
			if err := f.Render(&buf, rows, opts, c.Cols); err != nil {
				records = append(records, record{Case: c.Name, Format: fmtName, Status: "error"})
				continue
			}
			var seq []string
			var empty bool
			switch fmtName {
			case "table":
				seq, empty = seqFromTable(buf.String())
			case "json":
				seq, empty = seqFromJSON(buf.String())
			case "yaml":
				seq, empty = seqFromYAML(buf.String())
			case "csv":
				seq, empty = seqFromCSV(buf.String())
			case "text":
				seq, empty = seqFromText(buf.String())
			default:
				panic("no extractor for " + fmtName)
			}
			e := empty
			records = append(records, record{
				Case: c.Name, Format: fmtName, Status: "ok",
				Sequence: seq, Empty: &e,
			})
		}
	}

	// Contract rule 3 is NOT APPLICABLE to Go: a `table:""` tag cannot express
	// a header/key split, so there is nothing to reject. Reporting n/a rather
	// than a pass keeps the distinction visible.
	records = append(records, record{
		Case: "header-key-enforced", Format: "-", Status: "n/a",
	})

	var body strings.Builder
	for _, r := range records {
		// Marshal through a map so the wrapper keys sort deterministically,
		// matching the other runners. Sorting the WRAPPER never touches the
		// observed sequence, which stays an ordered array.
		m := map[string]any{"case": r.Case, "format": r.Format, "status": r.Status}
		if r.Status == "ok" {
			m["sequence"] = r.Sequence
			m["empty"] = *r.Empty
		}
		keys := make([]string, 0, len(m))
		for k := range m {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		parts := make([]string, 0, len(keys))
		for _, k := range keys {
			vb, err := json.Marshal(m[k])
			if err != nil {
				panic(err)
			}
			parts = append(parts, fmt.Sprintf("%q:%s", k, vb))
		}
		body.WriteString("{" + strings.Join(parts, ",") + "}\n")
	}
	if err := os.WriteFile(outPath, []byte(body.String()), 0o644); err != nil {
		panic(err)
	}
}
