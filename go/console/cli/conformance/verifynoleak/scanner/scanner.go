// Package scanner is the orchestrator that turns a list of files
// into a stream of Findings. It owns:
//
//   - file-type classification (yaml vs markdown vs skip)
//   - reading + size capping
//   - dispatching to the rules engine (yaml direct) or the markdown
//     extractor (fenced-block path)
//   - aggregating per-file findings into a FileResult
//
// The package has no opinion on where the file list comes from — see
// the source/ subpackage for git-aware sources.
package scanner

import (
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"gopkg.in/yaml.v3"

	"hop.top/kit/go/console/cli/conformance/verifynoleak/mdfence"
	"hop.top/kit/go/console/cli/conformance/verifynoleak/rules"
	"hop.top/kit/go/console/cli/conformance/verifynoleak/suppress"
)

// Default per-file size cap (1 MiB) matches design.md §6.
const DefaultMaxFileSize = 1 << 20

// Finding is the scanner-layer wrapper around a rules.Finding: it
// adds the file path and the absolute (file-relative) line number,
// adjusted for fenced-block offsets.
type Finding struct {
	Path        string
	Line        int // 1-based, file-relative
	RuleID      string
	Description string
	MatchedKeys []string
	// BlockStartLine is non-zero when the finding came from a fenced
	// markdown block; useful for the human formatter to show the
	// surrounding fence context.
	BlockStartLine int
}

// FileResult is the per-file outcome of a scan: zero findings means
// clean. Skipped reports whether the file was bypassed entirely
// (binary, too large, unsupported extension); ParseError captures
// best-effort YAML errors (warned, not fatal — see design.md §2).
type FileResult struct {
	Path       string
	Findings   []Finding
	Skipped    bool
	SkipReason string
	ParseError error
}

// Options tunes scanner behavior. Zero values are safe defaults.
type Options struct {
	Rules       *rules.Set
	MaxFileSize int64
	// Allowlist short-circuits the scan for any file whose path
	// matches a configured glob. Nil is the same as an empty list.
	Allowlist *suppress.Allowlist
}

func (o Options) maxSize() int64 {
	if o.MaxFileSize <= 0 {
		return DefaultMaxFileSize
	}
	return o.MaxFileSize
}

// Scan runs ScanFile for each path and returns the aggregated
// results. Errors that aren't per-file (rules set nil, etc.) come
// back from the function itself; per-file errors live on the
// FileResult so the caller can decide policy.
func Scan(paths []string, opts Options) ([]FileResult, error) {
	if opts.Rules == nil {
		return nil, errors.New("scanner: nil rules.Set (call rules.LoadDefault first)")
	}
	out := make([]FileResult, 0, len(paths))
	for _, p := range paths {
		out = append(out, ScanFile(p, opts))
	}
	return out, nil
}

// ScanFile dispatches one path through classification + the
// appropriate analysis pipeline.
func ScanFile(path string, opts Options) FileResult {
	r := FileResult{Path: path}

	if opts.Allowlist != nil && opts.Allowlist.Matches(path) {
		r.Skipped = true
		r.SkipReason = "allowlisted"
		return r
	}

	kind := classify(path)
	if kind == fileSkip {
		r.Skipped = true
		r.SkipReason = "unsupported extension"
		return r
	}

	info, err := os.Stat(path)
	if err != nil {
		r.Skipped = true
		r.SkipReason = fmt.Sprintf("stat: %v", err)
		return r
	}
	if info.Size() > opts.maxSize() {
		r.Skipped = true
		r.SkipReason = fmt.Sprintf("file size %d exceeds limit %d", info.Size(), opts.maxSize())
		return r
	}

	data, err := os.ReadFile(path)
	if err != nil {
		r.Skipped = true
		r.SkipReason = fmt.Sprintf("read: %v", err)
		return r
	}
	if isBinary(data) {
		r.Skipped = true
		r.SkipReason = "binary content"
		return r
	}

	// Parse ignore directives once per file. A bare-ignore (no
	// reason) surfaces as a directive-parse error → ParseError →
	// command layer maps it to ConfigError.
	var (
		directives []suppress.IgnoreDirective
		dirErr     error
	)
	switch kind {
	case fileYAML:
		directives, dirErr = suppress.ParseIgnoreDirectives(data, "yaml")
	case fileMarkdown:
		directives, dirErr = suppress.ParseIgnoreDirectives(data, "md")
	}
	if dirErr != nil {
		// Bare-ignore: don't scan; surface the config error so the
		// command layer can exit 5.
		r.ParseError = dirErr
		return r
	}
	if suppress.HasFileLevelIgnore(directives) {
		r.Skipped = true
		r.SkipReason = "in-file ignore comment"
		return r
	}

	switch kind {
	case fileYAML:
		r.Findings, r.ParseError = scanYAML(path, data, opts.Rules, 0)
	case fileMarkdown:
		r.Findings, r.ParseError = scanMarkdownWithDirectives(path, data, opts.Rules, directives)
	}
	return r
}

