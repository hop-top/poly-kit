// Package mdfence extracts YAML-fenced code blocks from Markdown
// source per design.md §2. Pure function; no I/O, no YAML parsing.
//
// Detection rules:
//
//   - Fence opener matches ```yaml / ```yml / ```YAML / ```YML
//     case-insensitive, leading whitespace and trailing space-or-EOL
//     tolerated.
//   - Fence closer matches a line containing ``` with the same or
//     greater backtick count as the opener (CommonMark §4.5).
//   - One level of nesting tracked: blocks nested inside other fenced
//     code are skipped (the outer fence already covers them).
//   - Multiple blocks per file are independent documents.
//
// What this package does NOT do:
//
//   - Parse YAML. Callers pass Block.Content to yaml.Unmarshal and
//     handle parse failures themselves.
//   - Detect "yaml-shaped" prose outside fences. Survey §1: too high
//     a false-positive base rate; explicit non-goal.
//   - Handle indented code blocks. CommonMark allows ` indented by 4
//     spaces ` to be a code block, but no language tag attaches to
//     them. Authors leak scenarios in fenced blocks, not indented.
package mdfence

import (
	"bufio"
	"bytes"
	"strings"
)

// Block is one extracted YAML-fenced code block. Lines are 1-based
// and refer to the original Markdown source — Content's offset 0
// corresponds to StartLine + 1 in the source (the line after the
// opening fence). StartLine is the line of the opening fence;
// EndLine is the line of the closing fence (or the last line of
// source if the fence is never closed).
type Block struct {
	Content   []byte
	StartLine int
	EndLine   int
}

// Extract walks md and returns every YAML-fenced block at top
// level (not inside an outer fence). Returned slice is empty (not
// nil) when no blocks are found, so callers can range freely.
func Extract(md []byte) []Block {
	out := []Block{}
	sc := bufio.NewScanner(bytes.NewReader(md))
	sc.Buffer(make([]byte, 64*1024), 1024*1024) // tolerate long lines (1 MB cap, matches design's --max-file-size default)

	var (
		lineNo          int
		inOuterFence    bool // we're inside a non-yaml fence; skip everything until it closes
		outerFenceTicks int
		inYAMLBlock     bool
		yamlStartLine   int
		yamlFenceTicks  int
		yamlBuf         bytes.Buffer
	)

	for sc.Scan() {
		lineNo++
		line := sc.Text()
		trimmed := strings.TrimLeft(line, " \t")
		ticks := countLeadingBackticks(trimmed)

		switch {
		case inYAMLBlock:
			// Inside our block — look for a closer.
			if ticks >= yamlFenceTicks && allBackticks(trimmed, ticks) {
				out = append(out, Block{
					Content:   bytes.Clone(yamlBuf.Bytes()),
					StartLine: yamlStartLine,
					EndLine:   lineNo,
				})
				inYAMLBlock = false
				yamlBuf.Reset()
				continue
			}
			yamlBuf.WriteString(line)
			yamlBuf.WriteByte('\n')

		case inOuterFence:
			// Inside a non-YAML fence — wait for the matching close.
			if ticks >= outerFenceTicks && allBackticks(trimmed, ticks) {
				inOuterFence = false
			}

		default:
			// Not inside any fence — look for an opener.
			if ticks < 3 {
				continue
			}
			// CommonMark allows an info string after the fence. The
			// first whitespace-delimited token is the language tag;
			// remaining tokens are metadata authors sometimes attach
			// (e.g. ```yaml title="x").
			lang := firstWord(trimmed[ticks:])
			if lang == "yaml" || lang == "yml" {
				inYAMLBlock = true
				yamlStartLine = lineNo
				yamlFenceTicks = ticks
				continue
			}
			// Any other fence — track it so we don't pick up a YAML
			// opener "inside" it.
			inOuterFence = true
			outerFenceTicks = ticks
		}
	}

	// Unclosed block at EOF: still emit the partial content with the
	// last-line offset, since the bytes captured are scannable.
	if inYAMLBlock {
		out = append(out, Block{
			Content:   bytes.Clone(yamlBuf.Bytes()),
			StartLine: yamlStartLine,
			EndLine:   lineNo,
		})
	}
	return out
}

// countLeadingBackticks returns the number of leading backtick
// characters in s.
func countLeadingBackticks(s string) int {
	n := 0
	for n < len(s) && s[n] == '`' {
		n++
	}
	return n
}

// allBackticks reports whether s starts with exactly n backticks
// followed by only whitespace through end of line. Used to detect
// fence closers — CommonMark requires the close line to contain no
// content beyond optional trailing whitespace.
func allBackticks(s string, n int) bool {
	if len(s) < n {
		return false
	}
	for i := 0; i < n; i++ {
		if s[i] != '`' {
			return false
		}
	}
	tail := strings.TrimRight(s[n:], " \t")
	// CommonMark also forbids more backticks on the close line beyond
	// the fence count when the closer is to terminate — but in
	// practice authors don't do this, and accepting it loose is safer
	// than rejecting a valid close. So: tail must contain no '`'.
	return !strings.ContainsRune(tail, '`')
}

// firstWord returns the first whitespace-delimited token of s,
// lowercased. Empty string if s is blank.
func firstWord(s string) string {
	s = strings.TrimSpace(s)
	if s == "" {
		return ""
	}
	for i, r := range s {
		if r == ' ' || r == '\t' {
			return strings.ToLower(s[:i])
		}
	}
	return strings.ToLower(s)
}
