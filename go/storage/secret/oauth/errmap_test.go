package oauth

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"errors"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"golang.org/x/oauth2"

	"hop.top/kit/go/storage/secret"
	"hop.top/kit/go/storage/secret/memory"
)

func isSentinel(err error) bool {
	for _, s := range []error{ErrCredentialRejected, ErrNeedsReconsent, ErrNotWritable, ErrTransient} {
		if errors.Is(err, s) {
			return true
		}
	}
	return false
}

func TestMapTokenStatus(t *testing.T) {
	cases := []struct {
		name   string
		kind   GrantKind
		status int
		code   string
		want   error // nil: plain error, no sentinel
		in     string
	}{
		{"invalid_client", ClientCredentials, 401, "invalid_client", ErrCredentialRejected, "invalid_client"},
		{"unauthorized_client", RefreshToken, 400, "unauthorized_client", ErrCredentialRejected, "unauthorized_client"},
		{"invalid_grant cc", ClientCredentials, 400, "invalid_grant", ErrCredentialRejected, "invalid_grant"},
		{"invalid_grant assertion", ClientAssertion, 400, "invalid_grant", ErrCredentialRejected, "invalid_grant"},
		{"invalid_grant jwt", JWTBearer, 400, "invalid_grant", ErrCredentialRejected, "invalid_grant"},
		{"invalid_grant refresh", RefreshToken, 400, "invalid_grant", ErrNeedsReconsent, "invalid_grant"},
		{"interaction_required refresh", RefreshToken, 400, "interaction_required", ErrNeedsReconsent, "interaction_required"},
		{"consent_required refresh", RefreshToken, 400, "consent_required", ErrNeedsReconsent, "consent_required"},
		{"login_required refresh", RefreshToken, 400, "login_required", ErrNeedsReconsent, "login_required"},
		{"interaction_required cc", ClientCredentials, 400, "interaction_required", ErrCredentialRejected, "interaction_required"},
		{"consent_required jwt", JWTBearer, 400, "consent_required", ErrCredentialRejected, "consent_required"},
		{"login_required assertion", ClientAssertion, 400, "login_required", ErrCredentialRejected, "login_required"},
		{"other 4xx", JWTBearer, 403, "", ErrCredentialRejected, "403"},
		{"unknown code dropped", ClientCredentials, 400, "<script>x</script>", ErrCredentialRejected, "400"},
		{"500", RefreshToken, 500, "invalid_grant", ErrTransient, "500"},
		{"503", ClientAssertion, 503, "", ErrTransient, "503"},
		{"429", ClientCredentials, 429, "", ErrTransient, "429"},
		{"408", JWTBearer, 408, "", ErrTransient, "408"},
		{"307", ClientCredentials, 307, "", nil, "307"},
		{"302 refresh", RefreshToken, 302, "invalid_grant", nil, "302"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			err := mapTokenStatus(c.kind, c.status, c.code)
			if c.want == nil {
				if isSentinel(err) {
					t.Fatalf("err = %v, want no sentinel", err)
				}
			} else if !errors.Is(err, c.want) {
				t.Fatalf("err = %v, want %v", err, c.want)
			}
			if c.want != ErrNeedsReconsent && errors.Is(err, ErrNeedsReconsent) {
				t.Fatal("unexpected reconsent")
			}
			if !strings.Contains(err.Error(), c.in) || strings.Contains(err.Error(), "script") {
				t.Fatalf("message %q", err)
			}
		})
	}
}

type timeoutErr struct{}

func (timeoutErr) Error() string   { return "i/o timeout" }
func (timeoutErr) Timeout() bool   { return true }
func (timeoutErr) Temporary() bool { return true }

var _ net.Error = timeoutErr{}

