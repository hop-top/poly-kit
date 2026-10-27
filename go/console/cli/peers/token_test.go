package peers_test

import (
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"hop.top/kit/go/console/cli"
	"hop.top/kit/go/console/cli/peers"
)

// TestPeersWithoutTheServePlaneMountsOnlyTheSigningTokenLeaves pins the
// unlinked half of peers' token rule: in a binary that does not link
// go/console/cli/service, as this test binary does not, peers with an
// identity gets create, decode and claims and no verify, which needs
// the plane's authn. The linked half, verify present, is pinned in the
// service package's TestTokenCommandMounting.
func TestPeersWithoutTheServePlaneMountsOnlyTheSigningTokenLeaves(t *testing.T) {
	if testing.Short() {
		t.Skip("runs go list")
	}
	gobin, err := exec.LookPath("go")
	if err != nil {
		t.Skip("go toolchain not on PATH")
	}
	out, err := exec.Command(gobin, "list", "-test", "-deps", ".").CombinedOutput()
	require.NoError(t, err, string(out))
	for _, p := range strings.Fields(string(out)) {
		require.NotEqual(t, "hop.top/kit/go/console/cli/service", p,
			"this test binary links the serve plane; the unlinked case is not what runs here")
	}

	dir := t.TempDir()
	root := cli.New(cli.Config{Name: "test", Version: "0.1.0", DisableValidate: true},
		cli.WithIdentity(cli.IdentityConfig{Dir: filepath.Join(dir, "id")}),
		peers.With(peers.Config{DataDir: dir}))
	var leaves []string
	for _, c := range root.Cmd.Commands() {
		if c.Name() == "token" {
			for _, l := range c.Commands() {
				leaves = append(leaves, l.Name())
			}
		}
	}
	sort.Strings(leaves)
	assert.Equal(t, []string{"claims", "create", "decode"}, leaves)
}
