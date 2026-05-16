package peer_test

import (
	"context"
	"fmt"
	"os"
	"path/filepath"

	"hop.top/kit/go/core/identity"
	"hop.top/kit/go/runtime/peer"
	"hop.top/kit/go/storage/sqlstore"
)

func tempStore() *sqlstore.Store {
	dir, err := os.MkdirTemp("", "peer-example-*")
	if err != nil {
		panic(err)
	}
	s, err := sqlstore.Open(filepath.Join(dir, "test.db"), sqlstore.Options{})
	if err != nil {
		panic(err)
	}
	return s
}

func ExampleNewRegistry() {
	store := tempStore()
	defer store.Close()

	kp, _ := identity.Generate()
	reg := peer.NewRegistry(store)

	_ = reg.Add(peer.PeerInfo{
		ID:        kp.PublicKeyID(),
		Name:      "peer-1",
		Addrs:     []string{"127.0.0.1:9000"},
		PublicKey: kp.PublicKey,
	})

	list, _ := reg.List()
	fmt.Println(len(list))
	fmt.Println(list[0].Name)
	// Output:
	// 1
	// peer-1
}

func ExampleTrustManager_AcceptTOFU() {
	store := tempStore()
	defer store.Close()

	self, _ := identity.Generate()
	remote, _ := identity.Generate()

	reg := peer.NewRegistry(store)
	tm := peer.NewTrustManager(reg, self)

	info := peer.PeerInfo{
		ID:        remote.PublicKeyID(),
		Name:      "new-peer",
		Addrs:     []string{"10.0.0.1:8080"},
		PublicKey: remote.PublicKey,
	}

	// First encounter: sets PendingTOFU
	err := tm.AcceptTOFU(info)
	fmt.Println(err)

	// Explicitly promote to trusted
	err = tm.Trust(remote.PublicKeyID())
	fmt.Println(err)

	trusted, _ := tm.IsTrusted(remote.PublicKeyID())
	fmt.Println(trusted)
	// Output:
	// <nil>
	// <nil>
	// true
}

func ExampleStaticDiscoverer() {
	kp, _ := identity.Generate()
	peers := []peer.PeerInfo{
		{ID: kp.PublicKeyID(), Name: "alpha", Addrs: []string{"10.0.0.1:80"}, PublicKey: kp.PublicKey},
	}

	disc := &peer.StaticDiscoverer{Peers: peers}
	found, _ := disc.Browse(context.Background())

	fmt.Println(len(found))
	fmt.Println(found[0].Name)
	// Output:
	// 1
	// alpha
}

func ExampleNewMesh() {
	store := tempStore()
	defer store.Close()

	self, _ := identity.Generate()
	reg := peer.NewRegistry(store)
	tm := peer.NewTrustManager(reg, self)
	disc := &peer.StaticDiscoverer{}

	selfInfo := peer.PeerInfo{
		ID:        self.PublicKeyID(),
		Name:      "me",
		Addrs:     []string{"127.0.0.1:7000"},
		PublicKey: self.PublicKey,
	}

	m := peer.NewMesh(selfInfo, tm, disc)
	fmt.Println(m != nil)
	// Output:
	// true
}
