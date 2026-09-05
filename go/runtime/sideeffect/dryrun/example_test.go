package dryrun_test

import (
	"os"

	"hop.top/kit/go/runtime/sideeffect/dryrun"
)

func Example() {
	fs := dryrun.NewFS(dryrun.WithWriter(os.Stdout))
	if err := fs.WriteFile("/etc/app.yaml", []byte("key: v"), 0o600); err != nil {
		panic(err) // never happens: dryrun returns nil for would-be calls
	}
	if err := fs.Remove("/etc/app.yaml"); err != nil {
		panic(err)
	}

	// Output:
	// [dry-run] would write /etc/app.yaml (6 bytes, mode 0600)
	// [dry-run] would remove /etc/app.yaml
}
