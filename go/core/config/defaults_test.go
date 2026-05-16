package config

import (
	"os"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/spf13/viper"
)

type defaultsSample struct {
	Model   string   `yaml:"model"`
	Retries int      `yaml:"retries"`
	Tags    []string `yaml:"tags"`
	Auth    struct {
		Token string `yaml:"token"`
	} `yaml:"auth"`
}

func TestApplyDefaults_StructSeedThenFileOverlay(t *testing.T) {
	dir := t.TempDir()
	file := filepath.Join(dir, "config.yaml")
	if err := os.WriteFile(file, []byte("model: from-file\n"), 0o600); err != nil {
		t.Fatalf("seed file: %v", err)
	}

	var cfg defaultsSample
	defaults := defaultsSample{
		Model:   "default-model",
		Retries: 3,
		Tags:    []string{"a", "b"},
	}
	defaults.Auth.Token = "default-token"

	if err := Load(&cfg, Options{
		Defaults:       defaults,
		UserConfigPath: file,
	}); err != nil {
		t.Fatalf("Load: %v", err)
	}

	// Defaults seed first; file overlays only the keys it sets.
	if cfg.Model != "from-file" {
		t.Errorf("Model: file should win, got %q", cfg.Model)
	}
	if cfg.Retries != 3 {
		t.Errorf("Retries: defaults should survive when file omits, got %d", cfg.Retries)
	}
	if !reflect.DeepEqual(cfg.Tags, []string{"a", "b"}) {
		t.Errorf("Tags: defaults should survive, got %v", cfg.Tags)
	}
	if cfg.Auth.Token != "default-token" {
		t.Errorf("Auth.Token: defaults should survive, got %q", cfg.Auth.Token)
	}
}

func TestSeedDefaults_FlattensIntoViper(t *testing.T) {
	v := viper.New()
	d := defaultsSample{Model: "m", Retries: 7}
	d.Auth.Token = "t"
	if err := SeedDefaults(v, d); err != nil {
		t.Fatalf("SeedDefaults: %v", err)
	}
	if got := v.GetString("model"); got != "m" {
		t.Errorf("viper model: got %q want m", got)
	}
	if got := v.GetInt("retries"); got != 7 {
		t.Errorf("viper retries: got %d want 7", got)
	}
	if got := v.GetString("auth.token"); got != "t" {
		t.Errorf("viper auth.token: got %q want t", got)
	}
}

func TestSeedDefaults_FilePathOverridesDefault(t *testing.T) {
	v := viper.New()
	if err := SeedDefaults(v, defaultsSample{Model: "default"}); err != nil {
		t.Fatalf("SeedDefaults: %v", err)
	}
	v.Set("model", "explicit")
	if got := v.GetString("model"); got != "explicit" {
		t.Errorf("explicit Set should beat SetDefault, got %q", got)
	}
}

func TestLoad_ViperDefaultsThenExplicitSync(t *testing.T) {
	dir := t.TempDir()
	file := filepath.Join(dir, "config.yaml")
	if err := os.WriteFile(file, []byte("model: from-file\nauth:\n  token: ftok\n"), 0o600); err != nil {
		t.Fatalf("seed file: %v", err)
	}
	v := viper.New()
	var cfg defaultsSample
	if err := Load(&cfg, Options{
		Defaults:       defaultsSample{Model: "default", Retries: 3},
		UserConfigPath: file,
		Viper:          v,
	}); err != nil {
		t.Fatalf("Load: %v", err)
	}
	// Defaults reached viper.SetDefault. Files did NOT auto-sync — Load
	// only reflects them in dst (typed source of truth).
	if got := v.GetString("model"); got != "default" {
		t.Errorf("viper model after Load: got %q want default (file values are typed-only)", got)
	}
	if cfg.Model != "from-file" {
		t.Errorf("dst Model after Load: got %q want from-file", cfg.Model)
	}

	// Callers who want viper to mirror dst opt in via SyncToViper.
	if err := SyncToViper(v, &cfg); err != nil {
		t.Fatalf("SyncToViper: %v", err)
	}
	if got := v.GetString("model"); got != "from-file" {
		t.Errorf("viper model after SyncToViper: got %q want from-file", got)
	}
	if got := v.GetString("auth.token"); got != "ftok" {
		t.Errorf("viper auth.token after SyncToViper: got %q want ftok", got)
	}
}

func TestSeedDefaults_NilArgsAreSafe(t *testing.T) {
	v := viper.New()
	if err := SeedDefaults(v, nil); err != nil {
		t.Errorf("SeedDefaults(nil): %v", err)
	}
	if err := SeedDefaults(nil, defaultsSample{}); err == nil {
		t.Error("SeedDefaults with nil viper: expected error, got nil")
	}
}
