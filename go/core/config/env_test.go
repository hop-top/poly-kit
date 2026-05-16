package config

import (
	"testing"

	"github.com/spf13/viper"
)

func TestBindEnv_AutomaticPrefix(t *testing.T) {
	t.Setenv("MYAPP_AUTH_TOKEN", "from-env")
	v := viper.New()
	if err := BindEnv(v, "MYAPP"); err != nil {
		t.Fatalf("BindEnv: %v", err)
	}
	if got := v.GetString("auth.token"); got != "from-env" {
		t.Errorf("auto env bind: got %q want from-env", got)
	}
}

func TestBindEnv_ExplicitOverride(t *testing.T) {
	t.Setenv("GH_TOKEN", "literal-env")
	v := viper.New()
	if err := BindEnv(v, "MYAPP", EnvBind{Key: "auth.token", Env: "GH_TOKEN"}); err != nil {
		t.Fatalf("BindEnv: %v", err)
	}
	if got := v.GetString("auth.token"); got != "literal-env" {
		t.Errorf("explicit env bind: got %q want literal-env", got)
	}
}

func TestBindEnv_NoPrefixIsAllowed(t *testing.T) {
	t.Setenv("AUTH_TOKEN", "raw-token")
	v := viper.New()
	if err := BindEnv(v, ""); err != nil {
		t.Fatalf("BindEnv: %v", err)
	}
	if got := v.GetString("auth.token"); got != "raw-token" {
		t.Errorf("no-prefix bind: got %q want raw-token", got)
	}
}

func TestBindEnv_RejectsMalformedExplicit(t *testing.T) {
	v := viper.New()
	if err := BindEnv(v, "X", EnvBind{Key: "k"}); err == nil {
		t.Error("empty Env: expected error, got nil")
	}
	if err := BindEnv(v, "X", EnvBind{Env: "E"}); err == nil {
		t.Error("empty Key: expected error, got nil")
	}
}

func TestLoad_EnvBindsHydrateViperBeforeFiles(t *testing.T) {
	t.Setenv("MYAPP_MODEL", "env-model")
	v := viper.New()
	var cfg defaultsSample
	if err := Load(&cfg, Options{
		Defaults:  defaultsSample{Model: "default", Retries: 3},
		EnvPrefix: "MYAPP",
		Viper:     v,
	}); err != nil {
		t.Fatalf("Load: %v", err)
	}
	// Viper sees the env value.
	if got := v.GetString("model"); got != "env-model" {
		t.Errorf("viper sees env: got %q want env-model", got)
	}
	// Default still in dst because no file or override touches it; env binds
	// hydrate viper but not the typed dst (that's EnvOverride's job).
	if cfg.Model != "default" {
		t.Errorf("dst Model: got %q want default (env-only-hits-viper)", cfg.Model)
	}
}
