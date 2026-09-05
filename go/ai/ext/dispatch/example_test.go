package dispatch_test

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/spf13/cobra"

	"hop.top/kit/go/ai/ext/dispatch"
)

func Example() {
	dir, _ := os.MkdirTemp("", "demo-plugins")
	defer os.RemoveAll(dir)
	_ = os.WriteFile(filepath.Join(dir, "demo-hello"), []byte("#!/bin/sh\necho hello\n"), 0o755)

	root := &cobra.Command{Use: "demo"}
	dispatch.Register(root, "demo", dir)
	for _, c := range root.Commands() {
		fmt.Println(c.Name(), c.GroupID)
	}
	// Output:
	// hello plugins
}
