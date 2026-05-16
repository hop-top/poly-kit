// Sacred paths are user-owned files the engine refuses to overwrite,
// even with force=true. Conflicts on these paths always route to a
// sibling .kit-suggested file so the human can diff + apply manually.
//
// Set covers project-identity / language-toolchain anchors:
//   - cmd/<name>/main.go (Go entrypoint, frequently customized)
//   - go.mod             (module path + dep set; never auto-rewrite)
//   - kit-template.yaml  (template's own manifest; recursive scaffold)
//   - .git/*             (defensive — should never appear in templates)
//
// See docs/superpowers/specs/2026-04-26-kit-init-design.md §8 (T-0949).
package template

import "path"

// isSacred reports whether rel (forward-slash separated, relative to the
// target dir) belongs to the sacred set above. Matching is glob-based via
// path.Match.
func isSacred(rel string) bool {
	for _, g := range sacredGlobs {
		if ok, err := path.Match(g, rel); err == nil && ok {
			return true
		}
	}
	return false
}

// sacredGlobs lives package-internal so engine_test can verify the set
// without exporting it. Add new entries with a code comment justifying
// why the file is human-owned.
var sacredGlobs = []string{
	"cmd/*/main.go",     // Go entrypoint
	"go.mod",            // module + dep set
	"kit-template.yaml", // template manifest
	".git/*",            // defensive
}
