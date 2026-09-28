package authn_test

import (
	"crypto/rand"
	"crypto/rsa"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	jose "github.com/go-jose/go-jose/v4"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"hop.top/kit/go/transport/authn"
	"hop.top/xrr"
	xrrhttp "hop.top/xrr/adapters/http"
)

// The real-provider tests replay recorded HTTP exchanges from
// testdata/cassettes. Re-record them against the live provider with
//
//	XRR_MODE=record go test ./go/transport/authn/ -run Cassette
//
// Replay is the default, so the suite never reaches the network.

const (
	googleIssuer = "https://accounts.google.com"
	googleJWKS   = "https://www.googleapis.com/oauth2/v3/certs"
)

// cassetteClient is an http.Client whose transport records or replays
// through xrr, per XRR_MODE (replay when unset).
func cassetteClient(t *testing.T, name string) *http.Client {
	t.Helper()
	mode := xrr.Mode(os.Getenv("XRR_MODE"))
	if mode == "" {
		mode = xrr.ModeReplay
	}
	dir := filepath.Join("testdata", "cassettes", name)
	if mode == xrr.ModeRecord {
		require.NoError(t, os.MkdirAll(dir, 0o755))
	}
	return &http.Client{Transport: &xrrTransport{
		session: xrr.NewSession(mode, xrr.NewFileCassette(dir)),
		adapter: xrrhttp.NewAdapter(),
		base:    http.DefaultTransport,
	}}
}

// xrrTransport is an http.RoundTripper that records or replays each
// exchange as an xrr http interaction: method, URL and body in;
// status and body out.
type xrrTransport struct {
	session *xrr.FileSession
	adapter *xrrhttp.Adapter
	base    http.RoundTripper
}

func (x *xrrTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	var body []byte
	if req.Body != nil {
		body, _ = io.ReadAll(req.Body)
		_ = req.Body.Close()
	}
	resp, err := x.session.Record(req.Context(), x.adapter,
		&xrrhttp.Request{Method: req.Method, URL: req.URL.String(), Body: string(body)},
		func() (xrr.Response, error) {
			req.Body = io.NopCloser(strings.NewReader(string(body)))
			r, err := x.base.RoundTrip(req)
			if err != nil {
				return nil, err
			}
			defer func() { _ = r.Body.Close() }()
			b, err := io.ReadAll(r.Body)
			if err != nil {
				return nil, err
			}
			return &xrrhttp.Response{Status: r.StatusCode, Body: string(b)}, nil
		})
	if err != nil {
		return nil, err
	}
	var status int
	var out string
	switch v := resp.(type) {
	case *xrrhttp.Response:
		status, out = v.Status, v.Body
	case *xrr.RawResponse:
		switch s := v.Payload["status"].(type) {
		case int:
			status = s
		case float64:
			status = int(s)
		}
		out, _ = v.Payload["body"].(string)
	default:
		return nil, fmt.Errorf("xrr: unexpected response %T", resp)
	}
	return &http.Response{
		StatusCode: status,
		Header:     http.Header{"Content-Type": []string{"application/json"}},
		Body:       io.NopCloser(strings.NewReader(out)),
		Request:    req,
	}, nil
}

// recordedKids fetches the provider's key set through client — the
// same cassette the verifier reads — and returns its kids.
func recordedKids(t *testing.T, client *http.Client, jwksURL string) []string {
	t.Helper()
	req, err := http.NewRequestWithContext(t.Context(), http.MethodGet, jwksURL, nil)
	require.NoError(t, err)
	resp, err := client.Do(req)
	require.NoError(t, err)
	defer func() { _ = resp.Body.Close() }()
	var set jose.JSONWebKeySet
	require.NoError(t, json.NewDecoder(resp.Body).Decode(&set))
	var kids []string
	for _, k := range set.Keys {
		kids = append(kids, k.KeyID)
	}
	require.NotEmpty(t, kids, "the recorded key set holds keys")
	return kids
}

// TestCassetteOIDCDiscoveryAgainstGoogle pins discovery and key-set
// parsing against a real provider's documents: the verifier resolves
// Google's jwks_uri and finds its keys by kid — a token naming one of
// them reaches the signature check and fails it (it was signed here,
// not by Google), and a token naming a kid Google does not publish is
// refused as unknown.
func TestCassetteOIDCDiscoveryAgainstGoogle(t *testing.T) {
	client := cassetteClient(t, "google")
	kids := recordedKids(t, client, googleJWKS)

	v, err := authn.NewOIDC(googleIssuer, authn.Remote{HTTPClient: client},
		authn.Options{Audience: []string{"kit-test.apps.googleusercontent.com"}})
	require.NoError(t, err)

	forger, err := rsa.GenerateKey(rand.Reader, 2048)
	require.NoError(t, err)
	now := time.Now()
	claims := with(valid(now), "iss", googleIssuer, "aud", "kit-test.apps.googleusercontent.com")

	_, err = v.Verify(t.Context(), sign(t, jose.RS256, forger, kids[0], claims))
	require.ErrorIs(t, err, authn.ErrInvalidToken)
	assert.Contains(t, err.Error(), "signature verifies with no trusted key",
		"the kid resolved to one of Google's keys, whose signature check failed")

	_, err = v.Verify(t.Context(), sign(t, jose.RS256, forger, "not-a-google-kid", claims))
	require.ErrorIs(t, err, authn.ErrInvalidToken)
	assert.Contains(t, err.Error(), `no trusted key has kid "not-a-google-kid"`)
}

// TestCassetteJWKSAgainstGoogle pins the jwks mode against the same
// recorded key set, addressed by URL rather than discovered.
func TestCassetteJWKSAgainstGoogle(t *testing.T) {
	client := cassetteClient(t, "google")
	kids := recordedKids(t, client, googleJWKS)
	v, err := authn.NewJWKS(googleJWKS, authn.Remote{HTTPClient: client}, authn.Options{})
	require.NoError(t, err)

	forger, err := rsa.GenerateKey(rand.Reader, 2048)
	require.NoError(t, err)
	_, err = v.Verify(t.Context(), sign(t, jose.RS256, forger, kids[len(kids)-1], valid(time.Now())))
	require.ErrorIs(t, err, authn.ErrInvalidToken)
	assert.Contains(t, err.Error(), "signature verifies with no trusted key")
}
