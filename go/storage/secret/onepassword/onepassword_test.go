package onepassword

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"testing"

	"hop.top/kit/go/storage/secret"
)

// --- CLI mode tests ---

func TestCLIGet(t *testing.T) {
	fake := fakeBinary(t, `{"value":"s3cret"}`)
	t.Setenv("PATH", filepath.Dir(fake))

	store := NewCLI("my-vault")
	s, err := store.Get(context.Background(), "db-pass")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if string(s.Value) != "s3cret" {
		t.Fatalf("got %q, want %q", s.Value, "s3cret")
	}
}

func TestCLIGetNotFound(t *testing.T) {
	fake := fakeBinary(t, "")
	// Make it exit non-zero
	os.WriteFile(fake, exitScript(1), 0o755)
	t.Setenv("PATH", filepath.Dir(fake))

	store := NewCLI("my-vault")
	_, err := store.Get(context.Background(), "missing")
	if err != secret.ErrNotFound {
		t.Fatalf("got %v, want ErrNotFound", err)
	}
}

func TestCLIList(t *testing.T) {
	items := []struct {
		Title string `json:"title"`
	}{
		{"db-password"},
		{"db-user"},
		{"api-key"},
	}
	data, _ := json.Marshal(items)
	fake := fakeBinary(t, string(data))
	t.Setenv("PATH", filepath.Dir(fake))

	store := NewCLI("v")
	got, err := store.List(context.Background(), "db-")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("got %d items, want 2", len(got))
	}
}

// --- Connect mode tests ---

func TestConnectGet(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer test-token" {
			t.Error("missing auth header")
		}
		json.NewEncoder(w).Encode([]connectItem{{
			ID:    "item-1",
			Title: "db-password",
			Fields: []struct {
				Label string `json:"label"`
				Value string `json:"value"`
			}{
				{Label: "password", Value: "secret-value"},
			},
		}})
	}))
	defer srv.Close()

	store := NewConnect(srv.URL, "test-token", "test-vault")
	s, err := store.Get(context.Background(), "db-password")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if string(s.Value) != "secret-value" {
		t.Fatalf("got %q, want %q", s.Value, "secret-value")
	}
}

func TestConnectGetNotFound(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		json.NewEncoder(w).Encode([]connectItem{})
	}))
	defer srv.Close()

	store := NewConnect(srv.URL, "tok", "v")
	_, err := store.Get(context.Background(), "nope")
	if err != secret.ErrNotFound {
		t.Fatalf("got %v, want ErrNotFound", err)
	}
}

func TestConnectList(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		json.NewEncoder(w).Encode([]connectItem{
			{ID: "1", Title: "app-secret"},
			{ID: "2", Title: "app-key"},
			{ID: "3", Title: "other"},
		})
	}))
	defer srv.Close()

	store := NewConnect(srv.URL, "tok", "v")
	got, err := store.List(context.Background(), "app-")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("got %d, want 2", len(got))
	}
}

func TestConnectSet(t *testing.T) {
	var called bool
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodPost {
			called = true
			w.WriteHeader(http.StatusCreated)
		}
	}))
	defer srv.Close()

	store := NewConnect(srv.URL, "tok", "v")
	err := store.Set(context.Background(), "new-key", []byte("val"))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !called {
		t.Fatal("POST not called")
	}
}

func TestConnectDelete(t *testing.T) {
	var deleted bool
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.Method {
		case http.MethodGet:
			json.NewEncoder(w).Encode([]connectItem{{ID: "item-99", Title: "kill-me"}})
		case http.MethodDelete:
			deleted = true
			w.WriteHeader(http.StatusNoContent)
		}
	}))
	defer srv.Close()

	store := NewConnect(srv.URL, "tok", "v")
	err := store.Delete(context.Background(), "kill-me")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !deleted {
		t.Fatal("DELETE not called")
	}
}

func TestCLISetReturnsNotSupported(t *testing.T) {
	store := NewCLI("v")
	err := store.Set(context.Background(), "k", []byte("v"))
	if err != secret.ErrNotSupported {
		t.Fatalf("got %v, want ErrNotSupported", err)
	}
}

