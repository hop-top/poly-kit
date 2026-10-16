package admittedctx_test

import (
	"os/exec"
	"strings"
	"testing"
)

// TestOnlyTheBridgeAndTheLeafImportTheKey enforces the convention the
// package doc states: inside the module, only cmdsurface writes the
// admitted caller and only the admitted leaf reads it. Test files may
// import the key to stamp a caller without building a bridge.
func TestOnlyTheBridgeAndTheLeafImportTheKey(t *testing.T) {
	if testing.Short() {
		t.Skip("runs go list")
	}
	gobin, err := exec.LookPath("go")
	if err != nil {
		t.Skip("go toolchain not on PATH")
	}
	const key = "hop.top/kit/internal/admittedctx"
	allowed := map[string]bool{
		"hop.top/kit/go/transport/cmdsurface":          true,
		"hop.top/kit/go/transport/cmdsurface/admitted": true,
	}
	// .Imports lists a package's non-test imports only; _test.go
	// files are free to import the key.
	out, err := exec.Command(gobin, "list", "-e", "-f",
		`{{.ImportPath}} {{join .Imports " "}}`, "hop.top/kit/...").CombinedOutput()
	if err != nil {
		t.Fatalf("go list: %v\n%s", err, out)
	}
	found := 0
	for _, line := range strings.Split(strings.TrimSpace(string(out)), "\n") {
		f := strings.Fields(line)
		if len(f) == 0 {
			continue
		}
		for _, imp := range f[1:] {
			if imp != key {
				continue
			}
			if !allowed[f[0]] {
				t.Errorf("%s imports %s; only cmdsurface and the admitted leaf may", f[0], key)
			} else {
				found++
			}
		}
	}
	if found != len(allowed) {
		t.Errorf("%d allowed importers seen, want %d: the guard is not looking where it should", found, len(allowed))
	}
}
