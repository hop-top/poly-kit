package config

import (
	"errors"
	"os"
	"path/filepath"
)

// ErrNotFound is returned when no project marker is found while walking up.
var ErrNotFound = errors.New("config: project marker not found")

// DefaultMaxDepth caps Nearest's parent-walk so a misconfigured startDir
// can't iterate forever on exotic filesystems. Filesystem root short-circuits
// terminate normal walks first.
const DefaultMaxDepth = 64

// Nearest walks up from startDir looking for marker (e.g. ".rlz/config.yaml").
// marker is interpreted relative to each ancestor directory; the first hit
// wins. Returns the absolute path to the marker file (or directory) when found.
//
// marker may be a single segment (".rlz") to match a directory, or a path with
// separators (".rlz/config.yaml") to match a file inside a marker directory.
// Empty startDir defaults to cwd.
func Nearest(startDir, marker string) (string, error) {
	return NearestWithDepth(startDir, marker, DefaultMaxDepth)
}

// NearestWithDepth is Nearest with an explicit depth cap (0 = unlimited).
func NearestWithDepth(startDir, marker string, maxDepth int) (string, error) {
	if marker == "" {
		return "", errors.New("config: marker is empty")
	}
	dir := startDir
	if dir == "" {
		cwd, err := os.Getwd()
		if err != nil {
			return "", err
		}
		dir = cwd
	}
	abs, err := filepath.Abs(dir)
	if err != nil {
		return "", err
	}
	dir = abs

	for depth := 0; maxDepth == 0 || depth < maxDepth; depth++ {
		candidate := filepath.Join(dir, marker)
		if _, err := os.Stat(candidate); err == nil {
			return candidate, nil
		} else if !os.IsNotExist(err) {
			return "", err
		}
		parent := filepath.Dir(dir)
		if parent == dir { // reached fs root
			break
		}
		dir = parent
	}
	return "", ErrNotFound
}
