package oauth

import (
	"context"
	"reflect"
	"sync"
	"sync/atomic"

	"hop.top/kit/go/storage/secret"
)

// A rotated refresh token that the store has not accepted yet is the only
// valid one: the provider already killed the stored one. It is held at
// package level so every Source of the process on the same store, ref and
// grant uses and flushes it, not just the Source that received it. Another
// process cannot see it; see the concept page.
//
// Isolation: entries are keyed by the store value as well as ref, secret_ref
// and grant digest, so two stores in one process (two tenants) never share a
// token. A store whose dynamic type is not comparable is not shared at all:
// its entry is private to the Source.
//
// Fail closed: the pending token is used only after the stored refresh token
// was read and found to be one the pending token superseded. If the stored
// token cannot be read, only the Source that received the pending token
// (write-back-failure recovery) may still use it; any other Source returns
// the read error.
var (
	pendMu  sync.Mutex
	pending = map[pendingKeyT]*pendingRotation{}
	pendSeq atomic.Uint64
)

type pendingKeyT struct {
	store                  secret.Store // nil: not shareable, id disambiguates
	id                     uint64
	ref, secretRef, digest string
}

// pendingRotation is a rotated token plus every refresh token it superseded
// while pending. The stored token equal to one of those means the write-back
// is still owed; any other stored value means a fresh consent or another
// writer replaced it, and the pending token is stale.
type pendingRotation struct {
	rt         string
	superseded map[string]struct{}
	owner      *Source // the Source that received rt
}

func newPendingKey(store secret.Store, ref string, g Grant) pendingKeyT {
	k := pendingKeyT{ref: ref, secretRef: g.SecretRef, digest: Digest(g)}
	if t := reflect.TypeOf(store); t != nil && t.Comparable() {
		k.store = store
	} else {
		k.id = pendSeq.Add(1)
	}
	return k
}

// setPending records rotated, received in exchange for sent.
func (s *Source) setPending(rotated, sent string) {
	pendMu.Lock()
	defer pendMu.Unlock()
	e := pending[s.pkey]
	if e == nil {
		e = &pendingRotation{superseded: map[string]struct{}{}}
		pending[s.pkey] = e
	}
	e.superseded[sent] = struct{}{}
	e.rt = rotated
	e.owner = s
}

// clearPending drops the pending token when it still is rt.
func (s *Source) clearPending(rt string) {
	pendMu.Lock()
	defer pendMu.Unlock()
	if e := pending[s.pkey]; e != nil && e.rt == rt {
		delete(pending, s.pkey)
	}
}

// pendingRT returns the pending rotated token, "" when none or stale. It
// fails closed: see the rules above.
func (s *Source) pendingRT(ctx context.Context) (string, error) {
	pendMu.Lock()
	e := pending[s.pkey]
	var rt string
	var owned bool
	var sup map[string]struct{}
	if e != nil {
		rt, owned = e.rt, e.owner == s
		sup = make(map[string]struct{}, len(e.superseded))
		for k := range e.superseded {
			sup[k] = struct{}{}
		}
	}
	pendMu.Unlock()
	if rt == "" {
		return "", nil
	}
	cur, err := readSecret(ctx, s.store, s.g.SecretRef, "secret_ref")
	if err != nil {
		if owned {
			return rt, nil
		}
		return "", err
	}
	if _, ok := sup[cur]; ok {
		return rt, nil
	}
	// Stored already is the pending token (flushed by a sibling), or was
	// replaced by something newer.
	s.clearPending(rt)
	return "", nil
}
