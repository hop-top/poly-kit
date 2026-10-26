package peers_test

import (
	"os/exec"
	"strings"
	"testing"
)

// TestPeersLinkNoServePlane pins that discovering peers does not force
// serving on a tool: peers attaches to cli.Root and links neither the
// serve plane nor the transport bridge.
func TestPeersLinkNoServePlane(t *testing.T) {
	if testing.Short() {
		t.Skip("runs go list")
	}
	gobin, err := exec.LookPath("go")
	if err != nil {
		t.Skip("go toolchain not on PATH")
	}
	out, err := exec.Command(gobin, "list", "-deps", "hop.top/kit/go/console/cli/peers").CombinedOutput()
	if err != nil {
		t.Fatalf("go list -deps: %v\n%s", err, out)
	}
	forbidden := map[string]string{
		"hop.top/kit/go/console/cli/service":  "the serve plane is opt-in (go/console/cli/service)",
		"hop.top/kit/go/transport/cmdsurface": "the bridge belongs to the serve plane",
	}
	sawPeer := false
	for _, p := range strings.Fields(string(out)) {
		if p == "hop.top/kit/go/runtime/peer" {
			sawPeer = true
		}
		if why, ok := forbidden[p]; ok {
			t.Errorf("peers links %s: %s", p, why)
		}
	}
	if !sawPeer {
		t.Fatal("peers does not link go/runtime/peer; the guard is not looking where it should")
	}
}
