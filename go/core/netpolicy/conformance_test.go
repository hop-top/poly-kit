package netpolicy

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
)

// Runs the shared corpus in contracts/netpolicy-v1. The vectors are the
// source of truth: a failure here means this implementation drifted
// from the contract, not that the vector should be edited to fit.

const netpolicyConfDir = "../../../contracts/netpolicy-v1"

// netpolicyConfFiles is every vector file this runner consumes; the
// corpus directory must match it exactly.
var netpolicyConfFiles = map[string]bool{"vectors.json": true}

type netpolicyConfFile struct {
	Schema         string `json:"$schema"`
	ID             string `json:"$id"`
	Version        string `json:"version"`
	Description    string `json:"description"`
	OfflineMessage string `json:"offline_message"`
	IsLoopback     []struct {
		Name     string `json:"name"`
		Host     string `json:"host"`
		Loopback bool   `json:"loopback"`
	} `json:"is_loopback"`
	CheckDial []struct {
		Name    string `json:"name"`
		Offline bool   `json:"offline"`
		Network string `json:"network"`
		Addr    string `json:"addr"`
		Error   string `json:"error"`
	} `json:"check_dial"`
	URLAllows []struct {
		Name    string `json:"name"`
		URL     string `json:"url"`
		Allowed bool   `json:"allowed"`
	} `json:"url_allows"`
	Refusal []struct {
		Name   string `json:"name"`
		Method string `json:"method"`
		URL    string `json:"url"`
		Error  string `json:"error"`
	} `json:"refusal"`
	AllowMessage string `json:"allow_message"`
	HostAllowed  []struct {
		Name       string   `json:"name"`
		AllowHosts []string `json:"allow_hosts"`
		Host       string   `json:"host"`
		Allowed    bool     `json:"allowed"`
	} `json:"host_allowed"`
	AllowCheckDial []struct {
		Name       string   `json:"name"`
		AllowHosts []string `json:"allow_hosts"`
		Network    string   `json:"network"`
		Addr       string   `json:"addr"`
		Error      string   `json:"error"`
	} `json:"allow_check_dial"`
	AllowURL []struct {
		Name       string   `json:"name"`
		AllowHosts []string `json:"allow_hosts"`
		Method     string   `json:"method"`
		URL        string   `json:"url"`
		Error      string   `json:"error"`
	} `json:"allow_url"`
	RedirectMessage string `json:"redirect_message"`
	RedirectHeaders []struct {
		Name        string            `json:"name"`
		CrossOrigin bool              `json:"cross_origin"`
		Headers     map[string]string `json:"headers"`
		Refused     bool              `json:"refused"`
	} `json:"redirect_headers"`
}

func loadNetpolicyConf(t *testing.T) netpolicyConfFile {
	t.Helper()
	dir := filepath.FromSlash(netpolicyConfDir)
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range entries {
		if filepath.Ext(e.Name()) == ".json" && !netpolicyConfFiles[e.Name()] {
			t.Errorf("%s has no runner", e.Name())
		}
	}
	raw, err := os.ReadFile(filepath.Join(dir, "vectors.json"))
	if err != nil {
		t.Fatal(err)
	}
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.DisallowUnknownFields()
	var f netpolicyConfFile
	if err := dec.Decode(&f); err != nil {
		t.Fatalf("parse vectors.json: %v", err)
	}
	if f.Version != "v1" {
		t.Fatalf("version = %q, want v1", f.Version)
	}
	return f
}

func TestConformanceNetpolicyOfflineMessage(t *testing.T) {
	if got, want := ErrOffline.Error(), loadNetpolicyConf(t).OfflineMessage; got != want {
		t.Errorf("ErrOffline = %q, want %q", got, want)
	}
}

func TestConformanceNetpolicyIsLoopback(t *testing.T) {
	cases := loadNetpolicyConf(t).IsLoopback
	if len(cases) == 0 {
		t.Fatal("is_loopback section is empty")
	}
	for _, c := range cases {
		if got := isLoopback(c.Host); got != c.Loopback {
			t.Errorf("%s: isLoopback(%q) = %v, want %v", c.Name, c.Host, got, c.Loopback)
		}
	}
}

