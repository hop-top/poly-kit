package admitted_test

import (
	"os/exec"
	"strings"
	"testing"
)

// TestLeafClosure pins what the leaf may link: the standard library,
// pflag, itself and the module-internal context key. go/console/cli
// imports this package on every CLI's path, so a leaf that quietly
// grows an import puts that import in every kit binary, served or not.
func TestLeafClosure(t *testing.T) {
	if testing.Short() {
		t.Skip("runs go list")
	}
	gobin, err := exec.LookPath("go")
	if err != nil {
		t.Skip("go toolchain not on PATH")
	}
	out, err := exec.Command(gobin, "list", "-deps", "-f",
		"{{if not .Standard}}{{.ImportPath}} {{if .Module}}{{.Module.Path}}{{end}}{{end}}",
		"hop.top/kit/go/transport/cmdsurface/admitted").CombinedOutput()
	if err != nil {
		t.Fatalf("go list -deps: %v\n%s", err, out)
	}
	allowedPkgs := map[string]bool{
		"hop.top/kit/go/transport/cmdsurface/admitted": true,
		"hop.top/kit/internal/admittedctx":             true,
	}
	for _, line := range strings.Split(strings.TrimSpace(string(out)), "\n") {
		f := strings.Fields(line)
		if len(f) == 0 {
			continue
		}
		pkg, mod := f[0], ""
		if len(f) > 1 {
			mod = f[1]
		}
		switch mod {
		case "hop.top/kit":
			if !allowedPkgs[pkg] {
				t.Errorf("the admitted leaf links %s", pkg)
			}
		case "github.com/spf13/pflag":
		default:
			t.Errorf("the admitted leaf links %s (module %q)", pkg, mod)
		}
	}
}
