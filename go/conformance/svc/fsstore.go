package svc

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
)

// FSStore is the filesystem-backed ScenarioStore. The layout is:
//
//	<root>/scenarios/<ns>/<id>/<version>/scenario.yaml
//	<root>/scenarios/<ns>/<id>/<version>/prompts/*.md
//	<root>/scenarios/<ns>/<id>/latest   (symlink to <version>; optional)
//
// Boot semantics: NewFSStore walks the whole tree, parses every
// scenario, refuses to start if any fail. Subsequent Get/Meta are
// served from an in-memory cache. The driver does not yet watch for
// changes; operators deploy a new pod for new scenarios.
type FSStore struct {
	root string
	mu   sync.RWMutex
	// keyed by canonical ref ("<ns>/<id>@<version>") and the resolved
	// alias ("<ns>/<id>") → latest.
	byCanonical map[string]*Scenario
	latestVer   map[string]string              // <ns>/<id> → version
	nsScenarios map[string]map[string]struct{} // ns → set of <id>s
	metas       map[string]ScenarioMeta        // <ns>/<id>@<version>
}

// NewFSStore boots a filesystem-backed store. On parse failure of any
// scenario.yaml the boot returns an error; the operator can't ship a
// broken bundle.
func NewFSStore(ctx context.Context, root string) (*FSStore, error) {
	abs, err := filepath.Abs(root)
	if err != nil {
		return nil, fmt.Errorf("fsstore: abs root: %w", err)
	}
	st, err := os.Stat(abs)
	if err != nil {
		return nil, fmt.Errorf("fsstore: stat root: %w", err)
	}
	if !st.IsDir() {
		return nil, fmt.Errorf("fsstore: root %q is not a directory", abs)
	}
	s := &FSStore{
		root:        abs,
		byCanonical: make(map[string]*Scenario),
		latestVer:   make(map[string]string),
		nsScenarios: make(map[string]map[string]struct{}),
		metas:       make(map[string]ScenarioMeta),
	}
	if err := s.scan(ctx); err != nil {
		return nil, err
	}
	return s, nil
}

// scan walks the scenarios subtree under root.
func (s *FSStore) scan(_ context.Context) error {
	scenRoot := filepath.Join(s.root, "scenarios")
	st, err := os.Stat(scenRoot)
	if err != nil {
		if os.IsNotExist(err) {
			// Empty store is valid; operator may add scenarios later.
			return nil
		}
		return fmt.Errorf("fsstore: stat scenarios/: %w", err)
	}
	if !st.IsDir() {
		return fmt.Errorf("fsstore: scenarios/ is not a directory")
	}

	nsEntries, err := os.ReadDir(scenRoot)
	if err != nil {
		return fmt.Errorf("fsstore: read scenarios/: %w", err)
	}
	for _, ne := range nsEntries {
		if !ne.IsDir() {
			continue
		}
		ns := ne.Name()
		if !ValidNamespace(ns) {
			return fmt.Errorf("fsstore: invalid namespace %q in scenarios/", ns)
		}
		nsDir := filepath.Join(scenRoot, ns)
		idEntries, err := os.ReadDir(nsDir)
		if err != nil {
			return fmt.Errorf("fsstore: read ns %q: %w", ns, err)
		}
		for _, ie := range idEntries {
			if !ie.IsDir() {
				continue
			}
			id := ie.Name()
			if !ValidID(id) {
				return fmt.Errorf("fsstore: invalid scenario id %q in %s", id, ns)
			}
			idDir := filepath.Join(nsDir, id)
			vers, err := s.scanVersions(idDir)
			if err != nil {
				return fmt.Errorf("fsstore: scan %s/%s: %w", ns, id, err)
			}
			latest := pickLatest(vers, filepath.Join(idDir, "latest"))
			for _, v := range vers {
				ref := ScenarioRef{Namespace: ns, ID: id, Version: v}
				sc, err := s.loadScenario(idDir, ref)
				if err != nil {
					return fmt.Errorf("fsstore: load %s: %w", ref.String(), err)
				}
				s.byCanonical[canonical(ref)] = sc
				s.metas[canonical(ref)] = ScenarioMeta{
					Ref:           ref,
					SchemaVersion: sc.SchemaVersion,
					Tier:          sc.Tier,
				}
			}
			if latest != "" {
				s.latestVer[ns+"/"+id] = latest
			}
			if s.nsScenarios[ns] == nil {
				s.nsScenarios[ns] = make(map[string]struct{})
			}
			s.nsScenarios[ns][id] = struct{}{}
		}
	}
	return nil
}

// scanVersions lists the version subdirs (everything except "latest").
func (s *FSStore) scanVersions(idDir string) ([]string, error) {
	entries, err := os.ReadDir(idDir)
	if err != nil {
		return nil, err
	}
	var out []string
	for _, e := range entries {
		if e.Name() == "latest" {
			continue
		}
		if !e.IsDir() {
			continue
		}
		if !ValidVersion(e.Name()) {
			return nil, fmt.Errorf("invalid version dir %q", e.Name())
		}
		out = append(out, e.Name())
	}
	sort.Strings(out)
	return out, nil
}

