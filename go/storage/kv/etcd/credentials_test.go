package etcd

import (
	"crypto/tls"
	"strings"
	"testing"

	"hop.top/kit/go/storage/kv"
)

func applyOptions(opts ...Option) options {
	var o options
	for _, opt := range opts {
		opt(&o)
	}
	return o
}

// Credentials reach the client only through clientv3.Config's own fields:
// the client never reads endpoint userinfo.
func TestClientConfigCarriesCredentials(t *testing.T) {
	tlsCfg := &tls.Config{ServerName: "etcd.example", MinVersion: tls.VersionTLS12}
	eps := []string{"https://etcd.example:2379"}

	cfg := clientConfig(eps, applyOptions(WithAuth("kit-user", "kit-secret"), WithTLS(tlsCfg)))
	if cfg.Username != "kit-user" || cfg.Password != "kit-secret" {
		t.Fatalf("credentials not mapped: Username=%q Password set=%v", cfg.Username, cfg.Password != "")
	}
	if cfg.TLS != tlsCfg {
		t.Fatal("TLS config not mapped")
	}
	if len(cfg.Endpoints) != 1 || cfg.Endpoints[0] != eps[0] {
		t.Fatalf("Endpoints = %v, want %v", cfg.Endpoints, eps)
	}
	if len(cfg.DialOptions) == 0 {
		t.Fatal("guarded dialer not installed")
	}

	bare := clientConfig(eps, applyOptions())
	if bare.Username != "" || bare.Password != "" || bare.TLS != nil {
		t.Fatal("credentials set without options")
	}
}

// The kv.Config fields are what kv.OpenContext users set; they must turn
// into the same client fields.
func TestConfigOptionsMapKVConfig(t *testing.T) {
	tlsCfg := &tls.Config{MinVersion: tls.VersionTLS12}
	o := applyOptions(configOptions(kv.Config{
		Backend:   "etcd",
		Endpoints: []string{"https://etcd.example:2379"},
		Username:  "kit-user",
		Password:  "kit-secret",
		TLS:       tlsCfg,
	})...)
	cfg := clientConfig([]string{"https://etcd.example:2379"}, o)
	if cfg.Username != "kit-user" || cfg.Password != "kit-secret" || cfg.TLS != tlsCfg {
		t.Fatalf("kv.Config credentials not mapped: Username=%q Password set=%v TLS=%v",
			cfg.Username, cfg.Password != "", cfg.TLS != nil)
	}

	if got := configOptions(kv.Config{Backend: "etcd"}); len(got) != 0 {
		t.Fatalf("options from a credential-free Config: %d", len(got))
	}
	// A lone field still becomes an option, so validation can reject it
	// instead of the client silently ignoring it.
	if o := applyOptions(configOptions(kv.Config{Password: "kit-secret"})...); o.validate() == nil {
		t.Fatal("lone Password from kv.Config was not rejected")
	}
}

// The client authenticates only when both are set and silently ignores
// either alone: a lone field is the same trap as endpoint userinfo.
func TestOptionsValidate(t *testing.T) {
	for _, tc := range []struct {
		name string
		opts []Option
		ok   bool
	}{
		{"none", nil, true},
		{"both", []Option{WithAuth("kit-user", "kit-secret")}, true},
		{"tls only", []Option{WithTLS(&tls.Config{MinVersion: tls.VersionTLS12})}, true},
		{"username only", []Option{WithAuth("kit-user", "")}, false},
		{"password only", []Option{WithAuth("", "kit-secret")}, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			err := applyOptions(tc.opts...).validate()
			if tc.ok && err != nil {
				t.Fatalf("validate() = %v, want nil", err)
			}
			if !tc.ok {
				if err == nil {
					t.Fatal("validate() = nil, want an error")
				}
				if strings.Contains(err.Error(), "kit-secret") || strings.Contains(err.Error(), "kit-user") {
					t.Fatalf("error echoes a credential: %v", err)
				}
			}
		})
	}
}

// The client applies the FIRST endpoint's transport security to the whole
// connection (dialWithBalancer). A list whose endpoints disagree would
// send traffic, credentials included, in plaintext to an https endpoint,
// or TLS to one meant for plaintext; TLS with http:// is dropped
// silently. Both are rejected.
func TestCheckTransport(t *testing.T) {
	for _, tc := range []struct {
		name string
		eps  []string
		tls  bool
		ok   bool
	}{
		{"plain host:port", []string{"a:2379", "b:2379"}, false, true},
		{"plain http", []string{"http://a:2379", "b:2379", "unix:///tmp/s"}, false, true},
		{"https", []string{"https://a:2379", "HTTPS://b:2379", "unixs:///tmp/s"}, false, true},
		{"tls with host:port", []string{"a:2379", "https://b:2379", "unix:///tmp/s"}, true, true},
		{"tls with http", []string{"http://a:2379"}, true, false},
		{"tls with uppercase http", []string{"https://a:2379", "HTTP://b:2379"}, true, false},
		{"http then https", []string{"http://a:2379", "https://b:2379"}, false, false},
		{"host:port then https", []string{"a:2379", "https://b:2379"}, false, false},
		{"https then host:port", []string{"https://a:2379", "b:2379"}, false, false},
		{"unix then unixs", []string{"unix:///tmp/a", "unixs:///tmp/b"}, false, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			err := checkTransport(tc.eps, tc.tls)
			if tc.ok && err != nil {
				t.Fatalf("checkTransport(%v, tls=%v) = %v, want nil", tc.eps, tc.tls, err)
			}
			if !tc.ok && err == nil {
				t.Fatalf("checkTransport(%v, tls=%v) = nil, want an error", tc.eps, tc.tls)
			}
		})
	}
}
