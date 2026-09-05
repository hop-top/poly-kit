package secret_test

import (
	"sort"
	"testing"

	"hop.top/kit/go/storage/secret"

	// Every backend the package documents must be blank-imported here
	// so its init registers it. A documented backend missing from this
	// list fails TestDocumentedBackendsResolve.
	_ "hop.top/kit/go/storage/secret/agefile"
	_ "hop.top/kit/go/storage/secret/env"
	_ "hop.top/kit/go/storage/secret/file"
	_ "hop.top/kit/go/storage/secret/ghsecrets"
	_ "hop.top/kit/go/storage/secret/infisical"
	_ "hop.top/kit/go/storage/secret/keyring"
	_ "hop.top/kit/go/storage/secret/memory"
	_ "hop.top/kit/go/storage/secret/onepassword"
	_ "hop.top/kit/go/storage/secret/openbao"
)

// minimalConfig returns a Config carrying the smallest set of fields
// the named backend needs to open successfully. Keeping these beside
// the documented list means adding a backend to secret.Backends
// without teaching the test how to configure it fails loudly rather
// than silently skipping.
func minimalConfig(t *testing.T, backend string) secret.Config {
	t.Helper()
	cfg := secret.Config{Backend: backend}
	switch backend {
	case "env", "keyring", "memory":
		// No required fields.
	case "file":
		cfg.Dir = t.TempDir()
	case "agefile":
		cfg.Path = "/tmp/secrets.age"
		cfg.IdentityFile = "/tmp/id.txt"
	case "onepassword":
		cfg.Vault = "Personal"
	case "ghsecrets":
		cfg.Repo = "owner/repo"
	case "openbao":
		cfg.Addr = "http://127.0.0.1:8200"
		cfg.Token = "tok"
	case "infisical":
		cfg.Addr = "http://127.0.0.1:8080"
		cfg.Token = "tok"
		cfg.Project = "proj"
		cfg.Env = "dev"
	default:
		t.Fatalf("no minimal config known for documented backend %q; "+
			"add one when registering a new backend", backend)
	}
	return cfg
}

// TestDocumentedBackendsResolve is the anti-drift guard: every name in
// secret.Backends — the set the README and Config.Backend advertise —
// must construct a usable store through Open. Documenting a backend
// without registering it (the file/openbao regression) fails here.
func TestDocumentedBackendsResolve(t *testing.T) {
	for _, backend := range secret.Backends {
		t.Run(backend, func(t *testing.T) {
			store, err := secret.Open(minimalConfig(t, backend))
			if err != nil {
				t.Fatalf("Open(%q): %v", backend, err)
			}
			if store == nil {
				t.Fatalf("Open(%q): nil store with nil error", backend)
			}
		})
	}
}

// TestBackendsListIsUnique guards against a duplicated entry masking a
// missing one when the list is edited by hand.
func TestBackendsListIsUnique(t *testing.T) {
	seen := map[string]bool{}
	for _, b := range secret.Backends {
		if seen[b] {
			t.Errorf("duplicate backend %q in secret.Backends", b)
		}
		seen[b] = true
	}
	if len(secret.Backends) == 0 {
		t.Fatal("secret.Backends is empty")
	}
}

// TestBackendsMatchREADME keeps the prose and the list in step: the
// README names the same backends, so a reader picking a name from the
// docs always gets one Open accepts.
func TestBackendsMatchREADME(t *testing.T) {
	readme := readREADMEBackends(t)
	documented := append([]string(nil), secret.Backends...)
	sort.Strings(readme)
	sort.Strings(documented)
	if len(readme) != len(documented) {
		t.Fatalf("README lists %v, secret.Backends has %v", readme, documented)
	}
	for i := range readme {
		if readme[i] != documented[i] {
			t.Fatalf("README lists %v, secret.Backends has %v", readme, documented)
		}
	}
}
