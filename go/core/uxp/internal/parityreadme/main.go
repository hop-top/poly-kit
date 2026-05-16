// Command parityreadme generates the parity table block in
// go/core/uxp/README.md from the static Mappings() and
// ToolCapabilities() declared by every registered adapter.
//
// Usage:
//
//	go generate ./go/core/uxp/...
//
// The generator imports every adapter and emits two tables:
//   - Universal-option × CLI matrix (replaces <!-- parity:start --> …
//     <!-- parity:end --> in README.md).
//   - Tool-capability × CLI matrix (replaces <!-- tools:start --> …
//     <!-- tools:end -->).
//
// CI runs `go generate ./...` and fails if either block diffs against
// the committed README. That makes the parity README a structural
// invariant of the package: edit an adapter's Mappings(), regenerate,
// commit the README diff. There is no other source of truth.
package main

import (
	"flag"
	"fmt"
	"os"
	"sort"
	"strings"

	"hop.top/kit/go/core/uxp/invoke"
	"hop.top/kit/go/core/uxp/invoke/adapters/claude"
	"hop.top/kit/go/core/uxp/invoke/adapters/codex"
	"hop.top/kit/go/core/uxp/invoke/adapters/copilot"
	"hop.top/kit/go/core/uxp/invoke/adapters/crush"
	"hop.top/kit/go/core/uxp/invoke/adapters/cursoragent"
	"hop.top/kit/go/core/uxp/invoke/adapters/gemini"
	"hop.top/kit/go/core/uxp/invoke/adapters/goose"
	"hop.top/kit/go/core/uxp/invoke/adapters/kimi"
	"hop.top/kit/go/core/uxp/invoke/adapters/opencode"
	"hop.top/kit/go/core/uxp/invoke/adapters/qwen"
	"hop.top/kit/go/core/uxp/invoke/adapters/vibe"
)

// Adapter ordering in the table follows the canonical column order
// defined in spec §15.4: reference adapters first (claude, gemini,
// codex, opencode), then expansion adapters in the order they were
// added (copilot, cursor-agent, qwen, kimi, vibe, goose, crush).
var orderedAdapters = []invoke.InvocationAdapter{
	claude.New(),
	gemini.New(),
	codex.New(),
	opencode.New(),
	copilot.New(),
	cursoragent.New(),
	qwen.New(),
	kimi.New(),
	vibe.New(),
	goose.New(),
	crush.New(),
}

// universalOptions is the row order. Mirrors spec §15.4 grouping.
var universalOptions = []string{
	"ModeRun", "ModeInteractive", "ModeResume", "Continue", "Fork",
	"CWD", "Model", "Agent",
	"OutputText", "OutputJSON", "OutputStreamJSON",
	"SandboxReadOnly", "SandboxWorkspaceWrite", "SandboxDangerFullAccess",
	"ApprovalAsk", "ApprovalPlan", "ApprovalAutoEdit", "ApprovalAutoAll", "ApprovalNever",
	"AddDirs", "Files", "Images",
}

var universalToolCapabilities = []string{
	"shell.exec", "file.read", "file.write", "file.edit", "file.search",
	"web.search", "web.fetch", "todo.write", "task.spawn", "plan.update",
	"mcp.call", "image.read", "browser.operate", "user.message",
}

func main() {
	var (
		updateFlag = flag.Bool("update", false, "rewrite the README.md block in-place")
		readmePath = flag.String("readme", defaultReadmePath(), "path to the README to update")
	)
	flag.Parse()

	parityTable := renderParityTable()
	toolsTable := renderToolsTable()

	if !*updateFlag {
		fmt.Println("<!-- parity:start -->")
		fmt.Println(parityTable)
		fmt.Println("<!-- parity:end -->")
		fmt.Println()
		fmt.Println("<!-- tools:start -->")
		fmt.Println(toolsTable)
		fmt.Println("<!-- tools:end -->")
		return
	}

	if err := updateBlock(*readmePath, "parity", parityTable); err != nil {
		fail(err)
	}
	if err := updateBlock(*readmePath, "tools", toolsTable); err != nil {
		fail(err)
	}
}

