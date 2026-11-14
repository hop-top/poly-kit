package oauth_test

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"net/url"

	"golang.org/x/oauth2"

	"hop.top/kit/go/transport/cmdsurface"
)

// Interactive consent over a loopback listener. A real CLI opens authorizeURL
// in a browser; here the "browser" is a plain GET of the redirect with a
// fixed code. The result carries the grant and the refresh secret to store,
// after which TokenSource works as in the refresh token example.
func Example_loopbackPKCE() {
	idp := tokenServer(func(f map[string]string) string {
		fmt.Println("exchange:", f["grant_type"], "verifier present:", f["code_verifier"] != "")
		return `{"access_token":"access-1","token_type":"Bearer","expires_in":3600,"refresh_token":"rt-1"}`
	})
	defer idp.Close()

	res, err := cmdsurface.LoopbackPKCE(context.Background(), cmdsurface.PKCEProvider{
		Name: "mail",
		Config: oauth2.Config{
			ClientID: "my-app", Scopes: []string{"mail.read"},
			Endpoint: oauth2.Endpoint{AuthURL: idp.URL + "/authorize", TokenURL: idp.URL},
		},
		SecretRef:  "app/rt",
		HTTPClient: idp.Client(),
	}, cmdsurface.LoopbackOptions{OnReady: func(authorizeURL string) {
		u, _ := url.Parse(authorizeURL)
		q := u.Query()
		fmt.Println("challenge method:", q.Get("code_challenge_method"))
		cb, _ := url.Parse(q.Get("redirect_uri"))
		cb.RawQuery = url.Values{"code": {"fake-code"}, "state": {q.Get("state")}}.Encode()
		go func() {
			resp, err := http.Get(cb.String())
			if err == nil {
				_, _ = io.Copy(io.Discard, resp.Body)
				_ = resp.Body.Close()
			}
		}()
	}})
	if err != nil {
		panic(err)
	}
	fmt.Println("grant kind:", res.Grant.Kind, "secret_ref:", res.Grant.SecretRef)
	fmt.Println("access token:", res.Token.AccessToken)
	// Store res.Grant (oauth.EncodeGrant) at the grant ref and
	// res.RefreshSecret at res.Grant.SecretRef, then call oauth.TokenSource.

	// Output:
	// challenge method: S256
	// exchange: authorization_code verifier present: true
	// grant kind: refresh_token secret_ref: app/rt
	// access token: access-1
}
