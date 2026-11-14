package oauth_test

import (
	"context"
	"fmt"
	"strings"

	"hop.top/kit/go/storage/secret/oauth"
)

// Refresh token with rotation. Each exchange returns a new refresh token;
// the source writes it back to the store before the next exchange.
func Example_refreshToken() {
	n := 0
	srv := tokenServer(func(f map[string]string) string {
		n++
		fmt.Printf("exchange %d: grant_type=%s refresh_token=%s\n", n, f["grant_type"], f["refresh_token"])
		return fmt.Sprintf(`{"access_token":"access-%d","token_type":"Bearer","expires_in":3600,"refresh_token":"rt-%d"}`, n, n)
	})
	defer srv.Close()
	store, lockDir, cleanup := exampleDirs()
	defer cleanup()
	ctx := context.Background()

	grant, err := oauth.NewRefreshGrant(srv.URL, "my-app", "app/rt", "", []string{"mail.read"})
	if err != nil {
		panic(err)
	}
	raw, _ := oauth.EncodeGrant(grant)
	_ = store.Set(ctx, "app/mail", raw)
	rt, _ := oauth.EncodeRefreshSecret("rt-0")
	_ = store.Set(ctx, "app/rt", rt)

	src, err := oauth.TokenSource(ctx, store, "app/mail",
		oauth.WithHTTPClient(srv.Client()), oauth.WithLockDir(lockDir))
	if err != nil {
		panic(err)
	}
	tok, _ := src.Token(ctx)
	fmt.Println(tok.AccessToken)

	// Invalidate (a host calls it after a 401) forces the second exchange.
	src.Invalidate()
	tok, _ = src.Token(ctx)
	fmt.Println(tok.AccessToken)

	sec, _ := store.Get(ctx, "app/rt")
	fmt.Println("stored refresh token is rt-2:", strings.Contains(string(sec.Value), "rt-2"))
	// Output:
	// exchange 1: grant_type=refresh_token refresh_token=rt-0
	// access-1
	// exchange 2: grant_type=refresh_token refresh_token=rt-1
	// access-2
	// stored refresh token is rt-2: true
}
