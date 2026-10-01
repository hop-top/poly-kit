package kv_test

import (
	"bytes"
	"crypto/tls"
	"fmt"
	"log/slog"
	"strings"
	"testing"

	"hop.top/kit/go/storage/kv"
)

// A Config carries credentials (Password, a DSN password, userinfo in an
// endpoint) and is the kind of value that ends up in a log line or a
// wrapped error. Every way of printing it must redact them while keeping
// the fields an operator needs to tell configs apart.
func TestConfigRedactsCredentials(t *testing.T) {
	cfg := kv.Config{
		Backend: "etcd",
		Endpoints: []string{
			"https://kit-user:kit-secret@etcd.example:2379/v3",
			"kit-user:kit-secret@10.0.0.1:2379",
			"grpc://kit-user:kit-secret@10.0.0.2:2379",
			"unix:///tmp/etcd.sock",
		},
		Prefix:   "app/",
		Username: "kit-user",
		Password: "kit-secret",
		TLS:      &tls.Config{ServerName: "etcd.example", MinVersion: tls.VersionTLS12},
		DSN:      "root:kit-secret@tcp(tidb.example:4000)/app?tls=true",
		Table:    "kv",
	}

	var text, js bytes.Buffer
	slog.New(slog.NewTextHandler(&text, nil)).Info("open", "cfg", cfg)
	slog.New(slog.NewJSONHandler(&js, nil)).Info("open", "cfg", cfg)

	renders := map[string]string{
		"String":    cfg.String(),
		"GoString":  cfg.GoString(),
		"%v":        fmt.Sprintf("%v", cfg),
		"%+v":       fmt.Sprintf("%+v", cfg),
		"%s":        fmt.Sprintf("%s", cfg), //nolint:staticcheck // %s is the verb under test
		"%#v":       fmt.Sprintf("%#v", cfg),
		"pointer":   fmt.Sprintf("%+v", &cfg),
		"error":     fmt.Errorf("open %v: failed", cfg).Error(),
		"slog text": text.String(),
		"slog json": js.String(),
	}
	for name, out := range renders {
		t.Run(name, func(t *testing.T) {
			if strings.Contains(out, "kit-secret") {
				t.Fatalf("credential leaked: %s", out)
			}
			for _, keep := range []string{"etcd", "etcd.example:2379", "10.0.0.1:2379", "unix:///tmp/etcd.sock", "app/", "tidb.example:4000"} {
				if !strings.Contains(out, keep) {
					t.Fatalf("render dropped %q: %s", keep, out)
				}
			}
		})
	}
}

// Redaction must not invent a credential that is not there: a Config
// without any prints its fields unchanged.
func TestConfigStringWithoutCredentials(t *testing.T) {
	cfg := kv.Config{
		Backend:   "etcd",
		Endpoints: []string{"http://etcd.example:2379/a@b", "unix:@abstract"},
		DSN:       "root@tcp(tidb.example:4000)/app",
	}
	out := cfg.String()
	for _, keep := range []string{"http://etcd.example:2379/a@b", "unix:@abstract", "root@tcp(tidb.example:4000)/app"} {
		if !strings.Contains(out, keep) {
			t.Fatalf("String() altered %q: %s", keep, out)
		}
	}
	if strings.Contains(out, "REDACTED") {
		t.Fatalf("String() redacted a Config with no credentials: %s", out)
	}
}
