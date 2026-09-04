package cli_test

import (
	"context"
	"testing"

	"hop.top/kit/go/console/cli"
	"hop.top/kit/go/core/netpolicy"
)

func TestOfflineMarkerCrossesPackages(t *testing.T) {
	ctx := cli.WithOffline(context.Background(), true)
	if !netpolicy.IsOffline(ctx) {
		t.Fatal("netpolicy cannot see a marker stamped by cli")
	}
	if !cli.IsOffline(netpolicy.WithOffline(context.Background(), true)) {
		t.Fatal("cli cannot see a marker stamped by netpolicy")
	}
}
