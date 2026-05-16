package infisical_test

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"hop.top/kit/go/storage/secret/infisical"
)

// TestRegression_JSONPayloadSpecialChars verifies json.Marshal correctly
// encodes values with newlines, tabs, quotes, backslashes, and unicode.
func TestRegression_JSONPayloadSpecialChars(t *testing.T) {
	cases := []struct {
		name string
		val  string
	}{
		{"newline", "line1\nline2\nline3"},
		{"tab", "col1\tcol2\tcol3"},
		{"quotes", `say "hello" and 'goodbye'`},
		{"backslash", `C:\Users\test\path`},
		{"unicode_bmp", "\u00e9\u00e0\u00fc\u2603"},
		{"unicode_astral", "\U0001F600\U0001F4A9\U0001F680"},
		{"mixed_special", "a=\"b\"\nc=\\d\t\U0001F4A9"},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var captured []byte

			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				body, err := io.ReadAll(r.Body)
				if err != nil {
					http.Error(w, err.Error(), 500)
					return
				}
				captured = body
				w.WriteHeader(http.StatusOK)
			}))
			t.Cleanup(srv.Close)

			store := infisical.New(srv.URL, "tok", "proj1", "dev")
			store.SetClient(srv.Client())

			err := store.Set(context.Background(), "K", []byte(tc.val))
			if err != nil {
				t.Fatalf("Set: %v", err)
			}

			// raw body must be valid JSON
			var parsed struct {
				WorkspaceID string `json:"workspaceId"`
				Environment string `json:"environment"`
				SecretValue string `json:"secretValue"`
			}
			if err := json.Unmarshal(captured, &parsed); err != nil {
				t.Fatalf("Unmarshal captured body: %v\nbody: %s", err, captured)
			}
			if parsed.SecretValue != tc.val {
				t.Errorf("round-trip mismatch:\n  got:  %q\n  want: %q", parsed.SecretValue, tc.val)
			}
			if parsed.WorkspaceID != "proj1" {
				t.Errorf("workspaceId = %q, want %q", parsed.WorkspaceID, "proj1")
			}
			if parsed.Environment != "dev" {
				t.Errorf("environment = %q, want %q", parsed.Environment, "dev")
			}
		})
	}
}

// TestRegression_URLPathEscaping verifies url.PathEscape for keys with
// reserved URI characters across Set, Get, and Delete.
func TestRegression_URLPathEscaping(t *testing.T) {
	keys := []string{
		"ns/key",
		"search?q=1",
		"anchor#ref",
		"percent%20encoded",
		"all/of?these#chars%here",
	}

	for _, key := range keys {
		t.Run(key, func(t *testing.T) {
			var paths []string

			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				paths = append(paths, r.URL.RawPath)

				switch r.Method {
				case http.MethodGet:
					json.NewEncoder(w).Encode(map[string]any{
						"secret": map[string]string{
							"secretKey":   key,
							"secretValue": "v",
						},
					})
				case http.MethodPost:
					w.WriteHeader(http.StatusOK)
				case http.MethodDelete:
					w.WriteHeader(http.StatusOK)
				}
			}))
			t.Cleanup(srv.Close)

			store := infisical.New(srv.URL, "tok", "proj1", "dev")
			store.SetClient(srv.Client())
			ctx := context.Background()

			if err := store.Set(ctx, key, []byte("v")); err != nil {
				t.Fatalf("Set(%q): %v", key, err)
			}
			if _, err := store.Get(ctx, key); err != nil {
				t.Fatalf("Get(%q): %v", key, err)
			}
			if err := store.Delete(ctx, key); err != nil {
				t.Fatalf("Delete(%q): %v", key, err)
			}

			// none of the raw paths should contain the unescaped key literally
			// (unless key has no special chars, which these all do)
			for i, p := range paths {
				if p == "" {
					// RawPath empty means Go decoded it; check r.URL.Path wouldn't
					// have matched a different route
					continue
				}
				if strings.Contains(p, key) {
					t.Errorf("request %d raw path %q contains unescaped key %q", i, p, key)
				}
			}
		})
	}
}

// TestRegression_DeletePayload verifies Delete sends workspaceId and
// environment but NOT secretValue.
func TestRegression_DeletePayload(t *testing.T) {
	var captured []byte

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, err := io.ReadAll(r.Body)
		if err != nil {
			http.Error(w, err.Error(), 500)
			return
		}
		captured = body
		w.WriteHeader(http.StatusOK)
	}))
	t.Cleanup(srv.Close)

	store := infisical.New(srv.URL, "tok", "proj1", "staging")
	store.SetClient(srv.Client())

	if err := store.Delete(context.Background(), "SOME_KEY"); err != nil {
		t.Fatalf("Delete: %v", err)
	}

	var raw map[string]any
	if err := json.Unmarshal(captured, &raw); err != nil {
		t.Fatalf("Unmarshal: %v\nbody: %s", err, captured)
	}

	if raw["workspaceId"] != "proj1" {
		t.Errorf("workspaceId = %v, want %q", raw["workspaceId"], "proj1")
	}
	if raw["environment"] != "staging" {
		t.Errorf("environment = %v, want %q", raw["environment"], "staging")
	}
	if _, exists := raw["secretValue"]; exists {
		t.Errorf("delete payload should not contain secretValue, got %v", raw["secretValue"])
	}
}

// TestRegression_BinarySafeValues verifies null bytes and control characters
// produce valid JSON via json.Marshal.
func TestRegression_BinarySafeValues(t *testing.T) {
	cases := []struct {
		name string
		val  []byte
	}{
		{"null_byte", []byte("before\x00after")},
		{"control_chars", []byte("\x01\x02\x03\x04\x05")},
		{"mixed_null_and_printable", []byte("hello\x00world\x00!")},
		{"bell_and_backspace", []byte("a\x07b\x08c")},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var captured []byte

			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				body, err := io.ReadAll(r.Body)
				if err != nil {
					http.Error(w, err.Error(), 500)
					return
				}
				captured = body
				w.WriteHeader(http.StatusOK)
			}))
			t.Cleanup(srv.Close)

			store := infisical.New(srv.URL, "tok", "proj1", "dev")
			store.SetClient(srv.Client())

			err := store.Set(context.Background(), "K", tc.val)
			if err != nil {
				t.Fatalf("Set: %v", err)
			}

			// body must be valid JSON
			var parsed struct {
				SecretValue string `json:"secretValue"`
			}
			if err := json.Unmarshal(captured, &parsed); err != nil {
				t.Fatalf("invalid JSON from json.Marshal: %v\nbody: %s", err, captured)
			}

			if parsed.SecretValue != string(tc.val) {
				t.Errorf("round-trip mismatch:\n  got:  %q\n  want: %q", parsed.SecretValue, tc.val)
			}
		})
	}
}
