// Package discover implements PATH-based external plugin discovery.
//
// It scans directories for executables matching a given prefix, then
// optionally interrogates each binary via --ext-info to extract metadata.
package discover

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
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

	info *Info
}

// Enrich populates the Found's metadata by interrogating the binary.
// On failure the Found remains usable with synthesized metadata.
func (f *Found) Enrich() error {
	info, err := InterrogateInfo(f.Path)
	if err != nil {
		return err
	}
	f.info = info
	f.Version = info.Metadata.Version
	return nil
}

// Meta returns the extension metadata. If Enrich has been called
// successfully, it returns the interrogated metadata; otherwise it
// synthesizes metadata from the discovered name and version.
func (f *Found) Meta() ext.Metadata {
	if f.info != nil {
		return f.info.Metadata
	}
	return ext.Metadata{
		Name:    f.Name,
		Version: f.Version,
	}
}

// Info returns the full --ext-info response captured by the last
// successful Enrich, or nil if Enrich has not succeeded. Each call
// returns a fresh copy; reading it never executes the binary.
func (f *Found) Info() *Info {
	if f.info == nil {
		return nil
	}
	return f.info.clone()
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

// Info is a parsed --ext-info response.
//
// Metadata and Capabilities hold the protocol-defined fields. Raw is the
// verbatim JSON object the binary printed, including any fields the
// protocol does not define; hosts decode the fields they own from it
// (see Decode). Discovery never interprets those extra fields.
type Info struct {
	Metadata     ext.Metadata
	Capabilities []string
	Raw          json.RawMessage
}

// Decode unmarshals the verbatim --ext-info payload into v, letting a
// host read fields beyond name/version/description/capabilities.
// Decode on a nil *Info (e.g. Found.Info() before a successful Enrich)
// returns an error.
func (i *Info) Decode(v any) error {
	if i == nil {
		return errors.New("discover: no ext-info payload (Enrich not run or failed)")
	}
	return json.Unmarshal(i.Raw, v)
}

func (i *Info) clone() *Info {
	c := *i
	c.Capabilities = append([]string(nil), i.Capabilities...)
	c.Raw = append(json.RawMessage(nil), i.Raw...)
	return &c
}

// extInfoResponse holds the protocol-defined fields of --ext-info.
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
// does not support the flag or returns invalid JSON. Use InterrogateInfo
// to also receive capabilities and the verbatim payload.
func Interrogate(path string) (*ext.Metadata, error) {
	info, err := InterrogateInfo(path)
	if err != nil {
		return nil, err
	}
	return &info.Metadata, nil
}

// InterrogateInfo executes the binary at path with --ext-info once and
// returns the parsed response together with the verbatim payload.
// Errors match Interrogate.
func InterrogateInfo(path string) (*Info, error) {
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

	return &Info{
		Metadata: ext.Metadata{
			Name:        resp.Name,
			Version:     resp.Version,
			Description: resp.Description,
		},
		Capabilities: resp.Capabilities,
		Raw:          json.RawMessage(bytes.TrimSpace(out)),
	}, nil
}
