package consent

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sync"

	"gopkg.in/yaml.v3"

	"hop.top/kit/go/core/xdg"
)

// xdgTool is the tool slug used when resolving the consent file path.
// Fixed to "kit" so the path matches kit-telemetry's installation_id
// neighborhood (xdg slug, not the embedding-app prefix). Polyglot SDKs
// share the same file; per-tool interpolation would break SDK reads.
const xdgTool = "kit"

// consentFileRel is the relative path under <XDG_CONFIG_HOME>/<xdgTool>.
// This YAML is the kit AppConfig — consent is one partition under
// telemetry, not a dedicated file. Callers MUST NOT assume the file
// is consent-only.
const consentFileRel = "telemetry.yaml"

// filePerm is the required mode of the on-disk file. 0600 because the
// install_id sibling uses the same posture; a world-readable consent
// file would leak the user's decision to other processes on shared
// boxes for no operational benefit.
const filePerm fs.FileMode = 0o600

// dirPerm is applied to the parent dir on first write. Matches the
// install_id parent dir perm (0700) so the whole kit config tree is
// uniformly owner-only.
const dirPerm fs.FileMode = 0o700

// FileStore is the default Store backed by a YAML file at
// <XDG_CONFIG_HOME>/kit/telemetry.yaml. The implementation preserves
// unknown top-level keys via yaml.Node round-tripping — this file is
// the kit AppConfig, so sibling partitions (other than
// telemetry.consent) MUST survive Set and Clear untouched.
//
// Concurrency: an internal mutex serializes Get/Set/Clear. The file
// itself is rewritten atomically (tmp + rename) so external readers
// never observe a half-written file. We do not take an OS-level file
// lock; cross-process write contention against the consent file is
// out of scope (kit telemetry subcommands run interactively, not in
// parallel).
type FileStore struct {
	// path is the resolved on-disk location. Populated once at
	// construction time via xdg.ConfigFile and reused for every
	// operation; we do not re-resolve per call to avoid the package-
	// global Reload race that the install_id implementation guards.
	path string

	// mu serializes file access within a single process.
	mu sync.Mutex
}

// NewFileStore constructs the default store, resolving the canonical
// path under XDG_CONFIG_HOME via the kit xdg wrapper. The returned
// FileStore does not touch the filesystem; the first Get/Set call
// performs the actual read or write.
func NewFileStore() (*FileStore, error) {
	path, err := xdg.ConfigFile(xdgTool, consentFileRel)
	if err != nil {
		return nil, fmt.Errorf("consent: resolve path: %w", err)
	}
	return &FileStore{path: path}, nil
}

// Path returns the resolved file path. Useful for `kit telemetry
// status` diagnostics and for tests asserting on the resolved
// location.
func (s *FileStore) Path() string {
	return s.path
}

// Get reads and returns the current decision. Per the Store contract,
// a missing file or absent telemetry.consent block returns
// Decision{State: StateUnknown} with a nil error; malformed YAML is
// surfaced as an error.
func (s *FileStore) Get(ctx context.Context) (Decision, error) {
	if err := ctx.Err(); err != nil {
		return Decision{}, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()

	doc, err := s.readDoc()
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return Decision{State: StateUnknown}, nil
		}
		return Decision{}, err
	}

	d, found, err := extractDecision(doc)
	if err != nil {
		return Decision{}, err
	}
	if !found {
		return Decision{State: StateUnknown}, nil
	}
	return d, nil
}

// Set persists the decision atomically. Existing top-level keys other
// than telemetry.consent are preserved verbatim; the telemetry.consent
// block is replaced wholesale with the supplied Decision.
func (s *FileStore) Set(ctx context.Context, d Decision) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()

	doc, err := s.readDoc()
	if err != nil && !errors.Is(err, fs.ErrNotExist) {
		return err
	}
	if doc == nil {
		doc = newMappingDoc()
	}

	if err := upsertDecision(doc, d); err != nil {
		return err
	}
	return s.writeDoc(doc)
}

// Clear resets to StateUnknown by removing the telemetry.consent block.
// If the file does not exist, Clear is a no-op (success). Other
// top-level keys are preserved.
func (s *FileStore) Clear(ctx context.Context) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()

	doc, err := s.readDoc()
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return nil
		}
		return err
	}
	removeConsent(doc)
	return s.writeDoc(doc)
}

// readDoc reads and parses the YAML file. Returns (nil, fs.ErrNotExist-
// wrapped error) when the file is absent so callers can branch
// cleanly. Returns an error for malformed YAML — see Store.Get contract.
func (s *FileStore) readDoc() (*yaml.Node, error) {
	b, err := os.ReadFile(s.path)
	if err != nil {
		return nil, err
	}
	if len(b) == 0 {
		return nil, nil
	}
	var doc yaml.Node
	if err := yaml.Unmarshal(b, &doc); err != nil {
		return nil, fmt.Errorf("consent: parse %s: %w", s.path, err)
	}
	return &doc, nil
}

// writeDoc serializes doc and writes it atomically (tmp + rename) with
// the required 0600 perms. The parent dir is created on demand with
// 0700 perms — xdg.ConfigFile already creates the parents per its
// docs, but we enforce the mode explicitly because the underlying
// library uses its own defaults.
func (s *FileStore) writeDoc(doc *yaml.Node) error {
	if err := os.MkdirAll(filepath.Dir(s.path), dirPerm); err != nil {
		return fmt.Errorf("consent: mkdir: %w", err)
	}

	b, err := yaml.Marshal(doc)
	if err != nil {
		return fmt.Errorf("consent: marshal: %w", err)
	}

	tmp, err := os.CreateTemp(filepath.Dir(s.path), ".telemetry.yaml.tmp.*")
	if err != nil {
		return fmt.Errorf("consent: create tmp: %w", err)
	}
	tmpPath := tmp.Name()
	// Best-effort cleanup if anything between here and Rename fails.
	defer func() {
		// If we successfully renamed, tmp is gone; Remove returns
		// ErrNotExist which we ignore.
		_ = os.Remove(tmpPath)
	}()

	if _, err := tmp.Write(b); err != nil {
		_ = tmp.Close()
		return fmt.Errorf("consent: write tmp: %w", err)
	}
	if err := tmp.Sync(); err != nil {
		_ = tmp.Close()
		return fmt.Errorf("consent: fsync tmp: %w", err)
	}
	if err := tmp.Close(); err != nil {
		return fmt.Errorf("consent: close tmp: %w", err)
	}
	if err := os.Chmod(tmpPath, filePerm); err != nil {
		return fmt.Errorf("consent: chmod tmp: %w", err)
	}
	if err := os.Rename(tmpPath, s.path); err != nil {
		return fmt.Errorf("consent: rename: %w", err)
	}
	// Belt-and-braces: enforce perms on the final path in case the
	// rename inherited the dir's mode on some platforms.
	if err := os.Chmod(s.path, filePerm); err != nil {
		return fmt.Errorf("consent: chmod final: %w", err)
	}
	return nil
}
