package uxp_test

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// TestREADMEParityIsUpToDate runs the parityreadme generator in
// stdout-only mode and asserts that the parity and tools blocks in
// the committed README match the current adapter Mappings() /
// ToolCapabilities() output.
//
// If this test fails, run:
//
//	go generate ./go/core/uxp/...
//
// and commit the README diff.
func TestREADMEParityIsUpToDate(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("requires unix shell semantics")
	}

	pkgDir := pkgRoot(t)

	// Run the generator without --update; it prints both blocks to
	// stdout for diffing.
	cmd := exec.Command("go", "run", "./internal/parityreadme")
	cmd.Dir = pkgDir
	var out bytes.Buffer
	cmd.Stdout = &out
	cmd.Stderr = &out
	if err := cmd.Run(); err != nil {
		t.Fatalf("go run parityreadme: %v\n%s", err, out.String())
	}

	got := normalizeBlocks(out.String())

	// The parity blocks live in the adopter reference page, not the
	// package README: they exceed what a Shape B README may carry.
	// Keep this path in sync with the -readme flag in generate.go.
	docPath := filepath.Join(pkgDir, "..", "..", "..",
		"docs", "adopters", "reference", "uxp.md")
	readmeBytes, err := os.ReadFile(docPath)
	if err != nil {
		t.Fatalf("read %s: %v", docPath, err)
	}
	want := normalizeBlocks(extractBlocks(string(readmeBytes)))

	if got != want {
		t.Errorf("uxp.md parity blocks are stale.\nRun: go generate ./go/core/uxp/...\n\n--- generated ---\n%s\n\n--- doc ---\n%s",
			got, want)
	}
}

// pkgRoot returns the directory of this test file's package
// (go/core/uxp/). Resolved via runtime.Caller for portability.
func pkgRoot(t *testing.T) string {
	t.Helper()
	_, thisFile, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("runtime.Caller failed")
	}
	return filepath.Dir(thisFile)
}

func extractBlocks(src string) string {
	parity := extractBlock(src, "parity")
	tools := extractBlock(src, "tools")
	return parity + "\n\n" + tools
}

func extractBlock(src, name string) string {
	startMarker := "<!-- " + name + ":start -->"
	endMarker := "<!-- " + name + ":end -->"
	startIdx := strings.Index(src, startMarker)
	endIdx := strings.Index(src, endMarker)
	if startIdx < 0 || endIdx < 0 || endIdx < startIdx {
		return ""
	}
	return src[startIdx+len(startMarker) : endIdx]
}

func normalizeBlocks(s string) string {
	// Trim outer whitespace; normalize CRLF; strip the marker lines
	// from generator output for direct comparison with the extracted
	// README block content.
	s = strings.ReplaceAll(s, "\r\n", "\n")
	s = strings.ReplaceAll(s, "<!-- parity:start -->", "")
	s = strings.ReplaceAll(s, "<!-- parity:end -->", "")
	s = strings.ReplaceAll(s, "<!-- tools:start -->", "")
	s = strings.ReplaceAll(s, "<!-- tools:end -->", "")
	return strings.TrimSpace(s)
}
