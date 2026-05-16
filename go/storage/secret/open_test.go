package secret_test

import (
	"context"
	"testing"

	"hop.top/kit/go/storage/secret"
	_ "hop.top/kit/go/storage/secret/agefile"
	_ "hop.top/kit/go/storage/secret/ghsecrets"
	_ "hop.top/kit/go/storage/secret/infisical"
	_ "hop.top/kit/go/storage/secret/memory"
	_ "hop.top/kit/go/storage/secret/onepassword"
)

func TestOpenMemory(t *testing.T) {
	s, err := secret.Open(secret.Config{Backend: "memory"})
	if err != nil {
		t.Fatalf("Open memory: %v", err)
	}
	ctx := context.Background()
	if err := s.Set(ctx, "k", []byte("v")); err != nil {
		t.Fatal(err)
	}
	got, err := s.Get(ctx, "k")
	if err != nil {
		t.Fatal(err)
	}
	if string(got.Value) != "v" {
		t.Fatalf("got %q", got.Value)
	}
}

func TestOpenInfisicalMissingAddr(t *testing.T) {
	_, err := secret.Open(secret.Config{Backend: "infisical"})
	if err == nil {
		t.Fatal("expected error for missing Addr")
	}
}

func TestOpenInfisicalMissingToken(t *testing.T) {
	_, err := secret.Open(secret.Config{Backend: "infisical", Addr: "http://localhost"})
	if err == nil {
		t.Fatal("expected error for missing Token")
	}
}

func TestOpenInfisicalMissingProject(t *testing.T) {
	_, err := secret.Open(secret.Config{
		Backend: "infisical", Addr: "http://localhost", Token: "tok",
	})
	if err == nil {
		t.Fatal("expected error for missing Project")
	}
}

func TestOpenInfisicalMissingEnv(t *testing.T) {
	_, err := secret.Open(secret.Config{
		Backend: "infisical", Addr: "http://localhost", Token: "tok", Project: "p",
	})
	if err == nil {
		t.Fatal("expected error for missing Env")
	}
}

func TestOpenInfisicalValid(t *testing.T) {
	s, err := secret.Open(secret.Config{
		Backend: "infisical", Addr: "http://localhost:8080",
		Token: "tok", Project: "proj", Env: "dev",
	})
	if err != nil {
		t.Fatalf("Open infisical: %v", err)
	}
	if s == nil {
		t.Fatal("expected non-nil store")
	}
}

func TestOpenAgefileMissingPath(t *testing.T) {
	_, err := secret.Open(secret.Config{Backend: "agefile"})
	if err == nil {
		t.Fatal("expected error for missing Path")
	}
}

func TestOpenAgefileMissingIdentity(t *testing.T) {
	_, err := secret.Open(secret.Config{Backend: "agefile", Path: "/tmp/x.age"})
	if err == nil {
		t.Fatal("expected error for missing IdentityFile")
	}
}

func TestOpenAgefileValid(t *testing.T) {
	s, err := secret.Open(secret.Config{
		Backend: "agefile", Path: "/tmp/secrets.age", IdentityFile: "/tmp/id.txt",
	})
	if err != nil {
		t.Fatalf("Open agefile: %v", err)
	}
	if s == nil {
		t.Fatal("expected non-nil store")
	}
}

func TestOpenGhsecretsValid(t *testing.T) {
	s, err := secret.Open(secret.Config{Backend: "ghsecrets", Repo: "owner/repo"})
	if err != nil {
		t.Fatalf("Open ghsecrets: %v", err)
	}
	if s == nil {
		t.Fatal("expected non-nil store")
	}
}

func TestOpenOnepasswordMissingVault(t *testing.T) {
	_, err := secret.Open(secret.Config{Backend: "onepassword"})
	if err == nil {
		t.Fatal("expected error for missing Vault")
	}
}

func TestOpenOnepasswordCLIMode(t *testing.T) {
	s, err := secret.Open(secret.Config{Backend: "onepassword", Vault: "Personal"})
	if err != nil {
		t.Fatalf("Open onepassword CLI: %v", err)
	}
	if s == nil {
		t.Fatal("expected non-nil store")
	}
}
