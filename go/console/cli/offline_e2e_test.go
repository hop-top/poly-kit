package cli_test

import (
	"errors"
	"net/http"
	"testing"

	"github.com/spf13/cobra"
	"hop.top/kit/go/console/cli"
	"hop.top/kit/go/core/netpolicy"
)

// End-to-end: a leaf that naively uses http.DefaultClient — i.e. an
// adopter who never heard of IsOffline — must still be refused when the
// user passes --offline. This is the guarantee the guide promises.
func TestOfflineIsEnforcedForNaiveLeaf(t *testing.T) {
	orig := http.DefaultTransport
	t.Cleanup(func() { http.DefaultTransport = orig })

	var gotErr error
	leaf := &cobra.Command{
		Use: "fetch",
		RunE: func(cmd *cobra.Command, _ []string) error {
			req, _ := http.NewRequestWithContext(
				cmd.Context(), http.MethodGet, "https://example.invalid/x", nil)
			_, gotErr = http.DefaultClient.Do(req) // naive: no IsOffline check
			return nil
		},
	}

	root := cli.New(cli.Config{Name: "probe", Version: "0.0.0"})
	root.Cmd.AddCommand(leaf)
	root.Cmd.SetArgs([]string{"fetch", "--offline"})
	if err := root.Cmd.Execute(); err != nil {
		t.Fatalf("execute: %v", err)
	}

	if !errors.Is(gotErr, netpolicy.ErrOffline) {
		t.Fatalf("naive leaf reached the network under --offline: %v", gotErr)
	}
}
