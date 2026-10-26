package peers_test

import (
	"fmt"
	"os"

	"hop.top/kit/go/console/cli"
	"hop.top/kit/go/console/cli/peers"
)

func ExampleWith() {
	dir, _ := os.MkdirTemp("", "peers")
	defer os.RemoveAll(dir)

	root := cli.New(cli.Config{Name: "mytool", Version: "0.1.0", Short: "peers example"},
		cli.WithIdentity(cli.IdentityConfig{Dir: dir}),
		peers.With(peers.Config{DataDir: dir}))

	p := peers.Of(root)
	fmt.Println("mesh:", p.Mesh != nil)
	known, _ := p.Registry.List()
	fmt.Println("known peers:", len(known))

	bare := cli.New(cli.Config{Name: "other", Version: "0.1.0", Short: "no peers"})
	fmt.Println("mesh without peers.With:", peers.Of(bare).Mesh != nil)
	// Output:
	// mesh: true
	// known peers: 0
	// mesh without peers.With: false
}
