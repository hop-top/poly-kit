package bus

import "os"

// AuthFromEnv builds a [StaticTokenAuth] from the first non-empty
// environment variable in envNames. The variables are checked in
// order, so callers can pass tool-specific names first and a generic
// fallback last (e.g. "DPKMS_BUS_TOKEN", "BUS_TOKEN"). When no
// variable is set or all are empty, AuthFromEnv returns (nil, false)
// and the caller can decide whether the absence is fatal.
func AuthFromEnv(envNames ...string) (*StaticTokenAuth, bool) {
	for _, name := range envNames {
		if v := os.Getenv(name); v != "" {
			return &StaticTokenAuth{Token_: v}, true
		}
	}
	return nil, false
}
