package oauth

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync/atomic"
	"testing"

	"hop.top/kit/go/storage/secret"
	"hop.top/kit/go/storage/secret/agefile"
	"hop.top/kit/go/storage/secret/ghsecrets"
	"hop.top/kit/go/storage/secret/infisical"
	"hop.top/kit/go/storage/secret/memory"
	"hop.top/kit/go/storage/secret/onepassword"
)

const (
	rtOld    = "rt-fake-old-value"
	rtNew    = "rt-fake-new-value"
	rtClient = "rt-fake-client-secret"
)

// rtStore is a writable in-memory store; fail makes Set error.
type rtStore struct {
	*memory.Store
	fail bool
	sets int
}

func (s *rtStore) Writable() bool { return true }
func (s *rtStore) Set(ctx context.Context, k string, v []byte) error {
	s.sets++
	if s.fail {
		return errors.New("disk full")
	}
	return s.Store.Set(ctx, k, v)
}

func rtNewStore(t *testing.T) *rtStore {
	t.Helper()
	s := &rtStore{Store: memory.New()}
	ctx := context.Background()
	_ = s.Store.Set(ctx, "rt", []byte(`{"v":1,"kind":"secret","value":"`+rtOld+`"}`))
	_ = s.Store.Set(ctx, "cs", []byte(`{"v":1,"kind":"secret","value":"`+rtClient+`"}`))
	return s
}

func rtGrant(u string) Grant {
	return Grant{Kind: RefreshToken, TokenURL: u, ClientID: "cid", Scopes: []string{"a", "b"}, SecretRef: "rt"}
}

func rtServer(t *testing.T, status int, body string, got *url.Values) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = r.ParseForm()
		if got != nil {
			*got = r.PostForm
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(status)
		_, _ = w.Write([]byte(body))
	}))
	t.Cleanup(srv.Close)
	return srv
}

func rtStored(t *testing.T, s secret.Store) string {
	t.Helper()
	got, err := s.Get(context.Background(), "rt")
	if err != nil {
		t.Fatal(err)
	}
	return string(got.Value)
}

func TestFetchRefreshTokenRotation(t *testing.T) {
	var form url.Values
	srv := rtServer(t, 200, `{"access_token":"at-fake","expires_in":3600,"refresh_token":"`+rtNew+`"}`, &form)
	st := rtNewStore(t)
	tok, err := fetchRefreshToken(context.Background(), st, "r", rtGrant(srv.URL), srv.Client())
	if err != nil {
		t.Fatal(err)
	}
	if tok.AccessToken != "at-fake" || tok.Validate() != nil {
		t.Errorf("bad token %+v", tok)
	}
	for k, v := range map[string]string{"grant_type": "refresh_token", "refresh_token": rtOld, "client_id": "cid", "scope": "a b"} {
		if form.Get(k) != v {
			t.Errorf("form %s = %q, want %q", k, form.Get(k), v)
		}
	}
	if _, ok := form["client_secret"]; ok {
		t.Error("public client must not send client_secret")
	}
	if want := `{"v":1,"kind":"secret","value":"` + rtNew + `"}`; rtStored(t, st) != want {
		t.Errorf("stored = %s", rtStored(t, st))
	}
}

func TestFetchRefreshTokenConfidentialClient(t *testing.T) {
	var form url.Values
	srv := rtServer(t, 200, `{"access_token":"at","expires_in":"60"}`, &form)
	g := rtGrant(srv.URL)
	g.ClientSecretRef = "cs"
	if _, err := fetchRefreshToken(context.Background(), rtNewStore(t), "r", g, srv.Client()); err != nil {
		t.Fatal(err)
	}
	if form.Get("client_secret") != rtClient {
		t.Error("client_secret not sent")
	}
}

func TestFetchRefreshTokenNoRotation(t *testing.T) {
	for _, body := range []string{
		`{"access_token":"at","expires_in":60}`,
		`{"access_token":"at","expires_in":60,"refresh_token":"` + rtOld + `"}`,
	} {
		srv := rtServer(t, 200, body, nil)
		st := rtNewStore(t)
		if _, err := fetchRefreshToken(context.Background(), st, "r", rtGrant(srv.URL), srv.Client()); err != nil {
			t.Fatal(err)
		}
		if st.sets != 0 {
			t.Errorf("store written without rotation: %s", body)
		}
	}
}

func TestFetchRefreshTokenWriteFailure(t *testing.T) {
	srv := rtServer(t, 200, `{"access_token":"at-fake","expires_in":60,"refresh_token":"`+rtNew+`"}`, nil)
	st := rtNewStore(t)
	st.fail = true
	tok, err := fetchRefreshToken(context.Background(), st, "r", rtGrant(srv.URL), srv.Client())
	if err == nil || tok.AccessToken != "" {
		t.Fatalf("want error and no token, got %v %+v", err, tok)
	}
	if !strings.Contains(rtStored(t, st), rtOld) {
		t.Error("old refresh token not intact")
	}
	for _, v := range []string{rtOld, rtNew, "at-fake", "disk full"} {
		if strings.Contains(err.Error(), v) {
			t.Errorf("error leaks %q", v)
		}
	}
}