// loadScenario reads + minimally parses a scenario.yaml. The full
// scenario library will land via the scenario library; until then the stub
// Scenario type carries only what's needed to ferry references and
// prompt directories. We treat scenario.yaml bytes as opaque to keep
// this independent of scen's not-yet-merged parser.
func (s *FSStore) loadScenario(idDir string, ref ScenarioRef) (*Scenario, error) {
	verDir := filepath.Join(idDir, ref.Version)
	scPath := filepath.Join(verDir, "scenario.yaml")
	raw, err := os.ReadFile(scPath)
	if err != nil {
		return nil, fmt.Errorf("read scenario.yaml: %w", err)
	}
	sc := &Scenario{
		SchemaVersion: "1",
		Namespace:     ref.Namespace,
		ID:            ref.ID,
		Version:       ref.Version,
		Tier:          1,
		Raw:           raw,
	}
	return sc, nil
}

// pickLatest picks the latest version. If a "latest" symlink exists,
// resolve it. Otherwise pick the lexicographically largest version.
func pickLatest(versions []string, latestPath string) string {
	if dst, err := os.Readlink(latestPath); err == nil {
		base := filepath.Base(dst)
		if ValidVersion(base) {
			return base
		}
	}
	if len(versions) == 0 {
		return ""
	}
	// Pick the largest version lexicographically.
	return versions[len(versions)-1]
}

// canonical is the cache key for an explicit ref.
func canonical(r ScenarioRef) string { return r.Namespace + "/" + r.ID + "@" + r.Version }

// Get implements ScenarioStore.
func (s *FSStore) Get(_ context.Context, ref ScenarioRef) (*Scenario, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	resolved, err := s.resolve(ref)
	if err != nil {
		return nil, err
	}
	sc, ok := s.byCanonical[canonical(resolved)]
	if !ok {
		return nil, ErrScenarioNotFound
	}
	// Return a copy so callers can't mutate the cache.
	out := *sc
	return &out, nil
}

// Meta implements ScenarioStore.
func (s *FSStore) Meta(_ context.Context, ref ScenarioRef) (*ScenarioMeta, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	resolved, err := s.resolve(ref)
	if err != nil {
		return nil, err
	}
	m, ok := s.metas[canonical(resolved)]
	if !ok {
		return nil, ErrScenarioNotFound
	}
	return &m, nil
}

// Prompt implements ScenarioStore.
func (s *FSStore) Prompt(_ context.Context, ref ScenarioRef, promptRef string) (string, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	resolved, err := s.resolve(ref)
	if err != nil {
		return "", err
	}
	// Reject traversal.
	cleaned := filepath.ToSlash(filepath.Clean(promptRef))
	if strings.HasPrefix(cleaned, "/") || strings.HasPrefix(cleaned, "../") || strings.Contains(cleaned, "/../") {
		return "", fmt.Errorf("prompt ref %q escapes scenario root", promptRef)
	}
	scDir := filepath.Join(s.root, "scenarios", resolved.Namespace, resolved.ID, resolved.Version)
	p := filepath.Join(scDir, filepath.FromSlash(cleaned))
	abs, _ := filepath.Abs(p)
	scAbs, _ := filepath.Abs(scDir)
	if !strings.HasPrefix(abs, scAbs+string(os.PathSeparator)) {
		return "", fmt.Errorf("prompt ref %q escapes scenario root", promptRef)
	}
	b, err := os.ReadFile(p)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return "", ErrScenarioNotFound
		}
		return "", err
	}
	return string(b), nil
}

// List implements ScenarioStore.
func (s *FSStore) List(_ context.Context, ns string) ([]ScenarioMeta, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	ids := s.nsScenarios[ns]
	if ids == nil {
		return nil, nil
	}
	var out []ScenarioMeta
	for id := range ids {
		latest := s.latestVer[ns+"/"+id]
		if m, ok := s.metas[ns+"/"+id+"@"+latest]; ok {
			out = append(out, m)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Ref.ID < out[j].Ref.ID })
	return out, nil
}

// Namespaces implements ScenarioStore.
func (s *FSStore) Namespaces(_ context.Context) ([]string, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := make([]string, 0, len(s.nsScenarios))
	for ns := range s.nsScenarios {
		out = append(out, ns)
	}
	sort.Strings(out)
	return out, nil
}

// resolve fills in the "latest" version when ref.Version is empty.
// Callers hold mu.
func (s *FSStore) resolve(ref ScenarioRef) (ScenarioRef, error) {
	if ref.Version != "" {
		return ref, nil
	}
	v, ok := s.latestVer[ref.Namespace+"/"+ref.ID]
	if !ok {
		return ref, ErrScenarioNotFound
	}
	out := ref
	out.Version = v
	return out, nil
}
