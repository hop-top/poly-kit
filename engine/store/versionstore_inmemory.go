package store

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"log"
	"math"
	"sync"
	"time"

	"hop.top/kit/go/core/util"
	"hop.top/kit/go/runtime/domain/version"
)

// inMemoryVersionStore is the in-process implementation of
// [VersionStore]. It is the historical backing for
// [VersionedDocumentStore] — fast, simple, zero-dependency, and
// ephemeral: every entry is lost on process restart. Kept as a
// first-class backend for tests and ephemeral uses where restart
// loss is acceptable.
//
// Mirrors the SQLite backend's content-addressed dedup shape from
// spec `engine-snapshot-dedup` §3 #3: snapshot bytes live in a
// `blobs` map keyed by hash with a refcount, and a per-version
// `versionToHash` indirection lets GetSnapshot resolve a versionID
// to its blob. The conformance principle requires both backends to
// produce identical observable behavior; the same sentinels
// (ErrHashCollision, ErrRefcountOverflow, ErrRefcountUnderflow) are
// raised under the same conditions.
type inMemoryVersionStore struct {
	mu            sync.RWMutex
	dags          map[string]*version.DAG // key: "type:id"
	versions      map[string][]Version    // key: "type:id" -> ordered versions
	blobs         map[string]blobEntry    // key: snapshot hash
	versionToHash map[string]string       // versionID -> snapshot hash

	// nextSeq is the per-(type, id) high-water "next seq to issue"
	// counter. AppendVersion bumps it monotonically; DeleteVersions
	// (Prune) does NOT decrement it. This guarantees that the seq
	// numbers issued for a given (type, id) never repeat across
	// AppendVersion -> Prune -> AppendVersion lifecycles, so the
	// version_id derived from "type:id-seq-data" cannot collide with
	// one already issued for a since-pruned version. Mirrors the
	// SQLite backend's version_seq_high_water table. DeleteHistory
	// drops the entry: the entire document is gone, so the next
	// AppendVersion under the same (type, id) starts a fresh
	// lifecycle at seq=1.
	nextSeq map[string]int

	// dead is the sparse live/dead-head map: a versionID is in this
	// map iff it has been explicitly marked dead (live=false). Absent
	// keys are treated as live — matches the SQLite schema's
	// `live BOOLEAN NOT NULL DEFAULT TRUE` and keeps the map small
	// (only abandoned heads contribute entries).
	dead map[string]struct{}
}

// blobEntry is the in-memory analog of a snapshot_blobs row.
type blobEntry struct {
	data     json.RawMessage
	refcount int64
}

// NewInMemoryVersionStore returns a [VersionStore] that holds all
// state in process memory. Useful for tests, dev convenience, and
// callers that explicitly opt into ephemeral version history.
func NewInMemoryVersionStore() VersionStore {
	return &inMemoryVersionStore{
		dags:          make(map[string]*version.DAG),
		versions:      make(map[string][]Version),
		blobs:         make(map[string]blobEntry),
		versionToHash: make(map[string]string),
		nextSeq:       make(map[string]int),
		dead:          make(map[string]struct{}),
	}
}

