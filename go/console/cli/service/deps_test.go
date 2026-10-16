package service_test

import (
	"os/exec"
	"strings"
	"testing"
)

// TestServePlaneLinksNoOptInService pins that serving does not force
// the opt-in services on a tool: mcpserve, rpcserve and celpermission
// import this package, never the reverse, so a tool serving REST and
// the socket links neither the MCP SDK, the rpc transport nor CEL.
// Serving does not force peer discovery either: no runtime/peer, no
// mDNS.
func TestServePlaneLinksNoOptInService(t *testing.T) {
	if testing.Short() {
		t.Skip("runs go list")
	}
	gobin, err := exec.LookPath("go")
	if err != nil {
		t.Skip("go toolchain not on PATH")
	}
	out, err := exec.Command(gobin, "list", "-deps", "-f",
		"{{if not .Standard}}{{.ImportPath}} {{if .Module}}{{.Module.Path}}{{end}}{{end}}",
		"hop.top/kit/go/console/cli/service").CombinedOutput()
	if err != nil {
		t.Fatalf("go list -deps: %v\n%s", err, out)
	}
	forbidden := map[string]bool{
		"hop.top/kit/go/console/cli/mcpserve":      true,
		"hop.top/kit/go/console/cli/rpcserve":      true,
		"hop.top/kit/go/console/cli/celpermission": true,
		"hop.top/kit/go/transport/mcpsdk":          true,
		"hop.top/kit/go/transport/rpc":             true,
		"hop.top/kit/go/console/cli/peers":         true,
		"hop.top/kit/go/runtime/peer":              true,
	}
	modules := []string{"github.com/modelcontextprotocol/go-sdk", "cel.dev/cel-go", "github.com/google/cel-go",
		"github.com/hashicorp/mdns", "github.com/miekg/dns"}
	sawCLI := false
	for _, line := range strings.Split(strings.TrimSpace(string(out)), "\n") {
		f := strings.Fields(line)
		if len(f) == 0 {
			continue
		}
		if f[0] == "hop.top/kit/go/console/cli" {
			sawCLI = true
		}
		if forbidden[f[0]] {
			t.Errorf("the serve plane links %s", f[0])
		}
		if len(f) > 1 {
			for _, m := range modules {
				if f[1] == m {
					t.Errorf("the serve plane links module %s (via %s)", m, f[0])
				}
			}
		}
	}
	if !sawCLI {
		t.Fatal("the serve plane does not link go/console/cli; the guard is not looking where it should")
	}
}
