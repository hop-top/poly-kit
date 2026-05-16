package config

import (
	"errors"
	"os"
	"path/filepath"
	"testing"

	"gopkg.in/yaml.v3"
)

func writeYAML(t *testing.T, path, content string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatalf("write %s: %v", path, err)
	}
}

func TestMigrate_NoOpWhenAtLatest(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "c.yaml")
	writeYAML(t, p, "schema_version: 2\nmodel: x\n")

	migs := []Migration{{From: 0, To: 1, Apply: func(m map[string]any) (map[string]any, error) { return m, nil }},
		{From: 1, To: 2, Apply: func(m map[string]any) (map[string]any, error) { return m, nil }}}
	out, err := Migrate(p, migs, MigrateOptions{})
	if err != nil {
		t.Fatalf("Migrate: %v", err)
	}
	if out["schema_version"] != 2 {
		t.Errorf("schema_version: got %v want 2", out["schema_version"])
	}
}

func TestMigrate_AppliesInOrder(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "c.yaml")
	writeYAML(t, p, "model: old\n") // version 0 (key missing)

	migs := []Migration{
		{From: 0, To: 1, Apply: func(m map[string]any) (map[string]any, error) {
			m["retries"] = 3
			return m, nil
		}},
		{From: 1, To: 2, Apply: func(m map[string]any) (map[string]any, error) {
			// rename "model" -> "engine"
			if v, ok := m["model"]; ok {
				m["engine"] = v
				delete(m, "model")
			}
			return m, nil
		}},
	}

	var seen []int
	out, err := Migrate(p, migs, MigrateOptions{
		OnMigration: func(_ string, m Migration) { seen = append(seen, m.To) },
	})
	if err != nil {
		t.Fatalf("Migrate: %v", err)
	}
	if !equalInts(seen, []int{1, 2}) {
		t.Errorf("on-migration: got %v want [1 2]", seen)
	}
	if out["schema_version"] != 2 {
		t.Errorf("schema_version: got %v want 2", out["schema_version"])
	}
	if _, ok := out["model"]; ok {
		t.Errorf("model key should have been renamed away")
	}
	if out["engine"] != "old" {
		t.Errorf("engine: got %v want 'old'", out["engine"])
	}
	if out["retries"] != 3 {
		t.Errorf("retries: got %v want 3", out["retries"])
	}
}

func TestMigrate_GapInChainErrors(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "c.yaml")
	writeYAML(t, p, "schema_version: 0\n")

	migs := []Migration{
		{From: 0, To: 1, Apply: func(m map[string]any) (map[string]any, error) { return m, nil }},
		{From: 2, To: 3, Apply: func(m map[string]any) (map[string]any, error) { return m, nil }},
	}
	_, err := Migrate(p, migs, MigrateOptions{})
	if err == nil {
		t.Fatal("gap chain: expected error, got nil")
	}
}

func TestMigrate_WriteBackPersistsMigratedShape(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "c.yaml")
	writeYAML(t, p, "model: old\n")

	migs := []Migration{
		{From: 0, To: 1, Apply: func(m map[string]any) (map[string]any, error) {
			m["model"] = "new"
			return m, nil
		}},
	}
	if _, err := Migrate(p, migs, MigrateOptions{WriteBack: true}); err != nil {
		t.Fatalf("Migrate: %v", err)
	}
	data, err := os.ReadFile(p)
	if err != nil {
		t.Fatalf("read back: %v", err)
	}
	var raw map[string]any
	if err := yaml.Unmarshal(data, &raw); err != nil {
		t.Fatalf("parse back: %v", err)
	}
	if raw["model"] != "new" {
		t.Errorf("write-back: got %v want 'new'", raw["model"])
	}
	if raw["schema_version"] != 1 {
		t.Errorf("write-back schema_version: got %v want 1", raw["schema_version"])
	}
}

func TestMigrate_DoesNotWriteBackWhenNoMigrationsApplied(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "c.yaml")
	original := "schema_version: 1\nmodel: keep\n"
	writeYAML(t, p, original)

	migs := []Migration{
		{From: 0, To: 1, Apply: func(m map[string]any) (map[string]any, error) { return m, nil }},
	}
	if _, err := Migrate(p, migs, MigrateOptions{WriteBack: true}); err != nil {
		t.Fatalf("Migrate: %v", err)
	}
	data, _ := os.ReadFile(p)
	if string(data) != original {
		t.Errorf("file was rewritten despite no migrations applied:\nwant %q\ngot  %q", original, string(data))
	}
}

func TestMigrate_MissingFile(t *testing.T) {
	_, err := Migrate("/nonexistent/x.yaml", []Migration{
		{From: 0, To: 1, Apply: func(m map[string]any) (map[string]any, error) { return m, nil }},
	}, MigrateOptions{})
	if err == nil || !errors.Is(err, os.ErrNotExist) {
		t.Errorf("missing file: want ErrNotExist, got %v", err)
	}
}

func TestLoad_RunsMigrationsOnEachFile(t *testing.T) {
	dir := t.TempDir()
	user := filepath.Join(dir, "user.yaml")
	project := filepath.Join(dir, "project.yaml")
	writeYAML(t, user, "model: m1\n") // version 0
	writeYAML(t, project, "schema_version: 1\nretries: 9\n")

	migs := []Migration{
		{From: 0, To: 1, Apply: func(m map[string]any) (map[string]any, error) {
			// only the user file actually needs migration
			if _, ok := m["model"]; ok {
				m["engine"] = m["model"]
				delete(m, "model")
			}
			return m, nil
		}},
	}

	type sample struct {
		Engine  string `yaml:"engine"`
		Retries int    `yaml:"retries"`
	}
	var cfg sample
	if err := Load(&cfg, Options{
		UserConfigPath:    user,
		ProjectConfigPath: project,
		Migrations:        migs,
	}); err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.Engine != "m1" {
		t.Errorf("Engine (migrated from model): got %q want m1", cfg.Engine)
	}
	if cfg.Retries != 9 {
		t.Errorf("Retries: got %d want 9", cfg.Retries)
	}
}

func equalInts(a, b []int) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}