func TestFetchRefreshTokenErrors(t *testing.T) {
	cases := []struct {
		name   string
		status int
		body   string
		want   error
	}{
		{"invalid_grant", 400, `{"error":"invalid_grant","error_description":"` + rtOld + `"}`, ErrNeedsReconsent},
		{"invalid_client", 401, `{"error":"invalid_client"}`, ErrCredentialRejected},
		{"unauthorized_client", 400, `{"error":"unauthorized_client"}`, ErrCredentialRejected},
		{"other 4xx", 403, `nope`, ErrCredentialRejected},
		{"500", 500, `x`, ErrTransient},
		{"429", 429, `x`, ErrTransient},
		{"408", 408, `x`, ErrTransient},
	}
	for _, c := range cases {
		srv := rtServer(t, c.status, c.body, nil)
		st := rtNewStore(t)
		_, err := fetchRefreshToken(context.Background(), st, "r", rtGrant(srv.URL), srv.Client())
		if !errors.Is(err, c.want) {
			t.Errorf("%s: err = %v, want %v", c.name, err, c.want)
			continue
		}
		if c.want == ErrNeedsReconsent && errors.Is(err, ErrCredentialRejected) {
			t.Errorf("%s: also credential rejected", c.name)
		}
		for _, v := range []string{rtOld, rtClient} {
			if strings.Contains(err.Error(), v) {
				t.Errorf("%s: error leaks credential", c.name)
			}
		}
		if st.sets != 0 {
			t.Errorf("%s: store written", c.name)
		}
	}
}

func TestFetchRefreshTokenIncompleteReply(t *testing.T) {
	for name, body := range map[string]string{
		"no expiry": `{"access_token":"at"}`,
		"no access": `{"expires_in":60}`,
	} {
		srv := rtServer(t, 200, body, nil)
		st := rtNewStore(t)
		_, err := fetchRefreshToken(context.Background(), st, "r", rtGrant(srv.URL), srv.Client())
		if err == nil || isSentinel(err) {
			t.Errorf("%s: err = %v, want plain error", name, err)
		}
		if st.sets != 0 {
			t.Errorf("%s: store written", name)
		}
	}
}

func TestFetchRefreshTokenNetworkError(t *testing.T) {
	srv := rtServer(t, 200, `{}`, nil)
	u := srv.URL
	srv.Close()
	_, err := fetchRefreshToken(context.Background(), rtNewStore(t), "r", rtGrant(u), nil)
	if !errors.Is(err, ErrTransient) {
		t.Errorf("err = %v", err)
	}
}

func TestFetchRefreshTokenNoRedirect(t *testing.T) {
	var hits atomic.Int32
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits.Add(1)
		w.WriteHeader(200)
	}))
	defer target.Close()
	for _, code := range []int{307, 308, 302} {
		first := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			http.Redirect(w, r, target.URL, code)
		}))
		_, err := fetchRefreshToken(context.Background(), rtNewStore(t), "r", rtGrant(first.URL), first.Client())
		first.Close()
		if err == nil || errors.Is(err, ErrTransient) || errors.Is(err, ErrCredentialRejected) || errors.Is(err, ErrNeedsReconsent) {
			t.Errorf("%d: err = %v, want plain error", code, err)
		}
	}
	if hits.Load() != 0 {
		t.Error("redirect target was contacted")
	}
}

func TestFetchRefreshTokenBadEnvelopeAndKind(t *testing.T) {
	srv := rtServer(t, 200, `{}`, nil)
	st := rtNewStore(t)
	_ = st.Store.Set(context.Background(), "rt", []byte(`{"v":1,"kind":"secret","value":"`+rtOld+`","x":1}`))
	_, err := fetchRefreshToken(context.Background(), st, "r", rtGrant(srv.URL), srv.Client())
	var ve *ValidationError
	if !errors.As(err, &ve) || strings.Contains(err.Error(), rtOld) {
		t.Errorf("err = %v", err)
	}
	g := rtGrant(srv.URL)
	g.Kind = ClientCredentials
	if _, err := fetchRefreshToken(context.Background(), rtNewStore(t), "r", g, srv.Client()); err == nil {
		t.Error("wrong kind accepted")
	}
}

func TestCheckRefreshWritableRefusals(t *testing.T) {
	stores := map[string]secret.Store{
		"memory":      memory.New(),
		"agefile":     agefile.New("/nonexistent/x.age", ""),
		"ghsecrets":   ghsecrets.New("o/r"),
		"onepassword": onepassword.NewCLI("v"),
		"op-connect":  onepassword.NewConnect("http://127.0.0.1:1", "t", "v"),
		"infisical":   infisical.New("http://127.0.0.1:1", "t", "p", "e"),
	}
	for name, s := range stores {
		err := checkRefreshWritable(s, name)
		var nw *NotWritableError
		if !errors.Is(err, ErrNotWritable) || !errors.As(err, &nw) || nw.Backend != name {
			t.Errorf("%s: err = %v", name, err)
		}
	}
	if err := checkRefreshWritable(rtNewStore(t), "ok"); err != nil {
		t.Errorf("writable store refused: %v", err)
	}
	// Refusal happens before any network or store read.
	_, err := fetchRefreshToken(context.Background(), memory.New(), "r", rtGrant("http://127.0.0.1:1"), nil)
	if !errors.Is(err, ErrNotWritable) {
		t.Errorf("fetch on memory: %v", err)
	}
}