// AppendVersion implements VersionStore. Mirrors the SQLite backend:
// hash the payload, look up an existing blob, bump refcount on a
// match (or surface ErrHashCollision on a hash collision with
// distinct bytes), insert otherwise.
func (s *inMemoryVersionStore) AppendVersion(_ context.Context, docType, id string, data json.RawMessage, parents []string) (Version, error) {
	key := docKey(docType, id)
	s.mu.Lock()
	defer s.mu.Unlock()

	dag, ok := s.dags[key]
	if !ok {
		dag = version.NewDAG()
		s.dags[key] = dag
	}

	existing := s.versions[key]
	// Derive seq from the high-water nextSeq counter, not
	// len(existing)+1: Prune (DeleteVersions) shrinks the slice but
	// must NOT reissue a seq already used by a since-pruned version
	// (the version_id is util.Short("type:id-seq-data"), so seq reuse
	// collides on version_id). The first append for a (type, id)
	// reads nextSeq[key]==0 and lands at seq=1; later appends bump
	// monotonically.
	seq := s.nextSeq[key] + 1
	vid := util.Short([]byte(fmt.Sprintf("%s-%d-%s", key, seq, data)), 16)
	hash := util.Short(data, 16)

	// Dedup-aware blob upsert (spec §3 #1, #4). The bytes
	// comparison protects against the hash-collision corner case
	// (decision #1); the overflow guard mirrors the SQLite WHERE
	// refcount < INT64_MAX clause (decision #4).
	if entry, ok := s.blobs[hash]; ok {
		if !bytes.Equal(entry.data, data) {
			return Version{}, ErrHashCollision
		}
		if entry.refcount == math.MaxInt64 {
			return Version{}, ErrRefcountOverflow
		}
		entry.refcount++
		s.blobs[hash] = entry
	} else {
		s.blobs[hash] = blobEntry{
			data:     append(json.RawMessage(nil), data...),
			refcount: 1,
		}
	}

	now := time.Now()
	if err := dag.Append(version.Version{
		ID:        vid,
		ParentIDs: append([]string(nil), parents...),
		Timestamp: now.UnixNano(),
		Hash:      hash,
	}); err != nil {
		// Roll back the dedup mutation we just made — keep the
		// blob/refcount invariant intact when the DAG rejects the
		// append (e.g. unknown parent). If the rollback itself
		// fails, surface it alongside the original DAG error;
		// silently swallowing would mask state corruption.
		if rerr := s.unrefBlob(hash); rerr != nil {
			return Version{}, fmt.Errorf("store: append version: %w (rollback also failed: %v)", err, rerr)
		}
		return Version{}, fmt.Errorf("store: append version: %w", err)
	}

	v := Version{
		Type:      docType,
		ID:        id,
		VersionID: vid,
		Seq:       seq,
		Data:      append(json.RawMessage(nil), data...),
		CreatedAt: now.UTC().Format(time.RFC3339Nano),
		Live:      true, // every version is born live; explicit Abandon flips it
	}
	s.versions[key] = append(existing, v)
	s.versionToHash[vid] = hash
	// Commit the high-water counter only after the DAG accepted the
	// append. If we'd advanced before the dag.Append rollback path,
	// a rejected append (e.g. unknown parent) would leak a seq
	// number — harmless for monotonicity but a confusing footgun.
	s.nextSeq[key] = seq

	return v, nil
}

// ListVersions implements VersionStore.
//
// Each returned [Version] has Live populated from the per-store dead
// map: absent entry → Live=true (the default; freshly appended
// versions are live), present entry → Live=false. The stored slice
// always carries Live=true (the value that AppendVersion writes), so
// the dead-map lookup is the single source of truth on read.
func (s *inMemoryVersionStore) ListVersions(_ context.Context, docType, id string) ([]Version, error) {
	key := docKey(docType, id)
	s.mu.RLock()
	defer s.mu.RUnlock()

	src := s.versions[key]
	if len(src) == 0 {
		return nil, nil
	}
	// Defensive copy so callers can't mutate our backing slice.
	out := make([]Version, len(src))
	copy(out, src)
	for i := range out {
		if _, dead := s.dead[out[i].VersionID]; dead {
			out[i].Live = false
		} else {
			out[i].Live = true
		}
	}
	return out, nil
}

// GetSnapshot implements VersionStore. Resolves versionID through
// the indirection map, then returns a defensive copy of the blob
// payload.
func (s *inMemoryVersionStore) GetSnapshot(_ context.Context, versionID string) (json.RawMessage, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	hash, ok := s.versionToHash[versionID]
	if !ok {
		return nil, fmt.Errorf("store: snapshot %s not found", versionID)
	}
	entry, ok := s.blobs[hash]
	if !ok {
		// Indicates a corruption in the join — the version maps to
		// a hash that has no blob. Mirrors the same defense-in-depth
		// as the SQLite backend's underflow guard.
		return nil, fmt.Errorf("store: snapshot %s: blob missing for hash %s", versionID, hash)
	}
	return append(json.RawMessage(nil), entry.data...), nil
}

// DeleteHistory implements VersionStore. No-op when there is no
// recorded history for (docType, id). Decrements per-blob refcounts
// and deletes blob entries whose count reaches zero (spec §3 #5).
func (s *inMemoryVersionStore) DeleteHistory(_ context.Context, docType, id string) error {
	key := docKey(docType, id)
	s.mu.Lock()
	defer s.mu.Unlock()

	for _, v := range s.versions[key] {
		hash, ok := s.versionToHash[v.VersionID]
		if !ok {
			// Missing indirection row = corruption parity with the
			// SQLite backend's missing-blob path.
			log.Printf("store: delete history: refcount underflow for version=%s (hash mapping missing)", v.VersionID)
			return fmt.Errorf("%w: version=%s hash mapping missing", ErrRefcountUnderflow, v.VersionID)
		}
		delete(s.versionToHash, v.VersionID)
		delete(s.dead, v.VersionID)
		if err := s.unrefBlob(hash); err != nil {
			return err
		}
	}
	delete(s.dags, key)
	delete(s.versions, key)
	// DeleteHistory removes the document outright; resetting the
	// high-water counter lets a fresh AppendVersion under the same
	// (type, id) start at seq=1 — DeleteVersions/Prune deliberately
	// does NOT do this so cross-Prune monotonicity holds.
	delete(s.nextSeq, key)
	return nil
}