func TestConformanceNetpolicyCheckDial(t *testing.T) {
	cases := loadNetpolicyConf(t).CheckDial
	if len(cases) == 0 {
		t.Fatal("check_dial section is empty")
	}
	for _, c := range cases {
		err := CheckDial(WithOffline(t.Context(), c.Offline), c.Network, c.Addr)
		switch {
		case c.Error == "" && err != nil:
			t.Errorf("%s: CheckDial(%s, %s) = %v, want nil", c.Name, c.Network, c.Addr, err)
		case c.Error != "" && !errors.Is(err, ErrOffline):
			t.Errorf("%s: CheckDial(%s, %s) = %v, want ErrOffline", c.Name, c.Network, c.Addr, err)
		case c.Error != "" && err.Error() != c.Error:
			t.Errorf("%s: CheckDial(%s, %s) = %q, want %q", c.Name, c.Network, c.Addr, err.Error(), c.Error)
		}
	}
}

// confRecorder records whether the wrapped transport was reached.
type confRecorder struct{ reached bool }

func (r *confRecorder) RoundTrip(*http.Request) (*http.Response, error) {
	r.reached = true
	return &http.Response{StatusCode: http.StatusOK, Body: http.NoBody}, nil
}

// offlineTrip sends method url through Guard on an offline context.
func offlineTrip(ctx context.Context, t *testing.T, method, url string) (bool, error) {
	t.Helper()
	req, err := http.NewRequestWithContext(WithOffline(ctx, true), method, url, nil)
	if err != nil {
		t.Fatalf("%s %s: %v", method, url, err)
	}
	rec := &confRecorder{}
	_, err = Guard(rec).RoundTrip(req)
	return rec.reached, err
}

func TestConformanceNetpolicyURLAllows(t *testing.T) {
	cases := loadNetpolicyConf(t).URLAllows
	if len(cases) == 0 {
		t.Fatal("url_allows section is empty")
	}
	for _, c := range cases {
		reached, err := offlineTrip(t.Context(), t, http.MethodGet, c.URL)
		if c.Allowed && (err != nil || !reached) {
			t.Errorf("%s: %s refused: %v", c.Name, c.URL, err)
		}
		if !c.Allowed && (!errors.Is(err, ErrOffline) || reached) {
			t.Errorf("%s: %s allowed: reached=%v err=%v", c.Name, c.URL, reached, err)
		}
	}
}

func TestConformanceNetpolicyRefusal(t *testing.T) {
	cases := loadNetpolicyConf(t).Refusal
	if len(cases) == 0 {
		t.Fatal("refusal section is empty")
	}
	for _, c := range cases {
		_, err := offlineTrip(t.Context(), t, c.Method, c.URL)
		if err == nil || err.Error() != c.Error {
			t.Errorf("%s: refusal = %v, want %q", c.Name, err, c.Error)
		}
	}
}

func TestConformanceNetpolicyAllowMessage(t *testing.T) {
	if got, want := ErrHostNotAllowed.Error(), loadNetpolicyConf(t).AllowMessage; got != want {
		t.Errorf("ErrHostNotAllowed = %q, want %q", got, want)
	}
}

func TestConformanceNetpolicyHostAllowed(t *testing.T) {
	cases := loadNetpolicyConf(t).HostAllowed
	if len(cases) == 0 {
		t.Fatal("host_allowed section is empty")
	}
	for _, c := range cases {
		ctx := WithAllowHosts(t.Context(), c.AllowHosts)
		if got := HostAllowed(ctx, c.Host); got != c.Allowed {
			t.Errorf("%s: HostAllowed(%v, %q) = %v, want %v", c.Name, c.AllowHosts, c.Host, got, c.Allowed)
		}
	}
}

func TestConformanceNetpolicyAllowCheckDial(t *testing.T) {
	cases := loadNetpolicyConf(t).AllowCheckDial
	if len(cases) == 0 {
		t.Fatal("allow_check_dial section is empty")
	}
	for _, c := range cases {
		err := CheckDial(WithAllowHosts(t.Context(), c.AllowHosts), c.Network, c.Addr)
		switch {
		case c.Error == "" && err != nil:
			t.Errorf("%s: CheckDial = %v, want nil", c.Name, err)
		case c.Error != "" && !errors.Is(err, ErrHostNotAllowed):
			t.Errorf("%s: CheckDial = %v, want ErrHostNotAllowed", c.Name, err)
		case c.Error != "" && err.Error() != c.Error:
			t.Errorf("%s: CheckDial = %q, want %q", c.Name, err.Error(), c.Error)
		}
	}
}

