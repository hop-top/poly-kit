package oauth_test

import (
	"context"
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/json"
	"encoding/pem"
	"fmt"
	"math/big"
	"time"

	"hop.top/kit/go/storage/secret/oauth"
)

// Client credentials authenticated by a signed assertion (certificate). The
// certificate and key are generated here; a host stores its own as a uma
// "client_cert" envelope.
func Example_clientAssertion() {
	srv := tokenServer(func(f map[string]string) string {
		fmt.Println("grant_type:", f["grant_type"])
		fmt.Println("assertion_type:", f["client_assertion_type"])
		fmt.Println("assertion present:", f["client_assertion"] != "", "client_secret sent:", f["client_secret"] != "")
		return `{"access_token":"access-1","token_type":"Bearer","expires_in":3600}`
	})
	defer srv.Close()
	store, lockDir, cleanup := exampleDirs()
	defer cleanup()
	ctx := context.Background()

	key, _ := rsa.GenerateKey(rand.Reader, 2048)
	tpl := &x509.Certificate{
		SerialNumber: big.NewInt(1), Subject: pkix.Name{CommonName: "example"},
		NotBefore: time.Now().Add(-time.Hour), NotAfter: time.Now().Add(time.Hour),
	}
	der, _ := x509.CreateCertificate(rand.Reader, tpl, tpl, &key.PublicKey, key)
	env, _ := json.Marshal(map[string]any{
		"v": 1, "kind": "client_cert",
		"certificate_pem": string(pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der})),
		"private_key_pem": string(pem.EncodeToMemory(&pem.Block{Type: "RSA PRIVATE KEY", Bytes: x509.MarshalPKCS1PrivateKey(key)})),
	})
	_ = store.Set(ctx, "app/cert", env)

	raw, _ := oauth.EncodeGrant(oauth.Grant{
		Kind: oauth.ClientAssertion, TokenURL: srv.URL, ClientID: "my-app",
		Scopes: []string{"api.read"}, ClientCertRef: "app/cert",
	})
	_ = store.Set(ctx, "app/graph", raw)

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
	// Output:
	// grant_type: client_credentials
	// assertion_type: urn:ietf:params:oauth:client-assertion-type:jwt-bearer
	// assertion present: true client_secret sent: false
	// access-1
}
