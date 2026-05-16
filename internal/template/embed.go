package template

import (
	"embed"
	"io/fs"
	"sort"
)

// builtins/ is populated by `make builtins-sync` from templates/cli-{go,ts,py,shared}.
// During sync, *.go and go.mod files are renamed to *.tmpl so (a) Go's embed
// does not refuse a nested module, (b) `go build ./...` does not try to compile
// template placeholders. Render engine restores the original names at output
// time. Manifest-ization (T-0841) will move this rename into per-template
// file rules.
//
//go:embed all:builtins/*
var builtinFS embed.FS

// BuiltIn returns a sub-fs rooted at "builtins/" so callers see
// each template at its own top-level (e.g. "cli-go/kit-template.yaml").
func BuiltIn() (fs.FS, error) {
	return fs.Sub(builtinFS, "builtins")
}

// Available lists immediate subdirs of builtins/, one per template.
// Returns names sorted alphabetically.
func Available() ([]string, error) {
	sub, err := BuiltIn()
	if err != nil {
		return nil, err
	}
	entries, err := fs.ReadDir(sub, ".")
	if err != nil {
		return nil, err
	}
	var names []string
	for _, e := range entries {
		if e.IsDir() {
			names = append(names, e.Name())
		}
	}
	sort.Strings(names)
	return names, nil
}
