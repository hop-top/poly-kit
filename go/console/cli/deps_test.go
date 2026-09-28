package cli_test

import (
	"os/exec"
	"strings"
	"testing"
)

// TestCLIDoesNotLinkTheMCPSDK pins that a kit CLI which never serves
// MCP does not carry the MCP SDK: the service lives in
// go/console/cli/mcpserve, and only tools importing it pay for it. The
// rpc service's server package is held to the same rule.
func TestCLIDoesNotLinkTheMCPSDK(t *testing.T) {
	if testing.Short() {
		t.Skip("runs go list")
	}
	gobin, err := exec.LookPath("go")
	if err != nil {
		t.Skip("go toolchain not on PATH")
	}
	out, err := exec.Command(gobin, "list", "-deps", "hop.top/kit/go/console/cli").CombinedOutput()
	if err != nil {
		t.Fatalf("go list -deps: %v\n%s", err, out)
	}
	for _, pkg := range strings.Fields(string(out)) {
		if strings.HasPrefix(pkg, "github.com/modelcontextprotocol/go-sdk") ||
			pkg == "hop.top/kit/go/transport/mcpsdk" {
			t.Errorf("go/console/cli depends on %s; MCP belongs in go/console/cli/mcpserve", pkg)
		}
		// transport/rpc registers the generic CRUD proto types at
		// init, which the linker cannot drop: tens of kilobytes in
		// every CLI that never serves RPC.
		if pkg == "hop.top/kit/go/transport/rpc" {
			t.Errorf("go/console/cli depends on %s; the rpc service belongs in go/console/cli/rpcserve", pkg)
		}
	}
}
