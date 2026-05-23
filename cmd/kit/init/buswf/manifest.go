// .kit/generated.json manifest reader/writer for the kit-bus workflow
// subset. This is the first concrete consumer of the manifest schema
// pinned in docs/contracts/kit-init-pr-wiring.md §6.
//
// We deliberately keep the schema isolated to this package for now —
// T-0772/T-0773/T-0774 will land their own entries and a shared writer
// can be promoted once we have three concrete callers. Per the "build
// the shared abstraction after the third example" rule.
package buswf

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"time"
)

// ManifestPath is the canonical path to .kit/generated.json (POSIX
// slashes; callers should filepath.FromSlash when constructing
// platform-specific paths).
const ManifestPath = ".kit/generated.json"

// Manifest is the on-disk shape of .kit/generated.json. Currently
// version 1.
type Manifest struct {
	Version     int            `json:"version"`
	GeneratedBy string         `json:"generated_by"`
	Files       []ManifestFile `json:"files"`
}

// ManifestFile is one entry in Manifest.Files.
type ManifestFile struct {
	Path        string `json:"path"`
	SHA256      string `json:"sha256"`
	GeneratedAt string `json:"generatedAt"`
}

// SHA256 computes the hex sha256 of body. Exposed so tests can build
// expected manifest values without re-deriving the helper.
func SHA256(body []byte) string {
	sum := sha256.Sum256(body)
	return hex.EncodeToString(sum[:])
}

// ReadManifest reads .kit/generated.json from root. Returns an empty
// Manifest (no entries) when the file does not exist — that case
// represents a bootstrap-tier run (no prior generation).
func ReadManifest(root string) (Manifest, error) {
	p := filepath.Join(root, filepath.FromSlash(ManifestPath))
	data, err := os.ReadFile(p)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return Manifest{Version: 1, GeneratedBy: "kit-init"}, nil
		}
		return Manifest{}, fmt.Errorf("read %s: %w", p, err)
	}
	var m Manifest
	if err := json.Unmarshal(data, &m); err != nil {
		return Manifest{}, fmt.Errorf("parse %s: %w", p, err)
	}
	return m, nil
}

// WriteManifest writes the manifest to .kit/generated.json under root,
// creating the parent dir as needed. Files entries are sorted by Path
// so re-serialisation is deterministic.
func WriteManifest(root string, m Manifest) error {
	m.Version = 1
	m.GeneratedBy = "kit-init"
	sort.Slice(m.Files, func(i, j int) bool {
		return m.Files[i].Path < m.Files[j].Path
	})
	data, err := json.MarshalIndent(m, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal manifest: %w", err)
	}
	data = append(data, '\n')

	p := filepath.Join(root, filepath.FromSlash(ManifestPath))
	if err := os.MkdirAll(filepath.Dir(p), 0o750); err != nil {
		return fmt.Errorf("mkdir %s: %w", filepath.Dir(p), err)
	}
	return os.WriteFile(p, data, 0o644)
}

// MergeFiles returns a copy of m with each entry in updates merged in:
// existing entries with the same Path are replaced, new paths appended.
// Other entries (e.g. from sibling generators) are preserved.
func (m Manifest) MergeFiles(updates []ManifestFile) Manifest {
	out := Manifest{
		Version:     1,
		GeneratedBy: "kit-init",
		Files:       []ManifestFile{},
	}
	byPath := map[string]ManifestFile{}
	for _, f := range m.Files {
		byPath[f.Path] = f
	}
	for _, f := range updates {
		byPath[f.Path] = f
	}
	for _, f := range byPath {
		out.Files = append(out.Files, f)
	}
	sort.Slice(out.Files, func(i, j int) bool {
		return out.Files[i].Path < out.Files[j].Path
	})
	return out
}

// Lookup returns the manifest entry for path (POSIX slashes) and a
// found bool. Used by augment-mode refresh logic.
func (m Manifest) Lookup(path string) (ManifestFile, bool) {
	for _, f := range m.Files {
		if f.Path == path {
			return f, true
		}
	}
	return ManifestFile{}, false
}

// nowUTC returns an RFC3339 UTC timestamp. Indirected for tests so they
// can pin generatedAt to a deterministic value.
var nowUTC = func() string {
	return time.Now().UTC().Format(time.RFC3339)
}
