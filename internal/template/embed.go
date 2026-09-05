package template

import (
	"embed"
	"io/fs"
	"sort"
)

// builtins/ is a verbatim copy of templates/cli-{go,ts,py,php,rs} and
// templates/shared, produced by `make builtins-sync` and checked by
// `make check-mirror-sync`. Go sources (*.go, go.mod) ship as *.tmpl on both
// sides so (a) Go's embed does not refuse a nested module and (b) `go build
// ./...` does not try to compile template placeholders; `make
// check-template-sources` enforces the suffix and the render engine strips
// it at output time (render_rules.strip_suffixes).
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
