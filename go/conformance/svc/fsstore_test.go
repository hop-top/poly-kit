package svc

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
)

func writeFile(t *testing.T, p, body string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestFSStore_GetMetaList(t *testing.T) {
	root := t.TempDir()
	writeFile(t, filepath.Join(root, "scenarios/acme/widget/2026.05.01/scenario.yaml"), "schema_version: \"1\"\n")
	writeFile(t, filepath.Join(root, "scenarios/acme/widget/2026.05.07/scenario.yaml"), "schema_version: \"1\"\n")
	writeFile(t, filepath.Join(root, "scenarios/acme/gadget/2026.05.01/scenario.yaml"), "schema_version: \"1\"\n")

	store, err := NewFSStore(context.Background(), root)
	if err != nil {
		t.Fatalf("NewFSStore: %v", err)
	}

	// Latest version resolves to 2026.05.07 (lexicographic).
	sc, err := store.Get(context.Background(), ScenarioRef{Namespace: "acme", ID: "widget"})
	if err != nil {
		t.Fatalf("Get latest: %v", err)
	}
	if sc.Version != "2026.05.07" {
		t.Errorf("latest version: got %q, want 2026.05.07", sc.Version)
	}

	// Explicit version resolves.
	sc, err = store.Get(context.Background(), ScenarioRef{Namespace: "acme", ID: "widget", Version: "2026.05.01"})
	if err != nil {
		t.Fatalf("Get pinned: %v", err)
	}
	if sc.Version != "2026.05.01" {
		t.Errorf("pinned version: got %q, want 2026.05.01", sc.Version)
	}

	// Missing scenario.
	_, err = store.Get(context.Background(), ScenarioRef{Namespace: "acme", ID: "doesnt-exist"})
	if !errors.Is(err, ErrScenarioNotFound) {
		t.Errorf("Get missing: want ErrScenarioNotFound, got %v", err)
	}

	// Meta for latest.
	m, err := store.Meta(context.Background(), ScenarioRef{Namespace: "acme", ID: "widget"})
	if err != nil {
		t.Fatalf("Meta: %v", err)
	}
	if m.Ref.Version != "2026.05.07" {
		t.Errorf("meta version: got %q", m.Ref.Version)
	}

	// List the namespace.
	list, err := store.List(context.Background(), "acme")
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(list) != 2 {
		t.Errorf("List size: want 2, got %d", len(list))
	}

	// Namespaces.
	ns, err := store.Namespaces(context.Background())
	if err != nil {
		t.Fatalf("Namespaces: %v", err)
	}
	if len(ns) != 1 || ns[0] != "acme" {
		t.Errorf("Namespaces: got %v", ns)
	}
}

func TestFSStore_Prompt(t *testing.T) {
	root := t.TempDir()
	writeFile(t, filepath.Join(root, "scenarios/acme/widget/v1/scenario.yaml"), "schema_version: \"1\"\n")
	writeFile(t, filepath.Join(root, "scenarios/acme/widget/v1/prompts/judge.md"), "be fair")

	store, err := NewFSStore(context.Background(), root)
	if err != nil {
		t.Fatalf("NewFSStore: %v", err)
	}

	text, err := store.Prompt(context.Background(),
		ScenarioRef{Namespace: "acme", ID: "widget", Version: "v1"},
		"prompts/judge.md")
	if err != nil {
		t.Fatalf("Prompt: %v", err)
	}
	if text != "be fair" {
		t.Errorf("Prompt body: got %q", text)
	}

	// Traversal rejected.
	if _, err := store.Prompt(context.Background(),
		ScenarioRef{Namespace: "acme", ID: "widget", Version: "v1"},
		"../../../../etc/passwd"); err == nil {
		t.Errorf("expected traversal rejection")
	}
}

func TestFSStore_EmptyRoot(t *testing.T) {
	root := t.TempDir()
	store, err := NewFSStore(context.Background(), root)
	if err != nil {
		t.Fatalf("NewFSStore empty root: %v", err)
	}
	ns, _ := store.Namespaces(context.Background())
	if len(ns) != 0 {
		t.Errorf("expected no namespaces, got %v", ns)
	}
}
