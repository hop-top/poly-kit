package infisical_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"hop.top/kit/go/storage/secret"
	"hop.top/kit/go/storage/secret/infisical"
)

func newTestServer(t *testing.T) (*httptest.Server, map[string]string) {
	t.Helper()
	data := map[string]string{}

	mux := http.NewServeMux()

	// GET single secret
	mux.HandleFunc("GET /api/v3/secrets/raw/{key}", func(w http.ResponseWriter, r *http.Request) {
		key := r.PathValue("key")
		v, ok := data[key]
		if !ok {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		json.NewEncoder(w).Encode(map[string]any{
			"secret": map[string]string{
				"secretKey":   key,
				"secretValue": v,
			},
		})
	})

	// GET list
	mux.HandleFunc("GET /api/v3/secrets/raw", func(w http.ResponseWriter, r *http.Request) {
		var secrets []map[string]string
		for k, v := range data {
			secrets = append(secrets, map[string]string{
				"secretKey":   k,
				"secretValue": v,
			})
		}
		json.NewEncoder(w).Encode(map[string]any{"secrets": secrets})
	})

	// POST set
	mux.HandleFunc("POST /api/v3/secrets/raw/{key}", func(w http.ResponseWriter, r *http.Request) {
		key := r.PathValue("key")
		var body struct {
			SecretValue string `json:"secretValue"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			http.Error(w, "bad request: "+err.Error(), http.StatusBadRequest)
			return
		}
		data[key] = body.SecretValue
		w.WriteHeader(http.StatusOK)
	})

	// DELETE
	mux.HandleFunc("DELETE /api/v3/secrets/raw/{key}", func(w http.ResponseWriter, r *http.Request) {
		key := r.PathValue("key")
		if _, ok := data[key]; !ok {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		delete(data, key)
		w.WriteHeader(http.StatusOK)
	})

	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	return srv, data
}

func TestGet(t *testing.T) {
	srv, data := newTestServer(t)
	data["DB_PASS"] = "hunter2"

	store := infisical.New(srv.URL, "tok", "proj1", "dev")
	store.SetClient(srv.Client())

	ctx := context.Background()
	s, err := store.Get(ctx, "DB_PASS")
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if string(s.Value) != "hunter2" {
		t.Fatalf("got %q, want %q", s.Value, "hunter2")
	}
}

func TestGetNotFound(t *testing.T) {
	srv, _ := newTestServer(t)
	store := infisical.New(srv.URL, "tok", "proj1", "dev")
	store.SetClient(srv.Client())

	_, err := store.Get(context.Background(), "NOPE")
	if err != secret.ErrNotFound {
		t.Fatalf("expected ErrNotFound, got %v", err)
	}
}

func TestSet(t *testing.T) {
	srv, data := newTestServer(t)
	store := infisical.New(srv.URL, "tok", "proj1", "dev")
	store.SetClient(srv.Client())

	ctx := context.Background()
	if err := store.Set(ctx, "NEW_KEY", []byte("val123")); err != nil {
		t.Fatalf("Set: %v", err)
	}
	if data["NEW_KEY"] != "val123" {
		t.Fatalf("server data: got %q", data["NEW_KEY"])
	}
}

func TestDelete(t *testing.T) {
	srv, data := newTestServer(t)
	data["TO_DEL"] = "bye"

	store := infisical.New(srv.URL, "tok", "proj1", "dev")
	store.SetClient(srv.Client())

	ctx := context.Background()
	if err := store.Delete(ctx, "TO_DEL"); err != nil {
		t.Fatalf("Delete: %v", err)
	}
	if _, ok := data["TO_DEL"]; ok {
		t.Fatal("expected key deleted from server")
	}
}

func TestDeleteNotFound(t *testing.T) {
	srv, _ := newTestServer(t)
	store := infisical.New(srv.URL, "tok", "proj1", "dev")
	store.SetClient(srv.Client())

	err := store.Delete(context.Background(), "NOPE")
	if err != secret.ErrNotFound {
		t.Fatalf("expected ErrNotFound, got %v", err)
	}
}

func TestList(t *testing.T) {
	srv, data := newTestServer(t)
	data["APP_ONE"] = "1"
	data["APP_TWO"] = "2"
	data["OTHER"] = "3"

	store := infisical.New(srv.URL, "tok", "proj1", "dev")
	store.SetClient(srv.Client())

	keys, err := store.List(context.Background(), "APP_")
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(keys) != 2 {
		t.Fatalf("got %d keys, want 2: %v", len(keys), keys)
	}
}

func TestSetSpecialChars_regressions(t *testing.T) {
	srv, data := newTestServer(t)
	store := infisical.New(srv.URL, "tok", "proj1", "dev")
	store.SetClient(srv.Client())

	cases := []struct {
		name string
		val  string
		want string // if empty, expect val unchanged
	}{
		{"newline", "line1\nline2", ""},
		{"tab", "col1\tcol2", ""},
		{"quotes", `say "hello"`, ""},
		{"backslash", `C:\Users\test`, ""},
		{"unicode", "\u00e9\u00e0\u00fc\U0001F600", ""},
		{"mixed", "a=\"b\"\nc=\\d\t\U0001F4A9", ""},
		{"ansi_escape", "\x1b[31mred\x1b[0m", ""},
		// invalid UTF-8 is replaced with U+FFFD per JSON spec
		{"invalid_utf8", string([]byte{0x80, 0x81}), "\ufffd\ufffd"},
	}

	ctx := context.Background()
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			key := "SPECIAL_" + tc.name
			if err := store.Set(ctx, key, []byte(tc.val)); err != nil {
				t.Fatalf("Set: %v", err)
			}
			want := tc.want
			if want == "" {
				want = tc.val
			}
			if got := data[key]; got != want {
				t.Fatalf("server got %q, want %q", got, want)
			}
		})
	}
}

func TestKeyPathEscaping_regressions(t *testing.T) {
	srv, data := newTestServer(t)
	store := infisical.New(srv.URL, "tok", "proj1", "dev")
	store.SetClient(srv.Client())
	ctx := context.Background()

	key := "ns/key"

	if err := store.Set(ctx, key, []byte("val")); err != nil {
		t.Fatalf("Set(%q): %v", key, err)
	}
	if data[key] != "val" {
		t.Fatalf("server data[%q] = %q, want %q", key, data[key], "val")
	}

	s, err := store.Get(ctx, key)
	if err != nil {
		t.Fatalf("Get(%q): %v", key, err)
	}
	if string(s.Value) != "val" {
		t.Fatalf("Get(%q) value = %q, want %q", key, s.Value, "val")
	}

	if err := store.Delete(ctx, key); err != nil {
		t.Fatalf("Delete(%q): %v", key, err)
	}
	if _, ok := data[key]; ok {
		t.Fatalf("expected key %q deleted", key)
	}
}

func TestExists(t *testing.T) {
	srv, data := newTestServer(t)
	data["EXISTS"] = "yes"

	store := infisical.New(srv.URL, "tok", "proj1", "dev")
	store.SetClient(srv.Client())

	ctx := context.Background()
	ok, err := store.Exists(ctx, "EXISTS")
	if err != nil || !ok {
		t.Fatalf("Exists(EXISTS): ok=%v err=%v", ok, err)
	}

	ok, err = store.Exists(ctx, "NOPE")
	if err != nil || ok {
		t.Fatalf("Exists(NOPE): ok=%v err=%v", ok, err)
	}
}
