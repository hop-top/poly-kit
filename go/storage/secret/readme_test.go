package secret_test

import (
	"os"
	"regexp"
	"strings"
	"testing"
)

// backendTick matches the backtick-quoted backend names the README
// lists in its "Backends" paragraph.
var backendTick = regexp.MustCompile("`([a-z0-9]+)`")

// readREADMEBackends extracts the backend names the README advertises.
// It reads the paragraph introduced by "Backends (" and stops at the
// blank line ending it, so unrelated backticked words elsewhere in the
// file (e.g. `composite`, `secret.Config`) are not picked up.
func readREADMEBackends(t *testing.T) []string {
	t.Helper()
	raw, err := os.ReadFile("README.md")
	if err != nil {
		t.Fatalf("read README.md: %v", err)
	}
	lines := strings.Split(string(raw), "\n")
	start := -1
	for i, ln := range lines {
		if strings.HasPrefix(ln, "Backends (") {
			start = i
			break
		}
	}
	if start < 0 {
		t.Fatal(`README.md has no paragraph starting with "Backends ("`)
	}
	var para []string
	for _, ln := range lines[start:] {
		if strings.TrimSpace(ln) == "" {
			break
		}
		para = append(para, ln)
	}
	var names []string
	for _, m := range backendTick.FindAllStringSubmatch(strings.Join(para, "\n"), -1) {
		names = append(names, m[1])
	}
	if len(names) == 0 {
		t.Fatal("README.md Backends paragraph names no backends")
	}
	return names
}
