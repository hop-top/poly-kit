package store

import (
	"context"
	"encoding/json"
	"errors"

	"hop.top/kit/go/runtime/domain/version"
)

// Sentinel errors returned by [VersionStore] implementations under
// the dedup invariants from spec `engine-snapshot-dedup` §3.
//
// ErrHashCollision: two distinct payloads hashed to the same key
// (decision #1). util.Short(data, 16) has a birthday bound near
// 2^64, so realistic kit workloads (millions of versions) never see
// it; the sentinel exists so callers can distinguish corruption from
// "hash already present, payload matches" (the normal dedup hit).
//
// ErrRefcountOverflow: refcount + 1 would exceed int64 max
// (decision #4). Effectively unreachable in practice (would require
// 9.2 quintillion references to one payload), but guarded
// explicitly so callers see a clean error rather than silent wrap.
//
// ErrRefcountUnderflow: a decrement would drive refcount below zero
// (decision #5). Indicates a bug — the version_snapshots join got
// out of sync with snapshot_blobs. Logged at error level by the
// implementation and surfaced to the caller; the SQL-level
// CHECK (refcount >= 0) constraint provides the same protection at
// the storage layer.
var (
	ErrHashCollision     = errors.New("store: snapshot hash collision: distinct payloads share a hash")
	ErrRefcountOverflow  = errors.New("store: snapshot refcount overflow")
	ErrRefcountUnderflow = errors.New("store: snapshot refcount underflow (corruption)")

	// ErrNotAHead is returned by [VersionStore.SetLive] (and the
	// public [VersionedDocumentStore.Abandon]) when the target
	// version is not a current DAG head — i.e. has at least one
	// child. The live/dead flag is only meaningful on heads:
	// non-head versions are retained transitively by the prune rule
	// (decision #3) regardless of liveness, so toggling the bit on
	// them would silently no-op.
	ErrNotAHead = errors.New("store: not a head")

	// ErrCannotAbandonLastLiveHead is returned by
	// [VersionedDocumentStore.Abandon] when abandoning the target
	// would leave the document with zero live heads. At least one
	// live head must always exist so the prune retain-floor (the
	// union of live heads' ancestors) is non-empty (decision #2).
	// Operators wanting to drop the last live head should call
	// Delete (the document goes away) or Update / Fork to create a
	// new live head before abandoning the old one.
	ErrCannotAbandonLastLiveHead = errors.New("store: cannot abandon the last live head")
)