func TestMapTokenError(t *testing.T) {
	canceled, cancel := context.WithCancel(context.Background())
	cancel()
	re := func(status int, code, body string) error {
		return &oauth2.RetrieveError{Response: &http.Response{StatusCode: status}, ErrorCode: code, Body: []byte(body)}
	}
	cases := []struct {
		name string
		ctx  context.Context
		kind GrantKind
		err  error
		want error
	}{
		{"retrieve code", context.Background(), ClientCredentials, re(401, "invalid_client", ""), ErrCredentialRejected},
		{"retrieve body code (jwt)", context.Background(), RefreshToken, re(400, "", `{"error":"invalid_grant"}`), ErrNeedsReconsent},
		{"retrieve 5xx", context.Background(), JWTBearer, re(502, "", "x"), ErrTransient},
		{"retrieve 3xx", context.Background(), ClientAssertion, re(307, "", ""), nil},
		{"net error", context.Background(), ClientCredentials, timeoutErr{}, ErrTransient},
		{"flattened fetch error", context.Background(), JWTBearer, errors.New("oauth2: cannot fetch token: Post x: EOF"), ErrTransient},
		{"ctx canceled", canceled, ClientCredentials, errors.New("whatever"), ErrTransient},
		{"missing access_token", context.Background(), ClientCredentials, errors.New("oauth2: server response missing access_token"), nil},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			err := mapTokenError(c.ctx, c.kind, c.err)
			if c.want == nil {
				if isSentinel(err) {
					t.Fatalf("err = %v, want no sentinel", err)
				}
				return
			}
			if !errors.Is(err, c.want) {
				t.Fatalf("err = %v, want %v", err, c.want)
			}
			if strings.Contains(err.Error(), "EOF") || strings.Contains(err.Error(), "Post x") {
				t.Fatalf("leaks underlying text: %v", err)
			}
		})
	}
}

func TestSeconds(t *testing.T) {
	for in, want := range map[string]int64{"3600": 3600, `"60"`: 60, "": 0, "null": 0, `"x"`: 0, "1.5": 0} {
		if got := seconds([]byte(in)); got != want {
			t.Errorf("seconds(%q) = %d, want %d", in, got, want)
		}
	}
}

type failStore struct{ secret.Store }

func (failStore) Get(context.Context, string) (*secret.Secret, error) {
	return nil, errors.New("backend says token=hunter2")
}

func TestReadSecretStoreFailure(t *testing.T) {
	_, err := readSecret(context.Background(), failStore{}, "k", "secret_ref")
	if !errors.Is(err, ErrTransient) || strings.Contains(err.Error(), "hunter2") {
		t.Fatalf("err = %v", err)
	}
	_, _, err = readClientCert(context.Background(), failStore{}, "k")
	if !errors.Is(err, ErrTransient) || strings.Contains(err.Error(), "hunter2") {
		t.Fatalf("cert err = %v", err)
	}
	var ve *ValidationError
	_, err = readSecret(context.Background(), memory.New(), "k", "client_secret_ref")
	if !errors.As(err, &ve) || ve.Field != "client_secret_ref" {
		t.Fatalf("not found err = %v", err)
	}
}

// A client that does not trust the server's certificate fails verification:
// a configuration fault, not a transient one.
func TestTLSVerificationFailureIsPlain(t *testing.T) {
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"access_token":"at","expires_in":3600}`))
	}))
	defer srv.Close()
	untrusting := &http.Client{}
	ctx := context.Background()

	_, err := postForm(ctx, untrusting, RefreshToken, srv.URL, url.Values{})
	if err == nil || isSentinel(err) {
		t.Errorf("postForm: err = %v, want plain error", err)
	}

	st := memory.New()
	put(t, st, "cs", secretEnv("cs-fake"))
	g := Grant{Kind: ClientCredentials, TokenURL: srv.URL, ClientID: "cid", SecretRef: "cs"}
	_, err = fetchClientCredentials(ctx, st, "g", g, untrusting)
	if err == nil || isSentinel(err) {
		t.Errorf("client_credentials: err = %v, want plain error", err)
	}

	// control: a trusting client succeeds, so the failure above is the cert
	if _, err := fetchClientCredentials(ctx, st, "g", g, srv.Client()); err != nil {
		t.Errorf("trusting client: %v", err)
	}
}

func TestMapTokenErrorTLSTypes(t *testing.T) {
	for name, err := range map[string]error{
		"unknown authority": x509.UnknownAuthorityError{},
		"invalid cert":      x509.CertificateInvalidError{Reason: x509.Expired},
		"hostname":          x509.HostnameError{Host: "x"},
		"tls verification":  &tls.CertificateVerificationError{Err: errors.New("x")},
	} {
		got := mapTokenError(context.Background(), ClientCredentials, &url.Error{Op: "Post", URL: "https://x", Err: err})
		if isSentinel(got) {
			t.Errorf("%s: err = %v, want plain error", name, got)
		}
	}
}
