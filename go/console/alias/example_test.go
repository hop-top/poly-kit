package alias_test

import (
	"fmt"
	"os"
	"path/filepath"

	"hop.top/kit/go/console/alias"
)

// ExampleStore shows the load / set / save / expand cycle. Expand
// rewrites only args[0], splitting the target on spaces and keeping
// the remaining args untouched.
func ExampleStore() {
	dir, err := os.MkdirTemp("", "alias-example")
	if err != nil {
		fmt.Println("tempdir:", err)
		return
	}
	defer os.RemoveAll(dir)

	s := alias.NewStore(filepath.Join(dir, "aliases.yaml"))
	if err := s.Load(); err != nil { // a missing file is not an error
		fmt.Println("load:", err)
		return
	}
	if err := s.Set("ml", "mission list"); err != nil {
		fmt.Println("set:", err)
		return
	}
	if err := s.Save(); err != nil { // creates parent dirs
		fmt.Println("save:", err)
		return
	}

	// args[0] matches: the target is split on spaces into separate
	// elements, and the remaining args are kept. Printed with %q so
	// the element boundaries are visible -- "mission list" as a
	// single unsplit element would render the same under %v.
	fmt.Printf("%q\n", s.Expand([]string{"ml", "--format", "json"}))
	// No match: args are returned unchanged.
	fmt.Printf("%q\n", s.Expand([]string{"other", "--format", "json"}))

	target, ok := s.Get("ml")
	fmt.Println(target, ok)

	// Remove reports an error for a name that is not registered.
	fmt.Println(s.Remove("nosuch"))

	// Output:
	// ["mission" "list" "--format" "json"]
	// ["other" "--format" "json"]
	// mission list true
	// alias: "nosuch" not found
}