// VersionStore persists the version DAG, per-document version lists,
// and snapshot blobs for a [VersionedDocumentStore]. It is the seam
// that lets engine versioning be storage-agnostic by construction:
// the in-memory backend (kept for tests and ephemeral uses) and the
// SQLite backend (durable, default for kit serve) both satisfy this
// interface and nothing more.
//
// VersionStore knows nothing about documents themselves. It deals
// only in (docType, id) keys, opaque JSON payloads, and version IDs.
//
// Implementations MUST be safe for concurrent use by multiple
// goroutines without external locking. The contract semantics below
// are normative: every implementation must observe them, and the
// conformance suite (P3.1) exercises them against each backend.
type VersionStore interface {
	// AppendVersion records a new version for (docType, id).
	//
	// Contract:
	//   - Assigns a strictly monotonic seq starting at 1 per (docType,
	//     id). The Nth call for a given key returns Seq == N.
	//   - parents may be empty for the first version of a document.
	//     Subsequent versions typically pass the previous version's
	//     ID; multiple parents are permitted to support branching
	//     even though the public API on VersionedDocumentStore
	//     appends linearly today.
	//   - If any parent ID is not known to the store (i.e. has not
	//     been appended previously for the same key), AppendVersion
	//     MUST return an error matching the underlying DAG's
	//     "unknown parent" semantics. Callers may rely on this error
	//     being non-nil for unknown parents.
	//   - data is preserved byte-for-byte: a subsequent GetSnapshot
	//     for the returned VersionID returns the exact same bytes.
	//   - The returned Version has its Type, ID, VersionID, Seq, Data
	//     and CreatedAt populated.
	AppendVersion(ctx context.Context, docType, id string, data json.RawMessage, parents []string) (Version, error)

	// ListVersions returns every version recorded for (docType, id),
	// ordered by ascending Seq. The returned slice is empty (not nil
	// error) only when the implementation chooses; the historical
	// behavior of VersionedDocumentStore is to surface "no history"
	// as a higher-level error. Implementations MUST NOT mutate the
	// returned slice's backing array on subsequent calls.
	ListVersions(ctx context.Context, docType, id string) ([]Version, error)

	// GetSnapshot returns the data payload captured at versionID.
	// The bytes are byte-identical to the data passed to the
	// AppendVersion call that produced versionID. Returns a non-nil
	// error if versionID is unknown to the store.
	GetSnapshot(ctx context.Context, versionID string) (json.RawMessage, error)

	// DeleteHistory removes every version, parent edge, and snapshot
	// associated with (docType, id). It MUST cascade so that no
	// orphan rows remain in any backing table. Calling DeleteHistory
	// for a (docType, id) with no recorded versions is a no-op and
	// MUST NOT return an error.
	DeleteHistory(ctx context.Context, docType, id string) error

	// LoadDAG reconstructs the in-memory [version.DAG] for a
	// document. Implementations are expected to materialize lazily
	// (loading rows on first access) and may cache the result for
	// the process lifetime, invalidating on DeleteHistory. The
	// returned DAG includes every Version row and every parent edge
	// recorded for (docType, id).
	LoadDAG(ctx context.Context, docType, id string) (*version.DAG, error)

	// DeleteVersions removes the named versions for (docType, id),
	// cascades parent edges and snapshot join rows, and decrements
	// snapshot_blobs refcounts for every released hash via the
	// existing dedup primitives. Returns the set of blobs whose
	// refcount hit zero (so the row was deleted) as
	// (hash, byteSize) pairs so the caller can populate
	// [PruneResult].
	//
	// Used by [VersionedDocumentStore.Prune]. Implementations MUST
	// use the same write-transaction discipline as AppendVersion
	// (BEGIN IMMEDIATE on SQLite; the existing mutex in-memory).
	// versionIDs not present for (docType, id) are silently ignored
	// — Prune is the only caller and it computes the set itself.
	// An empty versionIDs slice is a no-op and MUST NOT return an
	// error.
	//
	// Returns ErrRefcountUnderflow if the dedup join is corrupt
	// (a removed version's hash is missing from snapshot_blobs or
	// has refcount 0). Surfaces rather than clamping, per the
	// dedup contract.
	DeleteVersions(ctx context.Context, docType, id string, versionIDs []string) ([]FreedBlob, error)

	// SetLive flips the liveness bit on a head version. The bit is
	// the load-bearing input to the prune retain-floor: only live
	// heads contribute their ancestor sets to the retain floor;
	// dead heads are excluded, which is what allows the bottom-up
	// fixed-point to actually fire on abandoned subtrees (the
	// otherwise-impossible prune-fires case under the old spec).
	//
	// Convention: "absent or true means live." Backends MAY store
	// only the dead bits (i.e. only persist live=false rows) — the
	// in-memory backend uses a sparse map for exactly this; the
	// SQLite backend uses a NOT NULL DEFAULT TRUE column for
	// schema-friction reasons.
	//
	// Validation: versionID MUST be a current head (no children in
	// the DAG); otherwise SetLive returns ErrNotAHead. Idempotent —
	// SetLive to the already-current state is a successful no-op.
	//
	// Implementations MUST use the same write-transaction discipline
	// as AppendVersion (BEGIN IMMEDIATE on SQLite; the existing
	// mutex in-memory). The "at least one live head" invariant is
	// enforced by the public [VersionedDocumentStore.Abandon] caller
	// (which counts live heads pre-flight); SetLive itself does NOT
	// enforce it — Merge/Revert internal callers may legitimately
	// drive a head dead as the same atomic step that creates a new
	// live head.
	SetLive(ctx context.Context, docType, id, versionID string, live bool) error
}

// FreedBlob is the (hash, bytes) pair returned by
// [VersionStore.DeleteVersions] for every snapshot blob whose
// refcount hit zero and was deleted. Bytes is the len(data) of the
// blob the moment it was removed; callers sum it into
// [PruneResult.BytesFreed].
type FreedBlob struct {
	Hash  string
	Bytes int64
}
