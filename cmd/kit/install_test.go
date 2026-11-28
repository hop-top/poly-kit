package main

import (
	"os"
	"path/filepath"
	"testing"

	"golang.org/x/mod/modfile"
)

// TestRootModule_NoReplaceDirectives guards `go install
// hop.top/kit/cmd/kit@<version>`: the go command refuses to install
// from a module whose go.mod carries replace directives, and the
// published tag is the root module. Local co-development belongs in an
// uncommitted go.work file, not in go.mod.
func TestRootModule_NoReplaceDirectives(t *testing.T) {
	path := filepath.Join("..", "..", "go.mod")
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	f, err := modfile.Parse(path, data, nil)
	if err != nil {
		t.Fatalf("parse %s: %v", path, err)
	}
	for _, r := range f.Replace {
		t.Errorf("go.mod replace %s => %s breaks "+
			"`go install hop.top/kit/cmd/kit@<version>`; "+
			"depend on the published version and use a local "+
			"go.work for co-development",
			r.Old.Path, r.New.Path)
	}
}