func defaultReadmePath() string {
	// `go generate` invokes the directive with cwd = directory of the
	// source file containing it (go/core/uxp/generate.go), so the
	// README is right there.
	return "README.md"
}

func renderParityTable() string {
	var b strings.Builder

	// Header.
	fmt.Fprintf(&b, "| Universal |")
	for _, a := range orderedAdapters {
		fmt.Fprintf(&b, " %s |", a.CLI())
	}
	b.WriteString("\n|---|")
	for range orderedAdapters {
		b.WriteString("---|")
	}
	b.WriteString("\n")

	// Rows.
	mappingsByCLI := map[string]map[string]invoke.OptionMapping{}
	for _, a := range orderedAdapters {
		mappingsByCLI[a.CLI()] = map[string]invoke.OptionMapping{}
		for _, m := range a.Mappings() {
			mappingsByCLI[a.CLI()][m.Universal] = m
		}
	}

	for _, opt := range universalOptions {
		fmt.Fprintf(&b, "| `%s` |", opt)
		for _, a := range orderedAdapters {
			cell := mappingsByCLI[a.CLI()][opt]
			fmt.Fprintf(&b, " %s |", supportSymbol(cell.Support))
		}
		b.WriteString("\n")
	}

	b.WriteString("\nLegend: `N` native · `S` shim · `U` unsupported · `D` dangerous (opt-in required).\n")
	return strings.TrimRight(b.String(), "\n")
}

func renderToolsTable() string {
	var b strings.Builder

	fmt.Fprintf(&b, "| Tool |")
	for _, a := range orderedAdapters {
		fmt.Fprintf(&b, " %s |", a.CLI())
	}
	b.WriteString("\n|---|")
	for range orderedAdapters {
		b.WriteString("---|")
	}
	b.WriteString("\n")

	capsByCLI := map[string]map[string]invoke.ToolCapability{}
	for _, a := range orderedAdapters {
		capsByCLI[a.CLI()] = map[string]invoke.ToolCapability{}
		for _, c := range a.ToolCapabilities() {
			capsByCLI[a.CLI()][c.Universal] = c
		}
	}

	for _, tool := range universalToolCapabilities {
		fmt.Fprintf(&b, "| `%s` |", tool)
		for _, a := range orderedAdapters {
			cell := capsByCLI[a.CLI()][tool]
			fmt.Fprintf(&b, " %s |", supportSymbol(cell.Support))
		}
		b.WriteString("\n")
	}

	b.WriteString("\nLegend: `N` native · `S` shim · `U` unsupported.\n")
	return strings.TrimRight(b.String(), "\n")
}

func supportSymbol(s invoke.MappingSupport) string {
	switch s {
	case invoke.MappingNative:
		return "N"
	case invoke.MappingShim:
		return "S"
	case invoke.MappingDangerous:
		return "D"
	case invoke.MappingUnsupported:
		return "U"
	}
	return "?"
}

func updateBlock(path, name, content string) error {
	data, err := os.ReadFile(path)
	if err != nil {
		return fmt.Errorf("read %s: %w", path, err)
	}
	startMarker := fmt.Sprintf("<!-- %s:start -->", name)
	endMarker := fmt.Sprintf("<!-- %s:end -->", name)
	startIdx := strings.Index(string(data), startMarker)
	endIdx := strings.Index(string(data), endMarker)
	if startIdx < 0 || endIdx < 0 || endIdx < startIdx {
		return fmt.Errorf("markers %q / %q not found in %s", startMarker, endMarker, path)
	}
	before := string(data[:startIdx+len(startMarker)])
	after := string(data[endIdx:])
	out := before + "\n" + content + "\n" + after
	return os.WriteFile(path, []byte(out), 0o644)
}

func fail(err error) {
	fmt.Fprintln(os.Stderr, "parityreadme:", err)
	os.Exit(1)
}

// Ensure imports stay tidy when ToolCapabilities lookup misses (we'd
// see a zero-value cell). Keeping sort imported ensures any future
// row-stability work has the helper available.
var _ = sort.Strings
