package oauth_test

import (
	"context"
	"fmt"

	"hop.top/kit/go/storage/secret/oauth"
)

// Client credentials with a client secret. The grant lives at "app/graph";
// the secret is a uma "secret" envelope at "app/graph-secret".
func Example_clientCredentials() {
	srv := tokenServer(func(f map[string]string) string {
		fmt.Println("grant_type:", f["grant_type"], "client_id:", f["client_id"], "has secret:", f["client_secret"] != "")
		return `{"access_token":"access-1","token_type":"Bearer","expires_in":3600}`
	})
	defer srv.Close()
	store, lockDir, cleanup := exampleDirs()
	defer cleanup()
	ctx := context.Background()

	grant := oauth.Grant{
		Kind: oauth.ClientCredentials, TokenURL: srv.URL, ClientID: "my-app",
		Scopes: []string{"api.read"}, SecretRef: "app/graph-secret",
	}
	raw, _ := oauth.EncodeGrant(grant)
	_ = store.Set(ctx, "app/graph", raw)
	_ = store.Set(ctx, "app/graph-secret", []byte(`{"v":1,"kind":"secret","value":"fake-client-secret"}`))

	src, err := oauth.TokenSource(ctx, store, "app/graph",
		oauth.WithHTTPClient(srv.Client()), oauth.WithLockDir(lockDir))
	if err != nil {
		panic(err)
	}
	tok, err := src.Token(ctx)
	if err != nil {
		panic(err)
	}
	fmt.Println(tok.AccessToken)
	// A second call is served from the cache: no second token request.
	tok, _ = src.Token(ctx)
	fmt.Println(tok.AccessToken)
	// Output:
	// grant_type: client_credentials client_id: my-app has secret: true
	// access-1
	// access-1
}
