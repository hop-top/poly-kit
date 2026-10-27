package consent

import (
	"context"
	"errors"
	"io/fs"

	"gopkg.in/yaml.v3"
)

// FileStore implements GrantStore over the same config.yaml as Store:
// grants in the kit.consent partition, and the telemetry capability as
// the kit.telemetry.consent record (legacy telemetry.yaml fallback
// included), so SDKs that only know telemetry consent keep reading it.
var _ GrantStore = (*FileStore)(nil)

// Grants returns the telemetry grant (when a record exists) and every
// grant in kit.consent, sorted.
func (s *FileStore) Grants(ctx context.Context) ([]Grant, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()

	doc, err := s.readGrantDoc()
	if err != nil {
		return nil, err
	}
	gs, err := decodeGrants(doc)
	if err != nil {
		return nil, err
	}
	d, err := s.getLocked()
	if err != nil {
		return nil, &GrantError{Kind: KindMalformedStore, Msg: "telemetry consent", Err: err}
	}
	if tg, ok := telemetryGrant(d); ok {
		gs = append(gs, tg)
	}
	if gs == nil {
		gs = []Grant{}
	}
	sortGrants(gs)
	return gs, nil
}

// PutGrant writes g. A telemetry grant writes kit.telemetry.consent;
// any other replaces its entry in kit.consent.
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
	doc, err := s.readGrantDoc()
	if err != nil {
		return err
	}
	gs, err := decodeGrants(doc)
	if err != nil {
		return err
	}
	if doc == nil {
		doc = newMappingDoc()
	}
	gs = append(removeKey(gs, g.key()), g)
	if err := encodeGrants(doc, gs); err != nil {
		return err
	}
	return s.writeDoc(s.path, doc)
}

// DeleteGrant removes the grant for the key. Deleting telemetry clears
// the record in config.yaml and in the legacy file.
func (s *FileStore) DeleteGrant(ctx context.Context, c Capability, subject string) (bool, error) {
	if err := ctx.Err(); err != nil {
		return false, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()

	if c == CapTelemetry {
		d, err := s.getLocked()
		if err != nil {
			return false, &GrantError{Kind: KindMalformedStore, Msg: "telemetry consent", Err: err}
		}
		_, existed := telemetryGrant(d)
		if !existed {
			return false, nil
		}
		return true, s.clearLocked()
	}
	doc, err := s.readGrantDoc()
	if err != nil || doc == nil {
		return false, err
	}
	gs, err := decodeGrants(doc)
	if err != nil {
		return false, err
	}
	kept := removeKey(gs, grantKey{c, subject})
	if len(kept) == len(gs) {
		return false, nil
	}
	if err := encodeGrants(doc, kept); err != nil {
		return false, err
	}
	return true, s.writeDoc(s.path, doc)
}

// readGrantDoc reads config.yaml; a missing file is (nil, nil) and any
// other failure is malformed_store.
func (s *FileStore) readGrantDoc() (*yaml.Node, error) {
	doc, err := s.readDoc(s.path)
	if errors.Is(err, fs.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, &GrantError{Kind: KindMalformedStore, Err: err}
	}
	return doc, nil
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
