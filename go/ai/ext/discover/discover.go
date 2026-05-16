// Package discover implements PATH-based external plugin discovery.
//
// It scans directories for executables matching a given prefix, then
// optionally interrogates each binary via --ext-info to extract metadata.
package discover

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"hop.top/kit/go/ai/ext"
)

// Scanner finds executables matching a name prefix in a set of directories.
type Scanner struct {
	// Prefix is the binary name prefix to match (e.g. "kit-", "tlc-").
	Prefix string
	// Paths lists directories to scan. If empty, $PATH is split and used.
	Paths []string
}

// Found represents a discovered external plugin binary.
type Found struct {
	// Name is the extension name with the prefix stripped.
	Name string
	// Path is the absolute path to the binary.
	Path string
	// Version comes from --ext-info interrogation, if available.
	Version string

	meta *ext.Metadata
}

// Enrich populates the Found's metadata by interrogating the binary.
// On failure the Found remains usable with synthesized metadata.
func (f *Found) Enrich() error {
	m, err := Interrogate(f.Path)
	if err != nil {
		return err
	}
	f.meta = m
	f.Version = m.Version
	return nil
}

// Meta returns the extension metadata. If Enrich has been called
// successfully, it returns the interrogated metadata; otherwise it
// synthesizes metadata from the discovered name and version.
func (f *Found) Meta() ext.Metadata {
	if f.meta != nil {
		return *f.meta
	}
	return ext.Metadata{
		Name:    f.Name,
		Version: f.Version,
	}
}

// Capabilities returns CapDiscover — external plugins are always discovered.
func (f *Found) Capabilities() ext.Capability {
	return ext.CapDiscover
}

// Init executes the plugin binary. The context controls cancellation.
func (f *Found) Init(ctx context.Context) error {
	cmd := exec.CommandContext(ctx, f.Path)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	return cmd.Run()
}

// Close is a no-op for external plugin binaries.
func (f *Found) Close() error { return nil }

// Scan walks configured paths (or $PATH) for executables whose names
// start with the scanner's Prefix. It returns deduplicated results
// ordered by first occurrence.
func (s *Scanner) Scan() ([]Found, error) {
	dirs := s.Paths
	if len(dirs) == 0 {
		dirs = filepath.SplitList(os.Getenv("PATH"))
	}
	if s.Prefix == "" {
		return nil, fmt.Errorf("discover: prefix must not be empty")
	}

	seen := make(map[string]struct{})
	var results []Found

	for _, dir := range dirs {
		entries, err := os.ReadDir(dir)
		if err != nil {
			// Skip unreadable directories (permission errors, missing dirs).
			continue
		}
		for _, e := range entries {
			if e.IsDir() {
				continue
			}
			name := e.Name()
			if !strings.HasPrefix(name, s.Prefix) {
				continue
			}

			full := filepath.Join(dir, name)
			abs, err := filepath.Abs(full)
			if err != nil {
				continue
			}

			// Deduplicate by base name — first occurrence wins.
			if _, ok := seen[name]; ok {
				continue
			}

			info, err := e.Info()
			if err != nil {
				continue
			}
			if !isExecutable(info) {
				continue
			}

			seen[name] = struct{}{}
			results = append(results, Found{
				Name: strings.TrimPrefix(name, s.Prefix),
				Path: abs,
			})
		}
	}

	return results, nil
}

// isExecutable reports whether the file mode indicates an executable.
// NOTE: relies on Unix permission bits; Windows support (PATHEXT-based)
// is not yet implemented.
func isExecutable(fi os.FileInfo) bool {
	return fi.Mode()&0111 != 0
}

// extInfoResponse is the JSON schema returned by --ext-info.
type extInfoResponse struct {
	Name         string   `json:"name"`
	Version      string   `json:"version"`
	Description  string   `json:"description"`
	Capabilities []string `json:"capabilities"`
}

// interrogateTimeout is the maximum time to wait for --ext-info.
const interrogateTimeout = 5 * time.Second

// Interrogate executes the binary at path with --ext-info and parses
// the JSON response into ext.Metadata. Returns an error if the binary
// does not support the flag or returns invalid JSON.
func Interrogate(path string) (*ext.Metadata, error) {
	ctx, cancel := context.WithTimeout(context.Background(), interrogateTimeout)
	defer cancel()

	cmd := exec.CommandContext(ctx, path, "--ext-info")
	out, err := cmd.Output()
	if err != nil {
		return nil, fmt.Errorf("discover: interrogate %s: %w", path, err)
	}

	var resp extInfoResponse
	if err := json.Unmarshal(out, &resp); err != nil {
		return nil, fmt.Errorf("discover: parse ext-info from %s: %w", path, err)
	}

	return &ext.Metadata{
		Name:        resp.Name,
		Version:     resp.Version,
		Description: resp.Description,
	}, nil
}
