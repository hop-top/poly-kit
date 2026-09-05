package shim_test

import (
	"fmt"

	"hop.top/kit/go/core/uxp/invoke/shim"
)

// S-1: directory scope for CLIs that accept dirs but not files.
func ExampleExpandToParentDirs() {
	dirs := shim.ExpandToParentDirs([]string{
		"src/app/main.go",
		"src/app/util.go",
		"docs/README.md",
	})
	fmt.Println(dirs)
	// Output: [docs src/app]
}
