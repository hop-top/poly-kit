package router

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"

	"hop.top/kit/go/core/xdg"
)

var validSlug = regexp.MustCompile(`^[a-zA-Z0-9][a-zA-Z0-9_-]*$`)

// defaultConfigPath returns the default router config file path:
// $XDG_CONFIG_HOME/hop/llm/router/config.yaml
func defaultConfigPath() (string, error) {
	dir, err := xdg.ConfigDir("hop")
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, "llm", "router", "config.yaml"), nil
}

// stateDir returns the router state directory where PID files live:
// $XDG_STATE_HOME/hop/llm/router/
func stateDir() (string, error) {
	dir, err := xdg.StateDir("hop")
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, "llm", "router"), nil
}

// ensureStateDir creates the state directory if it does not exist.
func ensureStateDir() (string, error) {
	dir, err := stateDir()
	if err != nil {
		return "", err
	}
	if err := os.MkdirAll(dir, 0o750); err != nil {
		return "", err
	}
	return dir, nil
}

// pidFilePath returns the path for a PID file identified by slug.
// Slug must be alphanumeric with hyphens/underscores only.
func pidFilePath(slug string) (string, error) {
	if !validSlug.MatchString(slug) {
		return "", fmt.Errorf("invalid slug %q: must match [a-zA-Z0-9_-]", slug)
	}
	dir, err := stateDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, slug+".pid"), nil
}
