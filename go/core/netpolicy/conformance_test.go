package netpolicy

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"os"
	"path/filepath"
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