// scanYAML parses the bytes as YAML and applies the rule set. A
// parse error is non-fatal: callers see it on FileResult.ParseError
// so they can warn without blocking the commit.
//
// lineOffset is added to each Finding.Line so the same code can serve
// both whole-file YAML scans (offset=0) and Markdown-fenced blocks
// (offset = fence_open_line, so YAML line 1 inside a block whose
// fence opens on file line 42 reports as file line 43).
func scanYAML(path string, data []byte, set *rules.Set, lineOffset int) ([]Finding, error) {
	var doc yaml.Node
	if err := yaml.Unmarshal(data, &doc); err != nil {
		return nil, fmt.Errorf("%s: %w", path, err)
	}
	raw := rules.Apply(set, &doc)
	if len(raw) == 0 {
		return nil, nil
	}
	out := make([]Finding, 0, len(raw))
	for _, f := range raw {
		out = append(out, Finding{
			Path:           path,
			Line:           f.Line + lineOffset,
			RuleID:         f.RuleID,
			Description:    f.Description,
			MatchedKeys:    f.MatchedKeys,
			BlockStartLine: lineOffset, // 0 for whole-file YAML; non-zero for fenced
		})
	}
	return out, nil
}

// scanMarkdown is the directive-unaware path; kept for callers
// (and ReaderScanFile) that haven't parsed directives. Delegates
// to scanMarkdownWithDirectives with no scoped suppressions.
func scanMarkdown(path string, data []byte, set *rules.Set) ([]Finding, error) {
	return scanMarkdownWithDirectives(path, data, set, nil)
}

// scanMarkdownWithDirectives extracts fenced YAML blocks, applies
// the rules engine to each, and drops findings from blocks that a
// preceding ignore-next-block directive covers (design.md §4).
//
// A parse failure in one block does not stop processing of
// subsequent blocks; the error from the *last* failing block is
// preserved on FileResult for caller visibility.
func scanMarkdownWithDirectives(path string, data []byte, set *rules.Set, directives []suppress.IgnoreDirective) ([]Finding, error) {
	blocks := mdfence.Extract(data)
	var (
		out     []Finding
		lastErr error
	)
	for _, b := range blocks {
		// Check whether a preceding ignore-next-block directive
		// scopes to this fence. The directive must be within 3 lines
		// of the fence opener (≤ 2 blank lines + the comment line).
		if _, ok := suppress.FindIgnoreNextBlockFor(directives, b.StartLine); ok {
			continue
		}
		findings, err := scanYAML(path, b.Content, set, b.StartLine)
		if err != nil {
			lastErr = err
			continue
		}
		for i := range findings {
			findings[i].BlockStartLine = b.StartLine
		}
		out = append(out, findings...)
	}
	return out, lastErr
}

// fileKind classifies a path. Switch is the v1 surface; v1.1 may
// add source-literal scanning per design.md §6.
type fileKind int

const (
	fileSkip fileKind = iota
	fileYAML
	fileMarkdown
)

func classify(path string) fileKind {
	ext := strings.ToLower(filepath.Ext(path))
	switch ext {
	case ".yaml", ".yml":
		return fileYAML
	case ".md", ".markdown":
		return fileMarkdown
	}
	return fileSkip
}

// isBinary samples the first 512 bytes for a NUL — same heuristic
// `git diff` uses. Avoids dragging in `file --mime-type`.
func isBinary(data []byte) bool {
	if len(data) == 0 {
		return false
	}
	sample := data
	if len(sample) > 512 {
		sample = sample[:512]
	}
	for _, b := range sample {
		if b == 0 {
			return true
		}
	}
	return false
}

// CountFindings is a convenience for the verify-no-leak command's
// exit-code decision: any findings → leak detected. Skipped files
// and parse warnings do not count.
func CountFindings(results []FileResult) int {
	n := 0
	for _, r := range results {
		n += len(r.Findings)
	}
	return n
}

// ReaderScanFile is a helper for tests + callers that already have
// the bytes in hand (e.g. piping from `git show`). Bypasses the
// filesystem entirely.
func ReaderScanFile(path string, kind string, r io.Reader, opts Options) FileResult {
	data, err := io.ReadAll(r)
	if err != nil {
		return FileResult{Path: path, Skipped: true, SkipReason: fmt.Sprintf("read: %v", err)}
	}
	res := FileResult{Path: path}
	if int64(len(data)) > opts.maxSize() {
		res.Skipped = true
		res.SkipReason = fmt.Sprintf("size %d exceeds limit %d", len(data), opts.maxSize())
		return res
	}
	if isBinary(data) {
		res.Skipped = true
		res.SkipReason = "binary content"
		return res
	}
	switch strings.ToLower(kind) {
	case "yaml", "yml":
		res.Findings, res.ParseError = scanYAML(path, data, opts.Rules, 0)
	case "md", "markdown":
		res.Findings, res.ParseError = scanMarkdown(path, data, opts.Rules)
	default:
		res.Skipped = true
		res.SkipReason = "unsupported kind " + kind
	}
	return res
}