// unrefBlob decrements the refcount on a blob, deleting the entry
// when it reaches zero. Returns ErrRefcountUnderflow if the entry
// is missing or the count is already zero.
func (s *inMemoryVersionStore) unrefBlob(hash string) error {
	entry, ok := s.blobs[hash]
	if !ok {
		log.Printf("store: refcount underflow for hash=%s (blob entry missing)", hash)
		return fmt.Errorf("%w: hash=%s entry missing", ErrRefcountUnderflow, hash)
	}
	if entry.refcount <= 0 {
		log.Printf("store: refcount underflow for hash=%s (current=%d)", hash, entry.refcount)
		return fmt.Errorf("%w: hash=%s current=%d", ErrRefcountUnderflow, hash, entry.refcount)
	}
	if entry.refcount == 1 {
		delete(s.blobs, hash)
		return nil
	}
	entry.refcount--
	s.blobs[hash] = entry
	return nil
}

// DeleteVersions implements VersionStore. Removes the named versions
// for (docType, id), evicts the per-doc DAG cache (callers refresh
// via LoadDAG, which lazily rebuilds), and decrements blob refcounts
// per affected hash via [unrefBlob]. Returns the (hash, bytes) pairs
// for blobs whose count hit zero.
//
// versionIDs not present for (docType, id) are silently ignored.
// An empty versionIDs slice is a no-op.
func (s *inMemoryVersionStore) DeleteVersions(_ context.Context, docType, id string, versionIDs []string) ([]FreedBlob, error) {
	if len(versionIDs) == 0 {
		return nil, nil
	}
	key := docKey(docType, id)
	s.mu.Lock()
	defer s.mu.Unlock()

	doomed := make(map[string]struct{}, len(versionIDs))
	for _, vid := range versionIDs {
		doomed[vid] = struct{}{}
	}

	existing := s.versions[key]
	if len(existing) == 0 {
		return nil, nil
	}

	// 1. Filter the per-doc versions slice; collect the hashes that
	//    will need refcount-decrementing.
	kept := make([]Version, 0, len(existing))
	hashes := make([]string, 0, len(versionIDs))
	for _, v := range existing {
		if _, drop := doomed[v.VersionID]; drop {
			h, ok := s.versionToHash[v.VersionID]
			if !ok {
				log.Printf("store: prune: refcount underflow for version=%s (hash mapping missing)", v.VersionID)
				return nil, fmt.Errorf("%w: version=%s hash mapping missing", ErrRefcountUnderflow, v.VersionID)
			}
			hashes = append(hashes, h)
			delete(s.versionToHash, v.VersionID)
			// Drop the dead-map entry for the doomed version. Leaving
			// it would orphan a key (the version row goes away but the
			// dead bit lingers) — harmless functionally but leaks
			// memory across many prune cycles.
			delete(s.dead, v.VersionID)
			continue
		}
		kept = append(kept, v)
	}
	if len(kept) == 0 {
		// Caller has guaranteed (via Prune's head-retention rule)
		// that this branch is unreachable; defend in depth and refuse
		// to leave the doc with no versions. Restoring the original
		// slice keeps the store self-consistent for the caller's
		// error path.
		return nil, fmt.Errorf("store: prune: refusing to remove all versions of %s/%s", docType, id)
	}
	s.versions[key] = kept

	// 2. Rebuild the per-doc DAG from the surviving versions. Cheaper
	//    than incrementally surgery on the existing DAG (which would
	//    require exposing internals for parent-edge removal); the
	//    in-memory backend reconstructs the same way LoadDAG would
	//    if it were lazy.
	newDAG := version.NewDAG()
	for _, v := range kept {
		// Lookup the original parent_ids from the DAG before we
		// replace it. Surviving versions keep their original parent
		// edges byte-for-byte (spec §3: pruning never rewrites
		// retained versions' parent_ids).
		parents := s.parentsOf(key, v.VersionID)
		if err := newDAG.Append(version.Version{
			ID:        v.VersionID,
			ParentIDs: parents,
			Timestamp: parseTimestamp(v.CreatedAt),
			Hash:      s.hashOfRetained(v.VersionID),
		}); err != nil {
			return nil, fmt.Errorf("store: prune: rebuild dag: %w", err)
		}
	}
	s.dags[key] = newDAG

	// 3. Decrement refcounts and accumulate freed blobs. unrefBlob
	//    deletes the entry on count==0; we read the size BEFORE
	//    unrefBlob, then check post-unref whether the entry survived.
	var freed []FreedBlob
	for _, h := range hashes {
		entry, ok := s.blobs[h]
		if !ok {
			log.Printf("store: prune: refcount underflow for hash=%s (blob entry missing)", h)
			return nil, fmt.Errorf("%w: hash=%s entry missing", ErrRefcountUnderflow, h)
		}
		size := int64(len(entry.data))
		if err := s.unrefBlob(h); err != nil {
			return nil, err
		}
		if _, stillThere := s.blobs[h]; !stillThere {
			freed = append(freed, FreedBlob{Hash: h, Bytes: size})
		}
	}
	return freed, nil
}

