package main

import (
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"

	"hop.top/kit/go/console/cli"
	"hop.top/kit/go/core/identity"
	"hop.top/kit/go/runtime/peer"
	"hop.top/kit/go/transport/api"
)

// TestPeerTrustRouteWorksWithPeerReserved pins that kit's own engine
// routes, which call the trust manager deliberately behind the engine's
// auth, are unaffected by the peer command being reserved — withheld
// from every projected surface.
func TestPeerTrustRouteWorksWithPeerReserved(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("HOME", filepath.Join(dir, "home"))
	for _, k := range []string{"XDG_CONFIG_HOME", "XDG_DATA_HOME", "XDG_STATE_HOME", "XDG_CACHE_HOME"} {
		t.Setenv(k, filepath.Join(dir, "home", k))
	}
	root := cli.New(cli.Config{Name: "kit-test", Version: "0.0.0", DisableValidate: true},
		cli.WithIdentity(cli.IdentityConfig{Dir: filepath.Join(dir, "identity")}),
		cli.WithPeers(cli.PeerConfig{DataDir: filepath.Join(dir, "peers")}))
	if !root.IsReserved("peer") {
		t.Fatal("peer is not reserved")
	}

	stranger, err := identity.Generate()
	if err != nil {
		t.Fatal(err)
	}
	pub, err := stranger.MarshalPublicKey()
	if err != nil {
		t.Fatal(err)
	}
	id := stranger.PublicKeyID()
	if err := root.PeerTrust.AcceptTOFU(peer.PeerInfo{
		ID: id, Name: "lan", Addrs: []string{"127.0.0.1:1"}, PublicKey: pub,
	}); err != nil {
		t.Fatal(err)
	}

	router := api.NewRouter()
	registerPeerRoutes(router, root)
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/peers/"+id+"/trust", nil))
	if rec.Code != http.StatusNoContent {
		t.Fatalf("POST /peers/{id}/trust = %d: %s", rec.Code, rec.Body.String())
	}
	got, err := root.PeerRegistry.Get(id)
	if err != nil || got == nil {
		t.Fatalf("peer lookup: %v, %v", got, err)
	}
	if got.Trust != peer.Trusted {
		t.Fatalf("trust = %v, want trusted", got.Trust)
	}
}
