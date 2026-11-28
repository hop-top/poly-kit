package main

import (
	"bytes"
	"os"
	"strings"
	"testing"
)

// TestQuickstartGreet runs the command through the real Execute path
// and compares its JSON output with quickstart.expected.
func TestQuickstartGreet(t *testing.T) {
	root := newRoot()
	var out bytes.Buffer
	root.Cmd.SetOut(&out)
	root.SetArgs([]string{"greet", "Ada", "--format", "json"})

	if err := root.Execute(t.Context()); err != nil {
		t.Fatalf("execute: %v", err)
	}
	want, err := os.ReadFile("quickstart.expected")
	if err != nil {
		t.Fatal(err)
	}
	if got := strings.TrimSpace(out.String()); got != strings.TrimSpace(string(want)) {
		t.Fatalf("output differs\n got: %s\nwant: %s", got, want)
	}
}
