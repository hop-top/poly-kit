package oauth_test

import (
	"context"
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"encoding/json"
	"encoding/pem"
	"fmt"

	"hop.top/kit/go/storage/secret/oauth"
)

// JWT bearer (service account, RFC 7523). The key JSON is generated here; a
// host stores the provider's key file as the value of a uma "secret"
// envelope.
func Example_jwtBearer() {
	srv := tokenServer(func(f map[string]string) string {
		fmt.Println("grant_type:", f["grant_type"])
		fmt.Println("assertion present:", f["assertion"] != "")
		return `{"access_token":"access-1","token_type":"Bearer","expires_in":3600}`
	})
	defer srv.Close()
	store, lockDir, cleanup := exampleDirs()
	defer cleanup()
	ctx := context.Background()

	key, _ := rsa.GenerateKey(rand.Reader, 2048)
	der, _ := x509.MarshalPKCS8PrivateKey(key)
	saJSON, _ := json.Marshal(map[string]string{
		"client_email":   "svc@example.invalid",
		"private_key_id": "key-1",
		"private_key":    string(pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: der})),
	})
	env, _ := json.Marshal(map[string]any{"v": 1, "kind": "secret", "value": string(saJSON)})
	_ = store.Set(ctx, "app/sa-key", env)

	raw, _ := oauth.EncodeGrant(oauth.Grant{
		Kind: oauth.JWTBearer, TokenURL: srv.URL, Subject: "user@example.invalid",
		Scopes: []string{"mail.read"}, SecretRef: "app/sa-key",
	})
	_ = store.Set(ctx, "app/gmail", raw)

	src, err := oauth.TokenSource(ctx, store, "app/gmail",
		oauth.WithHTTPClient(srv.Client()), oauth.WithLockDir(lockDir))
	if err != nil {
		panic(err)
	}
	tok, err := src.Token(ctx)
	if err != nil {
		panic(err)
	}
	fmt.Println(tok.AccessToken)
	// Output:
	// grant_type: urn:ietf:params:oauth:grant-type:jwt-bearer
	// assertion present: true
	// access-1
}
