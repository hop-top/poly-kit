package real_test

import (
	"fmt"
	"os"
	"path/filepath"

	"hop.top/kit/go/runtime/sideeffect"
	"hop.top/kit/go/runtime/sideeffect/real"
)

func Example() {
	dir, _ := os.MkdirTemp("", "real")
	defer os.RemoveAll(dir)

	var fs sideeffect.FS = real.FS{} // zero value delegates to os
	path := filepath.Join(dir, "out.txt")
	if err := fs.WriteFile(path, []byte("hello"), 0o600); err != nil {
		panic(err)
	}
	data, _ := os.ReadFile(path)
	fmt.Println(string(data))

	// Output:
	// hello
}
