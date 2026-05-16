package config

import (
	"fmt"
	"strings"

	"github.com/spf13/viper"
)

// EnvBind maps a dotted config key to an explicit environment variable name,
// for cases where the auto-derived name (PREFIX_FOO_BAR for foo.bar) doesn't
// match the legacy or required name.
type EnvBind struct {
	// Key is the dotted config key, e.g. "auth.token".
	Key string
	// Env is the environment variable name, e.g. "FOO_API_KEY".
	Env string
}

// BindEnv configures viper to read environment variables. It applies the
// standard recipe: SetEnvPrefix, replace dots with underscores in key paths,
// and AutomaticEnv. Explicit binds override the automatic mapping for keys
// whose env var doesn't follow the prefix+upper+underscore convention.
//
//	BindEnv(v, "TLC")
//	  -> viper.GetString("auth.token") reads TLC_AUTH_TOKEN
//
//	BindEnv(v, "TLC", EnvBind{Key: "auth.token", Env: "GH_TOKEN"})
//	  -> auth.token reads GH_TOKEN, every other key reads TLC_<KEY>
//
// Passing an empty prefix is allowed but discouraged; viper will then read
// AUTH_TOKEN with no namespace, which collides easily.
func BindEnv(v *viper.Viper, prefix string, explicit ...EnvBind) error {
	if v == nil {
		return fmt.Errorf("viper is nil")
	}
	if prefix != "" {
		v.SetEnvPrefix(prefix)
	}
	v.SetEnvKeyReplacer(strings.NewReplacer(".", "_"))
	v.AutomaticEnv()
	for _, b := range explicit {
		if b.Key == "" || b.Env == "" {
			return fmt.Errorf("env bind: empty Key or Env (%+v)", b)
		}
		// viper.BindEnv with two args: first is the dotted key, rest are
		// the literal env var names to consult (no prefix added).
		if err := v.BindEnv(b.Key, b.Env); err != nil {
			return fmt.Errorf("bind env %s -> %s: %w", b.Key, b.Env, err)
		}
	}
	return nil
}