func TestCLIGetExecError(t *testing.T) {
	// Exit code 2 = not a "not found", should propagate real error
	fake := fakeBinary(t, "")
	os.WriteFile(fake, exitScript(2), 0o755)
	t.Setenv("PATH", filepath.Dir(fake))

	store := NewCLI("my-vault")
	_, err := store.Get(context.Background(), "key")
	if err == nil {
		t.Fatal("expected error")
	}
	if errors.Is(err, secret.ErrNotFound) {
		t.Fatal("exit code 2 should NOT return ErrNotFound")
	}
}

func TestCLIGetBinaryMissing(t *testing.T) {
	t.Setenv("PATH", t.TempDir()) // empty dir — no op binary

	store := NewCLI("v")
	_, err := store.Get(context.Background(), "key")
	if err == nil {
		t.Fatal("expected error")
	}
	if errors.Is(err, secret.ErrNotFound) {
		t.Fatal("missing binary should NOT return ErrNotFound")
	}
}

func TestConnectGetServerError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer srv.Close()

	store := NewConnect(srv.URL, "tok", "vault")
	_, err := store.Get(context.Background(), "key")
	if err == nil {
		t.Fatal("expected error")
	}
	if errors.Is(err, secret.ErrNotFound) {
		t.Fatal("500 should NOT return ErrNotFound")
	}
}

// --- regression tests ---

func TestConnectSetSpecialChars(t *testing.T) {
	// Values with quotes, backslashes, newlines must survive JSON encoding.
	cases := []struct {
		key, value string
	}{
		{"has-quotes", `pass"word`},
		{"has-backslash", `c:\users\admin`},
		{"has-newline", "line1\nline2"},
		{"has-tab", "col1\tcol2"},
		{"has-unicode", "p@$$w🔑rd"},
		{"has-control-chars", string([]byte{0x01, 0x1f, 0x7f})},
	}

	for _, tc := range cases {
		t.Run(tc.key, func(t *testing.T) {
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				var payload struct {
					Title  string `json:"title"`
					Fields []struct {
						Label string `json:"label"`
						Value string `json:"value"`
					} `json:"fields"`
				}
				if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
					t.Errorf("invalid JSON body: %v", err)
					w.WriteHeader(http.StatusBadRequest)
					return
				}
				if payload.Title != tc.key {
					t.Errorf("title: got %q, want %q", payload.Title, tc.key)
				}
				if len(payload.Fields) == 0 {
					t.Error("expected at least one field, got none")
					w.WriteHeader(http.StatusBadRequest)
					return
				}
				if len(payload.Fields) != 1 || payload.Fields[0].Value != tc.value {
					t.Errorf("value: got %q, want %q", payload.Fields[0].Value, tc.value)
				}
				w.WriteHeader(http.StatusCreated)
			}))
			defer srv.Close()

			store := NewConnect(srv.URL, "tok", "v")
			if err := store.Set(context.Background(), tc.key, []byte(tc.value)); err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
		})
	}
}

func TestCLIGetFlagLikeKey(t *testing.T) {
	// Key that looks like a flag should be passed after "--" separator
	// and not interpreted as a CLI flag.
	fake := fakeBinary(t, `{"value":"safe"}`)
	t.Setenv("PATH", filepath.Dir(fake))

	store := NewCLI("my-vault")

	// These keys contain flag-like patterns; they should not cause
	// "unknown flag" errors because the implementation uses "--" separator.
	keys := []string{"--vault=attacker", "-v", "--format=csv"}
	for _, key := range keys {
		_, err := store.Get(context.Background(), key)
		// Should either succeed (fake binary returns result) or return
		// a clean not-found — never an "unknown flag" parse error.
		if err != nil && err != secret.ErrNotFound {
			t.Errorf("key %q: unexpected error type: %v", key, err)
		}
	}
}

// --- helpers ---

func fakeBinary(t *testing.T, output string) string {
	t.Helper()
	dir := t.TempDir()
	path := filepath.Join(dir, "op")
	os.WriteFile(path, outputScript(output), 0o755)
	return path
}

func outputScript(output string) []byte {
	if runtime.GOOS == "windows" {
		return []byte("@echo off\necho " + output)
	}
	return []byte("#!/bin/sh\nprintf '%s' '" + output + "'\n")
}

func exitScript(code int) []byte {
	if runtime.GOOS == "windows" {
		return []byte("@echo off\nexit /b " + string(rune('0'+code)))
	}
	return []byte("#!/bin/sh\nexit " + string(rune('0'+code)) + "\n")
}