// parentsOf returns the parent IDs the existing DAG records for vid.
// Used by DeleteVersions to copy parent edges into the rebuilt DAG.
func (s *inMemoryVersionStore) parentsOf(key, vid string) []string {
	dag, ok := s.dags[key]
	if !ok {
		return nil
	}
	v, ok := dag.Get(vid)
	if !ok {
		return nil
	}
	return append([]string(nil), v.ParentIDs...)
}

// hashOfRetained returns the snapshot hash recorded for vid. Caller
// holds s.mu.
func (s *inMemoryVersionStore) hashOfRetained(vid string) string {
	return s.versionToHash[vid]
}

// parseTimestamp parses an RFC3339Nano timestamp into a Unix-nano
// int64; returns 0 on parse error (the rebuilt DAG only uses the
// timestamp for tie-breaks, so a zero-value here is benign in
// practice).
func parseTimestamp(s string) int64 {
	t, err := time.Parse(time.RFC3339Nano, s)
	if err != nil {
		return 0
	}
	return t.UnixNano()
}

// SetLive implements VersionStore. Validates that versionID is a
// current head (no children in the DAG) and toggles the dead-map
// entry to match the requested liveness.
//
// Convention: absent dead-map entry = live; present entry = dead.
// SetLive(true) deletes the entry; SetLive(false) inserts it.
// Idempotent (SetLive to already-current state is a successful
// no-op).
//
// The "at least one live head" invariant is NOT enforced here —
// public callers (Abandon) check it pre-flight; internal callers
// (Merge / Revert flipping a head dead) atomically create a new live
// head in the same transaction, so a transient zero-live-heads window
// would not be observable through ListVersions.
func (s *inMemoryVersionStore) SetLive(_ context.Context, docType, id, versionID string, live bool) error {
	key := docKey(docType, id)
	s.mu.Lock()
	defer s.mu.Unlock()

	// Validate the version exists for this (docType, id). The DAG is
	// the source of truth; without an entry there's nothing to flip.
	dag, ok := s.dags[key]
	if !ok {
		return fmt.Errorf("store: set live: %s not found for %s/%s", versionID, docType, id)
	}
	if _, exists := dag.Get(versionID); !exists {
		return fmt.Errorf("store: set live: %s not found for %s/%s", versionID, docType, id)
	}

	// Validate the version is a head — i.e. has no children. The
	// liveness bit is only consulted on heads (the prune algorithm
	// considers ancestors of live heads). Allowing it on non-heads
	// would silently no-op against the algorithm and confuse callers.
	if children := dag.Children(versionID); len(children) > 0 {
		return fmt.Errorf("%w: %s has %d child(ren)", ErrNotAHead, versionID, len(children))
	}

	if live {
		delete(s.dead, versionID)
	} else {
		s.dead[versionID] = struct{}{}
	}
	return nil
}

// LoadDAG implements VersionStore. Returns the live in-memory DAG
// the store has been mutating; callers should treat it as
// read-only.
func (s *inMemoryVersionStore) LoadDAG(_ context.Context, docType, id string) (*version.DAG, error) {
	key := docKey(docType, id)
	s.mu.RLock()
	dag, ok := s.dags[key]
	s.mu.RUnlock()
	if ok {
		return dag, nil
	}
	// Return an empty DAG rather than nil so callers don't need a
	// nil-check. Mirrors the lazy-init behavior of AppendVersion.
	return version.NewDAG(), nil
}
