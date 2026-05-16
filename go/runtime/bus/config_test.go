package bus

import (
	"context"
	"errors"
	"os"
	"testing"
)

// withUnsetEnv unsets the environment variable for the duration of
// the current test, restoring the prior value on cleanup. t.Setenv
// is documented to fail when called from a test that has already
// been Setenv'd in a parent — using os.Unsetenv + Cleanup keeps the
// table-driven precedence tests independent.
func withUnsetEnv(t *testing.T, key string) {
	t.Helper()
	old, had := os.LookupEnv(key)
	if err := os.Unsetenv(key); err != nil {
		t.Fatalf("unset %s: %v", key, err)
	}
	t.Cleanup(func() {
		if had {
			_ = os.Setenv(key, old)
		} else {
			_ = os.Unsetenv(key)
		}
	})
}

// fakeGetter is a tiny ConfigGetter for testing.
type fakeGetter map[string]string

func (g fakeGetter) Get(key string) (string, bool) {
	v, ok := g[key]
	return v, ok
}

func TestModeFromString(t *testing.T) {
	tests := []struct {
		in      string
		want    Mode
		wantErr bool
	}{
		{"", ModeWarn, false},
		{"   ", ModeWarn, false},
		{"off", ModeOff, false},
		{"OFF", ModeOff, false},
		{"  Warn  ", ModeWarn, false},
		{"strict", ModeStrict, false},
		{"Strict", ModeStrict, false},
		{"loud", ModeWarn, true},
		{"verbose", ModeWarn, true},
	}
	for _, tc := range tests {
		got, err := ModeFromString(tc.in)
		if (err != nil) != tc.wantErr {
			t.Errorf("ModeFromString(%q) err = %v, wantErr=%v", tc.in, err, tc.wantErr)
		}
		if got != tc.want {
			t.Errorf("ModeFromString(%q) = %v, want %v", tc.in, got, tc.want)
		}
	}
}

func TestModeFromEnv(t *testing.T) {
	tests := []struct {
		name string
		set  bool
		val  string
		want Mode
	}{
		{"unset", false, "", ModeWarn},
		{"empty", true, "", ModeWarn},
		{"off", true, "off", ModeOff},
		{"warn", true, "warn", ModeWarn},
		{"strict", true, "strict", ModeStrict},
		{"strict mixed case", true, "Strict", ModeStrict},
		{"invalid falls back", true, "yelling", ModeWarn},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if tc.set {
				t.Setenv(EnvEnforce, tc.val)
			} else {
				withUnsetEnv(t, EnvEnforce)
			}
			if got := ModeFromEnv(); got != tc.want {
				t.Errorf("ModeFromEnv() = %v, want %v", got, tc.want)
			}
		})
	}
}

func TestModeFromConfig_Precedence(t *testing.T) {
	tests := []struct {
		name    string
		getter  ConfigGetter
		envVal  string // "" means unset
		wantSet bool
		want    Mode
	}{
		{
			name:    "config overrides env",
			getter:  fakeGetter{ConfigKeyEnforce: "strict"},
			envVal:  "off",
			wantSet: true,
			want:    ModeStrict,
		},
		{
			name:    "env used when no config key",
			getter:  fakeGetter{},
			envVal:  "off",
			wantSet: true,
			want:    ModeOff,
		},
		{
			name:    "default when neither set",
			getter:  fakeGetter{},
			envVal:  "",
			wantSet: false,
			want:    ModeWarn,
		},
		{
			name:    "nil getter falls through to env",
			getter:  nil,
			envVal:  "strict",
			wantSet: true,
			want:    ModeStrict,
		},
		{
			name:    "invalid config value falls through to env",
			getter:  fakeGetter{ConfigKeyEnforce: "loud"},
			envVal:  "off",
			wantSet: true,
			want:    ModeOff,
		},
		{
			name:    "all unparseable returns default",
			getter:  fakeGetter{ConfigKeyEnforce: "nope"},
			envVal:  "alsono",
			wantSet: true,
			want:    ModeWarn,
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if tc.wantSet {
				t.Setenv(EnvEnforce, tc.envVal)
			} else {
				withUnsetEnv(t, EnvEnforce)
			}
			if got := ModeFromConfig(tc.getter); got != tc.want {
				t.Errorf("ModeFromConfig = %v, want %v", got, tc.want)
			}
		})
	}
}

func TestWithEnforceFromEnv(t *testing.T) {
	t.Setenv(EnvEnforce, "strict")
	b := New(WithEnforceFromEnv())
	defer func() { _ = b.Close(context.Background()) }()

	err := b.Publish(context.Background(), NewEvent("bad.topic", "src", nil))
	if !errors.Is(err, ErrInvalidTopic) {
		t.Errorf("with WithEnforceFromEnv=strict and invalid topic: err = %v, want ErrInvalidTopic", err)
	}
}

func TestWithEnforce_OverridesEnv(t *testing.T) {
	// Explicit WithEnforce > env. Env says strict, code says off.
	t.Setenv(EnvEnforce, "strict")
	b := New(WithEnforceFromEnv(), WithEnforce(ModeOff))
	defer func() { _ = b.Close(context.Background()) }()

	if err := b.Publish(context.Background(), NewEvent("bad.topic", "src", nil)); err != nil {
		t.Errorf("explicit WithEnforce(ModeOff) should win, got err = %v", err)
	}
}
