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
// adopters may have added by hand; only Clear touches it, removing the
// consent block alone so a reset sticks.
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

// FileStore is the default Store and GrantStore. Grants, telemetry
// included once written, live in the per-tool grants file
// <XDG_STATE_HOME>/<tool>/consent/grants.yaml. The legacy telemetry
// record lives in the kit AppConfig, <XDG_CONFIG_HOME>/kit/config.yaml
// at kit.telemetry.consent (an earlier layout used a dedicated
// <XDG_CONFIG_HOME>/kit/telemetry.yaml under bare telemetry.consent);
// both are read as a fallback, the config.yaml record is mirrored on
// every telemetry write, and Clear strips both. Edits of config.yaml
// preserve every other key via yaml.Node round-tripping.
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
	// (<XDG_CONFIG_HOME>/kit/telemetry.yaml). Read fallback; only
	// Clear writes it, to remove the consent block.
	legacyPath string

	// grantsPath is the per-tool grants file
	// (<XDG_STATE_HOME>/<tool>/consent/grants.yaml): the source of
	// truth for every grant, telemetry included once written.
	grantsPath string

	// mu serializes file access within a single process.
	mu sync.Mutex
}

// NewFileStore constructs the store for DefaultTool(). The returned
// FileStore does not touch the filesystem; the first call does.
func NewFileStore() (*FileStore, error) { return NewFileStoreFor(DefaultTool()) }

// NewFileStoreFor constructs the store for tool: grants at
// <XDG_STATE_HOME>/<tool>/consent/grants.yaml, the legacy telemetry
// record at <XDG_CONFIG_HOME>/kit/config.yaml and kit/telemetry.yaml.
func NewFileStoreFor(tool string) (*FileStore, error) {
	if err := validateTool(tool); err != nil {
		return nil, err
	}
	path, err := xdg.ConfigFile(xdgTool, consentFileRel)
	if err != nil {
		return nil, fmt.Errorf("consent: resolve path: %w", err)
	}
	grants, err := stateFile(tool, grantsFileName)
	if err != nil {
		return nil, err
	}
	legacy := filepath.Join(filepath.Dir(path), legacyConsentFileRel)
	return &FileStore{path: path, legacyPath: legacy, grantsPath: grants}, nil
}

// GrantsPath returns the per-tool grants file path.
func (s *FileStore) GrantsPath() string { return s.grantsPath }

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
// Read order: the grants file, then kit.telemetry.consent in
// config.yaml, then telemetry.consent in the legacy telemetry.yaml.
// Reads never write.
func (s *FileStore) Get(ctx context.Context) (Decision, error) {
	if err := ctx.Err(); err != nil {
		return Decision{}, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.getLocked()
}

func (s *FileStore) getLegacyLocked() (Decision, error) {
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

// Set persists the decision as the telemetry grant in the grants file
// (the source of truth from then on), then mirrors it into
// kit.telemetry.consent in config.yaml, preserving every other key, so
// SDKs that only read the legacy record agree. A config.yaml that does
// not parse is left untouched. Both writes are atomic (tmp + rename).
func (s *FileStore) Set(ctx context.Context, d Decision) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.setLocked(d)
}

func (s *FileStore) setLegacyLocked(d Decision) error {
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
	return s.writeDoc(s.path, doc)
}

// Clear resets to StateUnknown by removing the telemetry grant from
// the grants file and the consent block from config.yaml (kit.telemetry.consent) and from the legacy
// telemetry.yaml (telemetry.consent), so a reset sticks on installs
// that only carry the legacy file. Other keys in both files are
// preserved; a missing file is skipped.
func (s *FileStore) Clear(ctx context.Context) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.clearLocked()
}

func (s *FileStore) clearLegacyLocked() error {
	doc, err := s.readDoc(s.path)
	switch {
	case errors.Is(err, fs.ErrNotExist):
	case err != nil:
		return err
	case doc != nil:
		removeConsent(doc)
		if err := s.writeDoc(s.path, doc); err != nil {
			return err
		}
	}

	legacy, err := s.readDoc(s.legacyPath)
	switch {
	case errors.Is(err, fs.ErrNotExist):
		return nil
	case err != nil:
		return err
	}
	if _, found, _ := extractLegacyDecision(legacy); !found {
		return nil
	}
	deleteMappingChild(mappingChild(rootMapping(legacy), keyTelemetry), keyConsent)
	return s.writeDoc(s.legacyPath, legacy)
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
		return nil, fmt.Errorf("consent: parse %s: %w: %w", path, errParse, err)
	}
	return &doc, nil
}

// writeDoc serializes doc and writes it atomically (tmp + rename) with
// the required 0600 perms. The parent dir is created on demand with
// 0700 perms — xdg.ConfigFile already creates the parents per its
// docs, but we enforce the mode explicitly because the underlying
// library uses its own defaults.
func (s *FileStore) writeDoc(path string, doc *yaml.Node) error {
	b, err := yaml.Marshal(doc)
	if err != nil {
		return fmt.Errorf("consent: marshal: %w", err)
	}
	return writeFileAtomic(path, b)
}

// writeFileAtomic writes b to a temp sibling, fsyncs, sets 0600 and
// renames over path, so readers never see a half-written file.
func writeFileAtomic(path string, b []byte) error {
	if err := ensureDir(filepath.Dir(path)); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(filepath.Dir(path), "."+filepath.Base(path)+".tmp.*")
	if err != nil {
		return fmt.Errorf("consent: create tmp: %w", err)
	}
	tmpPath := tmp.Name()
	defer func() { _ = os.Remove(tmpPath) }()

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
	if err := os.Rename(tmpPath, path); err != nil {
		return fmt.Errorf("consent: rename: %w", err)
	}
	if err := os.Chmod(path, filePerm); err != nil {
		return fmt.Errorf("consent: chmod final: %w", err)
	}
	return nil
}

// ensureDir creates dir; the per-tool consent state dir is forced to
// 0700 even when something else created it first.
func ensureDir(dir string) error {
	if err := os.MkdirAll(dir, dirPerm); err != nil {
		return fmt.Errorf("consent: mkdir: %w", err)
	}
	if filepath.Base(dir) == consentStateDir {
		if err := os.Chmod(dir, dirPerm); err != nil {
			return fmt.Errorf("consent: chmod dir: %w", err)
		}
	}
	return nil
}

// errParse marks a legacy file that is not valid YAML.
var errParse = errors.New("not valid YAML")
