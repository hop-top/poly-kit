package onepassword

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

// connectSetPayload mirrors the JSON structure connectSet sends.
type connectSetPayload struct {
	Title  string `json:"title"`
	Fields []struct {
		Label string `json:"label"`
		Value string `json:"value"`
	} `json:"fields"`
}

// capturePayload returns a test server that decodes the POST body and
// sends the parsed payload to the returned channel.
func capturePayload(t *testing.T) (*httptest.Server, <-chan connectSetPayload) {
	t.Helper()
	ch := make(chan connectSetPayload, 1)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			t.Errorf("unexpected method %s", r.Method)
			w.WriteHeader(http.StatusMethodNotAllowed)
			return
		}
		var p connectSetPayload
		if err := json.NewDecoder(r.Body).Decode(&p); err != nil {
			t.Errorf("invalid JSON: %v", err)
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		ch <- p
		w.WriteHeader(http.StatusCreated)
	}))
	return srv, ch
}

func TestRegressionConnectSetPayloadStructure(t *testing.T) {
	srv, ch := capturePayload(t)
	defer srv.Close()

	store := NewConnect(srv.URL, "tok", "v")
	if err := store.Set(context.Background(), "my-secret", []byte("hunter2")); err != nil {
		t.Fatalf("Set: %v", err)
	}

	p := <-ch
	if p.Title != "my-secret" {
		t.Errorf("title: got %q, want %q", p.Title, "my-secret")
	}
	if len(p.Fields) != 1 {
		t.Fatalf("fields count: got %d, want 1", len(p.Fields))
	}
	if p.Fields[0].Label != "password" {
		t.Errorf("label: got %q, want %q", p.Fields[0].Label, "password")
	}
	if p.Fields[0].Value != "hunter2" {
		t.Errorf("value: got %q, want %q", p.Fields[0].Value, "hunter2")
	}
}

func TestRegressionConnectSetControlChars(t *testing.T) {
	cases := []struct {
		name  string
		value string
	}{
		{"null-byte", "before\x00after"},
		{"soh", "start\x01end"},
		{"unit-sep", "a\x1fb"},
		{"del", "x\x7fy"},
		{"mixed", "\x00\x01\x1f\x7f"},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			srv, ch := capturePayload(t)
			defer srv.Close()

			store := NewConnect(srv.URL, "tok", "v")
			if err := store.Set(context.Background(), tc.name, []byte(tc.value)); err != nil {
				t.Fatalf("Set: %v", err)
			}

			p := <-ch
			if p.Fields[0].Value != tc.value {
				t.Errorf("round-trip mismatch: got %q, want %q", p.Fields[0].Value, tc.value)
			}
		})
	}
}

func TestRegressionConnectSetUnicode(t *testing.T) {
	cases := []struct {
		name  string
		value string
	}{
		{"emoji", "pass🔑word"},
		{"cjk", "密码test"},
		{"mixed-scripts", "пароль-パスワード-كلمة"},
		{"surrogate-pair-emoji", "🏳️‍🌈🎉"},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			srv, ch := capturePayload(t)
			defer srv.Close()

			store := NewConnect(srv.URL, "tok", "v")
			if err := store.Set(context.Background(), tc.name, []byte(tc.value)); err != nil {
				t.Fatalf("Set: %v", err)
			}

			p := <-ch
			if p.Fields[0].Value != tc.value {
				t.Errorf("round-trip mismatch: got %q, want %q", p.Fields[0].Value, tc.value)
			}
		})
	}
}

func TestRegressionConnectSetEmptyValue(t *testing.T) {
	srv, ch := capturePayload(t)
	defer srv.Close()

	store := NewConnect(srv.URL, "tok", "v")
	if err := store.Set(context.Background(), "empty-key", []byte("")); err != nil {
		t.Fatalf("Set: %v", err)
	}

	p := <-ch
	if p.Title != "empty-key" {
		t.Errorf("title: got %q, want %q", p.Title, "empty-key")
	}
	if len(p.Fields) != 1 {
		t.Fatalf("fields count: got %d, want 1", len(p.Fields))
	}
	if p.Fields[0].Value != "" {
		t.Errorf("value: got %q, want empty string", p.Fields[0].Value)
	}
}
