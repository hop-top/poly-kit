package badge_test

import (
	"fmt"
	"os"
	"path/filepath"

	"hop.top/kit/go/console/cli/conformance/badge"
)

// ExampleCmd writes the ungradable seed badge that kit init ships.
func ExampleCmd() {
	out := filepath.Join(os.TempDir(), "example-12fc.json")
	defer os.Remove(out)

	cmd := badge.Cmd()
	cmd.SetArgs([]string{"--emit-seed", "--output", out})
	cmd.SetOut(os.Stderr)
	if err := cmd.Execute(); err != nil {
		fmt.Println("error:", err)
		return
	}

	data, _ := os.ReadFile(out)
	fmt.Print(string(data))
	// Output:
	// {
	//   "schemaVersion": 1,
	//   "label": "12-factor AI-CLI",
	//   "message": "ungradable",
	//   "color": "lightgrey",
	//   "labelColor": "555",
	//   "namedLogo": "robotframework",
	//   "cacheSeconds": 300
	// }
}
