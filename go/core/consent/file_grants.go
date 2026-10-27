package consent

import (
	"context"
	"errors"
	"io/fs"
	"os"
	"syscall"

	"gopkg.in/yaml.v3"
)

// FileStore implements GrantStore over the per-tool grants file
// (<XDG_STATE_HOME>/<tool>/consent/grants.yaml). config.yaml is never
// read for grants, so a typo there cannot refuse a capability, and a
// grant write cannot clobber it.
//
// Telemetry compat: the telemetry capability reads the grants file
// first, then the legacy kit.telemetry.consent record (config.yaml,
// then telemetry.yaml). A telemetry write lands in the grants file,
// the source of truth from then on, and is mirrored into
// kit.telemetry.consent so SDKs that only know the legacy record keep
// the same answer. Clear (and revoking telemetry) clears all three.
var _ GrantStore = (*FileStore)(nil)

// Grants returns every grant in the grants file, plus the legacy
// telemetry record when the file has no telemetry grant. An unreadable
// legacy record is left out here; Get (and so a telemetry check)
// reports it.
func (s *FileStore) Grants(ctx context.Context) ([]Grant, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()

	gs, err := s.readGrants()
	if err != nil {
		return nil, err
	}
	if !hasTelemetry(gs) {
		if d, err := s.getLegacyLocked(); err == nil {
			if tg, ok := telemetryGrant(d); ok {
				gs = append(gs, tg)
			}
		}
	}
	if gs == nil {
		gs = []Grant{}
	}
	sortGrants(gs)
	return gs, nil
}

// PutGrant replaces the grant for g's key in the grants file.
func (s *FileStore) PutGrant(ctx context.Context, g Grant) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if err := checkPersistable(g); err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if g.Capability == CapTelemetry {
		return s.setLocked(telemetryDecision(g))
	}
	return s.putGrantLocked(g)
}

// DeleteGrant removes the grant for the key. Deleting telemetry clears
// the grants file entry and both legacy records.
func (s *FileStore) DeleteGrant(ctx context.Context, c Capability, subject string) (bool, error) {
	if err := ctx.Err(); err != nil {
		return false, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()

	if c == CapTelemetry {
		d, err := s.getLocked()
		if err != nil {
			return false, asStoreError(err)
		}
		_, existed := telemetryGrant(d)
		if err := s.clearLocked(); err != nil {
			return false, err
		}
		return existed, nil
	}
	gs, err := s.readGrants()
	if err != nil {
		return false, err
	}
	kept := removeKey(gs, grantKey{c, subject})
	if len(kept) == len(gs) {
		return false, nil
	}
	return true, s.writeGrants(kept)
}

func (s *FileStore) putGrantLocked(g Grant) error {
	gs, err := s.readGrants()
	if err != nil {
		return err
	}
	return s.writeGrants(append(removeKey(gs, g.key()), g))
}

// getLocked is the telemetry Decision: grants file first, then legacy.
func (s *FileStore) getLocked() (Decision, error) {
	gs, err := s.readGrants()
	if err != nil {
		return Decision{}, err
	}
	for _, g := range gs {
		if g.Capability == CapTelemetry {
			return telemetryDecision(g), nil
		}
	}
	return s.getLegacyLocked()
}

// setLocked writes the telemetry Decision to the grants file, then
// mirrors it into the legacy record. A config.yaml that does not parse
// is left alone (never clobbered) rather than failing the write.
func (s *FileStore) setLocked(d Decision) error {
	g, ok := telemetryGrant(d)
	if !ok {
		return grantErr(KindInvalidDecision, "telemetry state %q is not granted or denied", d.State)
	}
	if err := s.putGrantLocked(g); err != nil {
		return err
	}
	if err := s.setLegacyLocked(d); err != nil && !errors.Is(err, errParse) {
		return err
	}
	return nil
}

// clearLocked removes telemetry from the grants file and both legacy
// files. A malformed grants file fails before anything is touched.
func (s *FileStore) clearLocked() error {
	gs, err := s.readGrants()
	if err != nil {
		return err
	}
	if hasTelemetry(gs) {
		if err := s.writeGrants(removeKey(gs, grantKey{CapTelemetry, ""})); err != nil {
			return err
		}
	}
	return s.clearLegacyLocked()
}

// readGrants decodes the grants file; a missing file holds no grants
// and any read or parse failure is malformed_store.
func (s *FileStore) readGrants() ([]Grant, error) {
	b, err := os.ReadFile(s.grantsPath)
	// ENOTDIR: a path component is a regular file, so the file cannot exist.
	if errors.Is(err, fs.ErrNotExist) || errors.Is(err, syscall.ENOTDIR) {
		return nil, nil
	}
	if err != nil {
		return nil, &GrantError{Kind: KindMalformedStore, Err: err}
	}
	if len(b) == 0 {
		return nil, nil
	}
	var doc yaml.Node
	if err := yaml.Unmarshal(b, &doc); err != nil {
		return nil, &GrantError{Kind: KindMalformedStore, Msg: s.grantsPath, Err: err}
	}
	return decodeGrants(&doc)
}

func (s *FileStore) writeGrants(gs []Grant) error {
	return s.writeDoc(s.grantsPath, encodeGrants(gs))
}

func hasTelemetry(gs []Grant) bool {
	for _, g := range gs {
		if g.Capability == CapTelemetry {
			return true
		}
	}
	return false
}

// asStoreError keeps GrantErrors and classifies the rest (a legacy
// parse failure) as malformed_store.
func asStoreError(err error) error {
	if KindOf(err) != "" {
		return err
	}
	return &GrantError{Kind: KindMalformedStore, Msg: "telemetry consent", Err: err}
}

func removeKey(gs []Grant, k grantKey) []Grant {
	out := make([]Grant, 0, len(gs))
	for _, g := range gs {
		if g.key() != k {
			out = append(out, g)
		}
	}
	return out
}
