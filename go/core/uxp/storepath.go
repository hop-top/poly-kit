package uxp

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// ResolveStorePath returns the absolute filesystem path for the given CLI's
// primary data store. It expands ~ to the user's home directory and
// substitutes environment variables ($VAR / ${VAR}). It does NOT verify
// that the path exists on disk.
func ResolveStorePath(cli CLIName, reg *CLIRegistry) (string, error) {
	info, ok := reg.Get(cli)
	if !ok {
		return "", fmt.Errorf("uxp: unknown CLI %q", cli)
	}

	p := info.StoreRootPaths.Data
	if p == "" {
		return "", fmt.Errorf("uxp: CLI %q has no data store path", cli)
	}

	// Expand environment variables first (before tilde, so $HOME works).
	p = os.ExpandEnv(p)

	// Expand leading ~/ to home directory.
	if strings.HasPrefix(p, "~/") || p == "~" {
		home, err := os.UserHomeDir()
		if err != nil {
			return "", fmt.Errorf("uxp: resolve home dir: %w", err)
		}
		if p == "~" {
			p = home
		} else {
			p = filepath.Join(home, p[2:])
		}
	}

	// Clean the path to resolve any .. or double separators.
	p = filepath.Clean(p)

	// Ensure absolute path.
	if !filepath.IsAbs(p) {
		p, _ = filepath.Abs(p)
	}

	return p, nil
}
