package oauth

import (
	"context"
	"encoding/base64"
	"errors"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"hop.top/kit/go/storage/secret/memory"
)

const ccSecretValue = "cc-fake-secret-value"

func ccGrant(tokenURL string) Grant {
	return Grant{
		Kind:      ClientCredentials,
		TokenURL:  tokenURL,
		ClientID:  "app-client-id",
		Scopes:    []string{"https://graph.microsoft.com/.default"},
		SecretRef: "uma/t/secret",
	}
}

func ccStore(t *testing.T, envelope string) *memory.Store {
	t.Helper()
	st := memory.New()
	if envelope != "" {
		if err := st.Set(context.Background(), "uma/t/secret", []byte(envelope)); err != nil {
			t.Fatal(err)
		}
	}
	return st
}

func ccServer(t *testing.T, status int, body string, got *url.Values) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if err := r.ParseForm(); err != nil {
			t.Error(err)
		}
		if got != nil {
			*got = r.PostForm
		}
		if r.Header.Get("Authorization") != "" {
			t.Error("credentials must be in body, not Authorization header")
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(status)
		_, _ = w.Write([]byte(body))
	}))
	t.Cleanup(srv.Close)
	return srv
}

func TestFetchClientCredentialsSuccess(t *testing.T) {
	var form url.Values
	srv := ccServer(t, 200, `{"token_type":"Bearer","expires_in":3599,"access_token":"cc-fake-access"}`, &form)
	st := ccStore(t, `{"v":1,"kind":"secret","value":"`+ccSecretValue+`"}`)

	before := time.Now()
	tok, err := fetchClientCredentials(context.Background(), st, "uma/t/grant", ccGrant(srv.URL), srv.Client())
	if err != nil {
		t.Fatal(err)
	}
	for k, want := range map[string]string{
		"grant_type":    "client_credentials",
		"client_id":     "app-client-id",
		"client_secret": ccSecretValue,
		"scope":         "https://graph.microsoft.com/.default",
	} {
		if form.Get(k) != want {
			t.Errorf("form %s = %q, want %q", k, form.Get(k), want)
		}
	}
	if tok.AccessToken != "cc-fake-access" {
		t.Errorf("access token = %q", tok.AccessToken)
	}
	d := tok.ExpiresAt.Sub(before)
	if d < 3590*time.Second || d > 3600*time.Second+5*time.Second {
		t.Errorf("expiry %v from now, want ~3599s", d)
	}
	if err := tok.Validate(); err != nil {
		t.Error(err)
	}
}

func TestFetchClientCredentialsBase64Secret(t *testing.T) {
	var form url.Values
	srv := ccServer(t, 200, `{"expires_in":60,"access_token":"a"}`, &form)
	enc := base64.StdEncoding.EncodeToString([]byte(ccSecretValue))
	st := ccStore(t, `{"v":1,"kind":"secret","value_base64":"`+enc+`"}`)
	if _, err := fetchClientCredentials(context.Background(), st, "r", ccGrant(srv.URL), srv.Client()); err != nil {
		t.Fatal(err)
	}
	if form.Get("client_secret") != ccSecretValue {
		t.Error("client_secret not decoded from value_base64")
	}
}

func TestFetchClientCredentialsErrors(t *testing.T) {
	cases := []struct {
		name   string
		status int
		body   string
		want   error
	}{
		{"invalid_client", 401, `{"error":"invalid_client","error_description":"echo ` + ccSecretValue + `"}`, ErrCredentialRejected},
		{"invalid_grant", 400, `{"error":"invalid_grant"}`, ErrCredentialRejected},
		{"unauthorized_client", 400, `{"error":"unauthorized_client"}`, ErrCredentialRejected},
		{"500", 500, `oops ` + ccSecretValue, ErrTransient},
		{"503", 503, ``, ErrTransient},
		{"429", 429, ``, ErrTransient},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			srv := ccServer(t, c.status, c.body, nil)
			st := ccStore(t, `{"v":1,"kind":"secret","value":"`+ccSecretValue+`"}`)
			_, err := fetchClientCredentials(context.Background(), st, "r", ccGrant(srv.URL), srv.Client())
			if !errors.Is(err, c.want) {
				t.Fatalf("err = %v, want %v", err, c.want)
			}
			if strings.Contains(err.Error(), ccSecretValue) {
				t.Errorf("error leaks credential: %v", err)
			}
		})
	}
}

func TestFetchClientCredentialsNetworkError(t *testing.T) {
	srv := ccServer(t, 200, `{}`, nil)
	url := srv.URL
	srv.Close()
	st := ccStore(t, `{"v":1,"kind":"secret","value":"`+ccSecretValue+`"}`)
	_, err := fetchClientCredentials(context.Background(), st, "r", ccGrant(url), http.DefaultClient)
	if !errors.Is(err, ErrTransient) {
		t.Fatalf("err = %v, want ErrTransient", err)
	}
	if strings.Contains(err.Error(), ccSecretValue) {
		t.Error("leak")
	}
}

func TestFetchClientCredentialsBadSecretEnvelope(t *testing.T) {
	srv := ccServer(t, 200, `{"expires_in":60,"access_token":"a"}`, nil)
	cases := map[string]struct{ env, field string }{
		"missing":       {"", "secret_ref"},
		"not json":      {"garbage-" + ccSecretValue, ""},
		"wrong kind":    {`{"v":1,"kind":"oauth2","value":"` + ccSecretValue + `"}`, "kind"},
		"wrong v":       {`{"v":2,"kind":"secret","value":"` + ccSecretValue + `"}`, "v"},
		"no value":      {`{"v":1,"kind":"secret"}`, "value"},
		"both values":   {`{"v":1,"kind":"secret","value":"a","value_base64":"YQ=="}`, "value"},
		"bad base64":    {`{"v":1,"kind":"secret","value_base64":"!!` + ccSecretValue + `"}`, "value_base64"},
		"empty value":   {`{"v":1,"kind":"secret","value":""}`, "value"},
		"wrong type":    {`{"v":1,"kind":"secret","value":42}`, "value"},
		"unknown field": {`{"v":1,"kind":"secret","value":"` + ccSecretValue + `","x":1}`, ""},
	}
	for name, c := range cases {
		t.Run(name, func(t *testing.T) {
			st := ccStore(t, c.env)
			_, err := fetchClientCredentials(context.Background(), st, "r", ccGrant(srv.URL), srv.Client())
			if err == nil {
				t.Fatal("want error")
			}
			if strings.Contains(err.Error(), ccSecretValue) {
				t.Errorf("error leaks secret: %v", err)
			}
			if c.field != "" && !strings.Contains(err.Error(), c.field) {
				t.Errorf("error %q does not name %q", err, c.field)
			}
		})
	}
}

func TestFetchClientCredentialsMissingExpiry(t *testing.T) {
	srv := ccServer(t, 200, `{"access_token":"a"}`, nil)
	st := ccStore(t, `{"v":1,"kind":"secret","value":"`+ccSecretValue+`"}`)
	_, err := fetchClientCredentials(context.Background(), st, "r", ccGrant(srv.URL), srv.Client())
	if err == nil || isSentinel(err) {
		t.Fatalf("err = %v, want plain error for missing expires_in", err)
	}
}