func TestConformanceNetpolicyAllowURL(t *testing.T) {
	cases := loadNetpolicyConf(t).AllowURL
	if len(cases) == 0 {
		t.Fatal("allow_url section is empty")
	}
	for _, c := range cases {
		req, err := http.NewRequestWithContext(WithAllowHosts(t.Context(), c.AllowHosts), c.Method, c.URL, nil)
		if err != nil {
			t.Fatalf("%s: %v", c.Name, err)
		}
		rec := &confRecorder{}
		_, err = Guard(rec).RoundTrip(req)
		switch {
		case c.Error == "" && (err != nil || !rec.reached):
			t.Errorf("%s: refused: %v", c.Name, err)
		case c.Error != "" && (!errors.Is(err, ErrHostNotAllowed) || rec.reached):
			t.Errorf("%s: err = %v reached=%v, want ErrHostNotAllowed", c.Name, err, rec.reached)
		case c.Error != "" && err.Error() != c.Error:
			t.Errorf("%s: err = %q, want %q", c.Name, err.Error(), c.Error)
		}
	}
}

func TestConformanceNetpolicyRedirectHeaders(t *testing.T) {
	f := loadNetpolicyConf(t)
	if len(f.RedirectHeaders) == 0 {
		t.Fatal("redirect_headers section is empty")
	}
	if got := ErrCrossOriginHeaders.Error(); got != f.RedirectMessage {
		t.Errorf("ErrCrossOriginHeaders = %q, want %q", got, f.RedirectMessage)
	}
	for _, c := range f.RedirectHeaders {
		t.Run(c.Name, func(t *testing.T) {
			var mu sync.Mutex
			var got []http.Header
			final := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				mu.Lock()
				got = append(got, r.Header.Clone())
				mu.Unlock()
			})
			b := httptest.NewServer(final)
			defer b.Close()
			var a *httptest.Server
			a = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path == "/final" {
					final(w, r)
					return
				}
				to := a.URL + "/final"
				if c.CrossOrigin {
					to = b.URL + "/final"
				}
				http.Redirect(w, r, to, http.StatusFound)
			}))
			defer a.Close()

			var refusals []Refusal
			ctx := WithAudit(t.Context(), func(r Refusal) { refusals = append(refusals, r) })
			req, err := http.NewRequestWithContext(ctx, http.MethodGet, a.URL+"/hop", nil)
			if err != nil {
				t.Fatal(err)
			}
			for k, v := range c.Headers {
				req.Header.Set(k, v)
			}
			resp, err := NewClient(nil).Do(req)
			if resp != nil {
				resp.Body.Close()
			}
			if c.Refused {
				var re *RedirectError
				if !errors.As(err, &re) || !errors.Is(err, ErrCrossOriginHeaders) {
					t.Fatalf("err = %v, want *RedirectError", err)
				}
				if !strings.HasSuffix(re.Error(), f.RedirectMessage) {
					t.Errorf("error %q does not end with %q", re.Error(), f.RedirectMessage)
				}
				for _, v := range c.Headers {
					if strings.Contains(err.Error(), v) {
						t.Errorf("error leaks header value %q: %v", v, err)
					}
				}
				if len(refusals) != 1 || refusals[0].Op != "redirect" || !errors.Is(refusals[0].Reason, ErrCrossOriginHeaders) {
					t.Errorf("audit = %+v, want one redirect refusal", refusals)
				}
				mu.Lock()
				defer mu.Unlock()
				if len(got) != 0 {
					t.Errorf("target heard %d requests, want 0", len(got))
				}
				return
			}
			if err != nil {
				t.Fatalf("hop refused: %v", err)
			}
			mu.Lock()
			defer mu.Unlock()
			if len(got) != 1 {
				t.Fatalf("target heard %d requests, want 1", len(got))
			}
			for k, v := range c.Headers {
				if http.CanonicalHeaderKey(k) == "Content-Type" {
					continue // allowed, but net/http drops it on a bodiless hop
				}
				if g := got[0].Get(k); g != v {
					t.Errorf("target header %s = %q, want %q", k, g, v)
				}
			}
			if len(refusals) != 0 {
				t.Errorf("audit = %+v, want none", refusals)
			}
		})
	}
}
