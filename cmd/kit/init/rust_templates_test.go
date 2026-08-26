// Static guard over the built-in Rust templates.
//
// rustc rejects a file that both imports a name via `use` and defines a
// top-level item with the same name (error E0255); a template carrying
// such a collision makes every scaffolded project fail `cargo check`
// immediately. The render pipeline never compiles Rust, so this pins the
// invariant statically against the embedded builtins.
package kitinit

import (
	"io/fs"
	"regexp"
	"strings"
	"testing"

	tmpl "hop.top/kit/internal/template"
)

func TestBuiltinRustTemplates_UseImportsDoNotShadowLocalItems(t *testing.T) {
	root, err := tmpl.BuiltIn()
	if err != nil {
		t.Fatalf("BuiltIn: %v", err)
	}
	checked := 0
	err = fs.WalkDir(root, ".", func(path string, d fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if d.IsDir() || (!strings.HasSuffix(path, ".rs") && !strings.HasSuffix(path, ".rs.tmpl")) {
			return nil
		}
		data, err := fs.ReadFile(root, path)
		if err != nil {
			return err
		}
		checked++
		imported := rustUseLeafNames(string(data))
		for _, name := range rustTopLevelItemNames(string(data)) {
			if imported[name] {
				t.Errorf("%s: `use` imports %q, which the file also defines at top level (rustc E0255); alias the import or use a fully qualified path", path, name)
			}
		}
		return nil
	})
	if err != nil {
		t.Fatalf("walk builtins: %v", err)
	}
	if checked == 0 {
		t.Fatal("no .rs templates found in builtins — walk is miswired")
	}
}

var rustUseLine = regexp.MustCompile(`^\s*(?:pub\s+)?use\s+(.+);\s*$`)

// rustUseLeafNames extracts the names a file's single-line `use`
// declarations bring into scope: the last path segment, or the alias
// after `as`. Grouped imports (`use a::{b, c as d};`) are flattened;
// glob and `self` imports are skipped.
func rustUseLeafNames(src string) map[string]bool {
	names := make(map[string]bool)
	for _, line := range strings.Split(src, "\n") {
		m := rustUseLine.FindStringSubmatch(line)
		if m == nil {
			continue
		}
		spec := m[1]
		leaves := []string{spec}
		if open := strings.Index(spec, "{"); open >= 0 {
			inner := strings.TrimSuffix(strings.TrimSpace(spec[open+1:]), "}")
			leaves = strings.Split(inner, ",")
		}
		for _, leaf := range leaves {
			leaf = strings.TrimSpace(leaf)
			if _, alias, ok := strings.Cut(leaf, " as "); ok {
				leaf = alias
			}
			segs := strings.Split(leaf, "::")
			name := strings.TrimSpace(segs[len(segs)-1])
			if name == "" || name == "*" || name == "self" {
				continue
			}
			names[name] = true
		}
	}
	return names
}

var rustTopLevelItem = regexp.MustCompile(`^(?:pub(?:\([^)]*\))?\s+)?(?:struct|enum|trait|union|fn|type|mod)\s+([A-Za-z_][A-Za-z0-9_]*)`)

// rustTopLevelItemNames lists names defined by unindented item
// declarations. Indented (nested) items are ignored on purpose: only
// same-scope definitions collide with top-level `use` imports.
func rustTopLevelItemNames(src string) []string {
	var names []string
	for _, line := range strings.Split(src, "\n") {
		if m := rustTopLevelItem.FindStringSubmatch(line); m != nil {
			names = append(names, m[1])
		}
	}
	return names
}
