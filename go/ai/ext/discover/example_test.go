package discover_test

import (
	"fmt"
	"os"
	"path/filepath"

	"hop.top/kit/go/ai/ext/discover"
)

func Example() {
	dir, _ := os.MkdirTemp("", "demo-plugins")
	defer os.RemoveAll(dir)
	_ = os.WriteFile(filepath.Join(dir, "demo-hello"), []byte("#!/bin/sh\necho hello\n"), 0o755)

	s := &discover.Scanner{Prefix: "demo-", Paths: []string{dir}}
	found, _ := s.Scan()
	for _, f := range found {
		fmt.Println(f.Name, f.Meta().Name, f.Capabilities())
	}
	// Output:
	// hello hello discover
}
