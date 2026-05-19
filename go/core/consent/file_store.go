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
// kit.telemetry, not a dedicated file. Callers MUST NOT assume the
// file is consent-only.
const consentFileRel = "config.yaml"

// legacyConsentFileRel is the pre-refactor path that some installs may
// still carry on disk. Read paths fall back to it when the canonical
// config.yaml is missing or lacks the kit.telemetry.consent block; write
// paths always emit to config.yaml. Migration is silent — the legacy
// file is left in place to avoid clobbering sibling top-level keys that
// adopters may have added by hand.
const legacyConsentFileRel = "telemetry.yaml"

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
// <XDG_CONFIG_HOME>/kit/config.yaml under the kit.telemetry.consent
// partition. The implementation preserves unknown top-level keys via
// yaml.Node round-tripping — this file is the kit AppConfig, so
// sibling partitions (other than kit.telemetry.consent) MUST survive
// Set and Clear untouched.
//
// Migration: an earlier layout persisted the same shape into a
// dedicated <XDG_CONFIG_HOME>/kit/telemetry.yaml under bare
// `telemetry.consent`. FileStore reads that legacy file as a fallback
// when config.yaml is absent or lacks the consent block. Writes always
// go to config.yaml; the legacy file is never modified.
//
// Concurrency: an internal mutex serializes Get/Set/Clear. The file
// itself is rewritten atomically (tmp + rename) so external readers
// never observe a half-written file. We do not take an OS-level file
// lock; cross-process write contention against the consent file is
// out of scope (kit telemetry subcommands run interactively, not in
// parallel).
type FileStore struct {
	// path is the resolved canonical on-disk location
	// (<XDG_CONFIG_HOME>/kit/config.yaml). Populated once at
	// construction time via xdg.ConfigFile and reused for every
	// operation; we do not re-resolve per call to avoid the package-
	// global Reload race that the install_id implementation guards.
	path string

	// legacyPath is the pre-refactor location
	// (<XDG_CONFIG_HOME>/kit/telemetry.yaml). Read-only fallback —
	// never written to.
	legacyPath string

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
	legacy := filepath.Join(filepath.Dir(path), legacyConsentFileRel)
	return &FileStore{path: path, legacyPath: legacy}, nil
}

// Path returns the resolved file path. Useful for `kit telemetry
// status` diagnostics and for tests asserting on the resolved
// location.
func (s *FileStore) Path() string {
	return s.path
}

// Get reads and returns the current decision. Per the Store contract,
// a missing file or absent kit.telemetry.consent block returns
// Decision{State: StateUnknown} with a nil error; malformed YAML is
// surfaced as an error.
//
// Read order: canonical config.yaml first under kit.telemetry.consent;
// if the file is absent OR the consent block is missing, fall back to
// the legacy telemetry.yaml under bare telemetry.consent. The legacy
// file is never re-written; the next Set/Clear migrates to config.yaml.
func (s *FileStore) Get(ctx context.Context) (Decision, error) {
	if err := ctx.Err(); err != nil {
		return Decision{}, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()

	// Canonical path first.
	doc, err := s.readDoc(s.path)
	if err != nil && !errors.Is(err, fs.ErrNotExist) {
		return Decision{}, err
	}
	if doc != nil {
		d, found, err := extractDecision(doc)
		if err != nil {
			return Decision{}, err
		}
		if found {
			return d, nil
		}
	}

	// Legacy fallback. Malformed legacy YAML is surfaced as an error
	// (mirrors the canonical-path behavior) so callers can spot a
	// corrupt migration source.
	legacyDoc, err := s.readDoc(s.legacyPath)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return Decision{State: StateUnknown}, nil
		}
		return Decision{}, err
	}
	d, found, err := extractLegacyDecision(legacyDoc)
	if err != nil {
		return Decision{}, err
	}
	if !found {
		return Decision{State: StateUnknown}, nil
	}
	return d, nil
}

// Set persists the decision atomically into config.yaml under
// kit.telemetry.consent. Existing top-level keys (and existing keys
// under kit) other than kit.telemetry.consent are preserved verbatim;
// the consent block itself is replaced wholesale with the supplied
// Decision.
//
// Migration: when only the legacy telemetry.yaml exists at call time,
// Set always emits into config.yaml. The legacy file is left in place
// (it may carry hand-added sibling keys); subsequent Get calls prefer
// config.yaml so the legacy block becomes shadowed without being
// destroyed.
func (s *FileStore) Set(ctx context.Context, d Decision) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()

	doc, err := s.readDoc(s.path)
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

// Clear resets to StateUnknown by removing the kit.telemetry.consent
// block from config.yaml. If config.yaml does not exist, Clear is a
// no-op (success). Other top-level keys are preserved. The legacy
// telemetry.yaml is never modified.
func (s *FileStore) Clear(ctx context.Context) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()

	doc, err := s.readDoc(s.path)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return nil
		}
		return err
	}
	removeConsent(doc)
	return s.writeDoc(doc)
}

// readDoc reads and parses the YAML at path. Returns (nil,
// fs.ErrNotExist-wrapped error) when the file is absent so callers can
// branch cleanly. Returns an error for malformed YAML — see Store.Get
// contract.
func (s *FileStore) readDoc(path string) (*yaml.Node, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	if len(b) == 0 {
		return nil, nil
	}
	var doc yaml.Node
	if err := yaml.Unmarshal(b, &doc); err != nil {
		return nil, fmt.Errorf("consent: parse %s: %w", path, err)
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

	tmp, err := os.CreateTemp(filepath.Dir(s.path), ".config.yaml.tmp.*")
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
