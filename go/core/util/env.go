package util

import (
	"os"
	"strconv"
	"strings"
	"time"
)

// EnvString returns os.Getenv(key) or def if empty.
func EnvString(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}

// EnvInt returns the env var as int, or def if missing/invalid.
func EnvInt(key string, def int) int {
	v := os.Getenv(key)
	if v == "" {
		return def
	}
	n, err := strconv.Atoi(v)
	if err != nil {
		return def
	}
	return n
}

// EnvBool returns the env var as bool, or def if missing/invalid.
// Truthy: "1", "true", "yes", "on" (case-insensitive).
func EnvBool(key string, def bool) bool {
	v := os.Getenv(key)
	if v == "" {
		return def
	}
	switch strings.ToLower(v) {
	case "1", "true", "yes", "on":
		return true
	case "0", "false", "no", "off":
		return false
	}
	return def
}

// EnvDuration returns the env var parsed as time.Duration, or def.
func EnvDuration(key string, def time.Duration) time.Duration {
	v := os.Getenv(key)
	if v == "" {
		return def
	}
	d, err := time.ParseDuration(v)
	if err != nil {
		return def
	}
	return d
}
