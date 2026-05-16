package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"hop.top/kit/go/runtime/domain/version"
)

// Version records a point-in-time snapshot of a document's data.
//
// Live carries the load-bearing live/dead head distinction the prune
// algorithm walks: only live heads contribute their ancestor sets to
// the retain floor (decision #3, #4). The default is true — every
// version is born live; only an explicit Abandon (or an internal
// Merge / Revert side-effect) flips it.
//
// JSON wire format: live=true is omitted (the default; backward-compat
// for SDK callers that don't parse the new field), live=false emits
// `"live": false`. Implemented via [Version.MarshalJSON] /
// [Version.UnmarshalJSON] because Go's encoding/json `omitempty` on a
// bool would do the opposite (omit on false, emit on true). An
// absent `live` field on input is read as live=true.
type Version struct {
	Type      string          `json:"-"`
	ID        string          `json:"-"`
	VersionID string          `json:"-"`
	Seq       int             `json:"-"`
	Data      json.RawMessage `json:"-"`
	CreatedAt string          `json:"-"`
	Live      bool            `json:"-"`
}

// versionWire is the on-wire shape Version (un)marshals through. Live
// is a *bool so the encoder can emit nothing when nil and "false" when
// pointing at false; UnmarshalJSON sets Live=true on a missing field.
//
// Field order mirrors the public struct so the JSON output is stable.
type versionWire struct {
	Type      string          `json:"type"`
	ID        string          `json:"id"`
	VersionID string          `json:"version_id"`
	Seq       int             `json:"seq"`
	Data      json.RawMessage `json:"data"`
	CreatedAt string          `json:"created_at"`
	Live      *bool           `json:"live,omitempty"`
}

// MarshalJSON renders Version with the live=true-is-omitted convention
// per the doc comment on [Version].
func (v Version) MarshalJSON() ([]byte, error) {
	w := versionWire{
		Type:      v.Type,
		ID:        v.ID,
		VersionID: v.VersionID,
		Seq:       v.Seq,
		Data:      v.Data,
		CreatedAt: v.CreatedAt,
	}
	if !v.Live {
		// Use a stack-local addressable false so &liveFalse points at
		// a value, not the literal — needed because Go forbids taking
		// the address of an untyped bool literal.
		liveFalse := false
		w.Live = &liveFalse
	}
	return json.Marshal(w)
}

// UnmarshalJSON parses Version from JSON, treating an absent `live`
// field as live=true (the default; spec §6 schema convention).
func (v *Version) UnmarshalJSON(data []byte) error {
	var w versionWire
	if err := json.Unmarshal(data, &w); err != nil {
		return err
	}
	v.Type = w.Type
	v.ID = w.ID
	v.VersionID = w.VersionID
	v.Seq = w.Seq
	v.Data = w.Data
	v.CreatedAt = w.CreatedAt
	if w.Live == nil {
		v.Live = true
	} else {
		v.Live = *w.Live
	}
	return nil
}

// VersionedDocumentStore wraps DocumentStore with version tracking
// on every mutation. The version backend is pluggable via the
// [VersionStore] seam: the in-memory implementation
// ([NewInMemoryVersionStore]) preserves today's ephemeral behavior;
// the SQLite implementation ([NewSQLiteVersionStore]) makes history
// durable across process restarts and commits document + version
// writes in a single transaction (spec §6).
//
// Public API on this type — Create/Update/Get/List/Delete/History/
// Revert — is independent of the chosen backend. Wire-protocol
// callers (engine HTTP/WS) see no difference.
type VersionedDocumentStore struct {
	store    *DocumentStore
	versions VersionStore
}

// NewVersionedDocumentStore wraps an existing DocumentStore using
// the supplied [VersionStore] for version persistence. Callers who
// want the historical in-memory behavior can pass
// [NewInMemoryVersionStore]() or use the
// [NewInMemoryVersionedDocumentStore] convenience constructor.
func NewVersionedDocumentStore(s *DocumentStore, vs VersionStore) *VersionedDocumentStore {
	return &VersionedDocumentStore{store: s, versions: vs}
}

// NewInMemoryVersionedDocumentStore is a convenience constructor
// that wires an in-memory [VersionStore]. Equivalent to
// NewVersionedDocumentStore(s, NewInMemoryVersionStore()).
//
// Suitable for tests and ephemeral uses; for kit serve, prefer the
// SQLite-backed VersionStore so history survives restarts.
func NewInMemoryVersionedDocumentStore(s *DocumentStore) *VersionedDocumentStore {
	return NewVersionedDocumentStore(s, NewInMemoryVersionStore())
}

// sqlExec is the subset of *sql.DB / *sql.Tx / *sql.Conn used by
// the tx-aware code paths. Both *sql.Tx and *sql.Conn satisfy it,
// which lets the shared-tx path drive a BEGIN IMMEDIATE transaction
// via *sql.Conn (database/sql.BeginTx cannot start an IMMEDIATE
// transaction directly with the modernc.org/sqlite driver) while
// still letting the unrelated standalone *sql.Tx callers (none
// today, but the surface stays open) compose with the same helpers.
type sqlExec interface {
	ExecContext(ctx context.Context, query string, args ...any) (sql.Result, error)
	QueryContext(ctx context.Context, query string, args ...any) (*sql.Rows, error)
	QueryRowContext(ctx context.Context, query string, args ...any) *sql.Row
}

// txCapable is the optional interface a VersionStore can satisfy to
// participate in a shared transaction with the document write. The
// SQLite backend implements it; the in-memory backend doesn't need
// to (no durability boundary to coordinate).
//
// The executor is sqlExec rather than *sql.Tx so the shared-tx
// driver can use a *sql.Conn running an explicit BEGIN IMMEDIATE.
// See spec §6 and the SQLite concurrency notes in
// versionstore_sqlite.go.
//
// Kept unexported so the txCapable contract is an internal
// implementation detail of this package — VersionStore consumers
// outside the package see only the documented surface.
type txCapable interface {
	appendVersionTx(ctx context.Context, tx sqlExec, docType, id string, data json.RawMessage, parents []string) (Version, error)
	deleteHistoryTx(ctx context.Context, tx sqlExec, docType, id string) error
}

// beginImmediate checks out a dedicated *sql.Conn from the pool and
// starts a transaction with BEGIN IMMEDIATE. The returned commit
// function closes the transaction (COMMIT on nil error, ROLLBACK
// otherwise) and returns the connection to the pool. Callers MUST
// invoke commit exactly once.
//
// Why not db.BeginTx? Under WAL with concurrent writers, the
// default DEFERRED tx that database/sql.BeginTx produces fails at
// lock-upgrade time with SQLITE_BUSY_SNAPSHOT (517) the moment a
// write follows a read on a snapshot another writer has advanced —
// and busy_timeout cannot help because the conflict is detected
// immediately, not on a wait queue. BEGIN IMMEDIATE acquires the
// reserved lock at transaction start, so the upgrade race is
// avoided entirely.
func beginImmediate(ctx context.Context, db *sql.DB) (*sql.Conn, func(error) error, error) {
	conn, err := db.Conn(ctx)
	if err != nil {
		return nil, nil, fmt.Errorf("store: begin immediate: conn: %w", err)
	}
	if _, err := conn.ExecContext(ctx, "BEGIN IMMEDIATE"); err != nil {
		_ = conn.Close()
		return nil, nil, fmt.Errorf("store: begin immediate: %w", err)
	}
	commit := func(callerErr error) error {
		defer func() { _ = conn.Close() }()
		if callerErr != nil {
			if _, rerr := conn.ExecContext(context.Background(), "ROLLBACK"); rerr != nil {
				return fmt.Errorf("store: rollback: %w (after %v)", rerr, callerErr)
			}
			return nil
		}
		if _, cerr := conn.ExecContext(ctx, "COMMIT"); cerr != nil {
			return fmt.Errorf("store: commit: %w", cerr)
		}
		return nil
	}
	return conn, commit, nil
}

// Create inserts a document and records version 1. When the
// backend supports cross-store transactions, the document insert
// and the initial version row commit atomically.
//
// Create is a thin wrapper around [VersionedDocumentStore.CreateAndVersion]
// that drops the Version return for callers that do not need it.
func (vs *VersionedDocumentStore) Create(ctx context.Context, docType string, data json.RawMessage) (Document, error) {
	doc, _, err := vs.CreateAndVersion(ctx, docType, data)
	return doc, err
}

// CreateAndVersion is the same as [VersionedDocumentStore.Create] but
// also returns the [Version] row that was appended for the new
// document. Callers that need to address the new version directly
// (event payloads carrying version_id+seq, audit/replay) can use this
// helper to avoid a follow-up lookup that races concurrent writers.
func (vs *VersionedDocumentStore) CreateAndVersion(ctx context.Context, docType string, data json.RawMessage) (Document, Version, error) {
	if txvs, ok := vs.versions.(txCapable); ok {
		return vs.createTx(ctx, txvs, docType, data)
	}
	doc, err := vs.store.Create(ctx, docType, data)
	if err != nil {
		return Document{}, Version{}, err
	}
	v, err := vs.versions.AppendVersion(ctx, doc.Type, doc.ID, data, nil)
	if err != nil {
		return Document{}, Version{}, fmt.Errorf("store: record initial version: %w", err)
	}
	return doc, v, nil
}

func (vs *VersionedDocumentStore) createTx(ctx context.Context, txvs txCapable, docType string, data json.RawMessage) (Document, Version, error) {
	conn, commit, err := beginImmediate(ctx, vs.store.DB())
	if err != nil {
		return Document{}, Version{}, err
	}
	doc, err := vs.store.createConn(ctx, conn, docType, data)
	if err != nil {
		_ = commit(err)
		return Document{}, Version{}, err
	}
	v, err := txvs.appendVersionTx(ctx, conn, doc.Type, doc.ID, data, nil)
	if err != nil {
		_ = commit(err)
		return Document{}, Version{}, fmt.Errorf("store: record initial version: %w", err)
	}
	if cerr := commit(nil); cerr != nil {
		return Document{}, Version{}, fmt.Errorf("store: create: commit: %w", cerr)
	}
	return doc, v, nil
}

// Update replaces document data and appends a new version.
//
// Update is a thin wrapper around [VersionedDocumentStore.UpdateAndVersion]
// that drops the Version return for callers that do not need it.
func (vs *VersionedDocumentStore) Update(ctx context.Context, docType, id string, data json.RawMessage) (Document, error) {
	doc, _, err := vs.UpdateAndVersion(ctx, docType, id, data)
	return doc, err
}

// UpdateAndVersion is the same as [VersionedDocumentStore.Update] but
// also returns the [Version] row that was appended for the update.
// Callers that need to address the new version directly (event
// payloads carrying version_id+seq, audit/replay) can use this helper
// to avoid a follow-up lookup that races concurrent writers.
func (vs *VersionedDocumentStore) UpdateAndVersion(ctx context.Context, docType, id string, data json.RawMessage) (Document, Version, error) {
	if txvs, ok := vs.versions.(txCapable); ok {
		return vs.updateTx(ctx, txvs, docType, id, data)
	}
	doc, err := vs.store.Update(ctx, docType, id, data)
	if err != nil {
		return Document{}, Version{}, err
	}
	parents, err := vs.parentsFor(ctx, docType, id)
	if err != nil {
		return Document{}, Version{}, err
	}
	v, err := vs.versions.AppendVersion(ctx, docType, id, data, parents)
	if err != nil {
		return Document{}, Version{}, fmt.Errorf("store: record version: %w", err)
	}
	return doc, v, nil
}

func (vs *VersionedDocumentStore) updateTx(ctx context.Context, txvs txCapable, docType, id string, data json.RawMessage) (Document, Version, error) {
	conn, commit, err := beginImmediate(ctx, vs.store.DB())
	if err != nil {
		return Document{}, Version{}, err
	}
	doc, err := vs.store.updateConn(ctx, conn, docType, id, data)
	if err != nil {
		_ = commit(err)
		return Document{}, Version{}, err
	}
	parents, err := vs.parentsForTx(ctx, conn, docType, id)
	if err != nil {
		_ = commit(err)
		return Document{}, Version{}, err
	}
	v, err := txvs.appendVersionTx(ctx, conn, docType, id, data, parents)
	if err != nil {
		_ = commit(err)
		return Document{}, Version{}, fmt.Errorf("store: record version: %w", err)
	}
	if cerr := commit(nil); cerr != nil {
		return Document{}, Version{}, fmt.Errorf("store: update: commit: %w", cerr)
	}
	return doc, v, nil
}

// Get delegates to the inner store.
func (vs *VersionedDocumentStore) Get(ctx context.Context, docType, id string) (Document, error) {
	return vs.store.Get(ctx, docType, id)
}

// List delegates to the inner store.
func (vs *VersionedDocumentStore) List(ctx context.Context, docType string, q Query) ([]Document, error) {
	return vs.store.List(ctx, docType, q)
}

// Delete removes the document and its version history.
func (vs *VersionedDocumentStore) Delete(ctx context.Context, docType, id string) error {
	if txvs, ok := vs.versions.(txCapable); ok {
		return vs.deleteTx(ctx, txvs, docType, id)
	}
	if err := vs.store.Delete(ctx, docType, id); err != nil {
		return err
	}
	return vs.versions.DeleteHistory(ctx, docType, id)
}

func (vs *VersionedDocumentStore) deleteTx(ctx context.Context, txvs txCapable, docType, id string) error {
	conn, commit, err := beginImmediate(ctx, vs.store.DB())
	if err != nil {
		return err
	}
	if err := vs.store.deleteConn(ctx, conn, docType, id); err != nil {
		_ = commit(err)
		return err
	}
	if err := txvs.deleteHistoryTx(ctx, conn, docType, id); err != nil {
		_ = commit(err)
		return err
	}
	if cerr := commit(nil); cerr != nil {
		return fmt.Errorf("store: delete: commit: %w", cerr)
	}
	return nil
}

// History returns all versions for a document ordered by seq.
func (vs *VersionedDocumentStore) History(ctx context.Context, docType, id string) ([]Version, error) {
	versions, err := vs.versions.ListVersions(ctx, docType, id)
	if err != nil {
		return nil, err
	}
	if len(versions) == 0 {
		return nil, fmt.Errorf("store: no history for %s/%s", docType, id)
	}
	return versions, nil
}

// Fork creates a divergent branch starting at fromSeq. It appends a
// new version whose only parent is fromSeq's version_id and whose
// data is fromSeq's snapshot byte-for-byte. The new version becomes
// the latest seq for (docType, id), so a subsequent Update naturally
// extends the branch tip Fork just produced; the original linear
// chain is left intact and its old head remains a head of the DAG.
//
// Anonymous-branches model (spec §3 decision 1): branches are not
// named — their identity is the head version_id this call returns.
// Two Forks at the same fromSeq produce two divergent branch tips
// (the spec's idempotency language is interpreted as "same shape,
// not same row" — repeated Fork calls without intervening writes
// each materialize a sibling because Phase 1 ships no UpdateAt
// surface to extend a specific tip; sibling materialization is the
// only way the public API alone can express branched topology).
//
// 404-equivalent: returns an error if (docType, id) has no history.
// Out-of-range fromSeq returns an error mirroring Revert's shape.
func (vs *VersionedDocumentStore) Fork(ctx context.Context, docType, id string, fromSeq int) (Version, error) {
	versions, err := vs.versions.ListVersions(ctx, docType, id)
	if err != nil {
		return Version{}, err
	}
	if len(versions) == 0 {
		return Version{}, fmt.Errorf("store: no history for %s/%s", docType, id)
	}

	var source *Version
	for i := range versions {
		if versions[i].Seq == fromSeq {
			source = &versions[i]
			break
		}
	}
	if source == nil {
		return Version{}, fmt.Errorf("store: version %d not found for %s/%s", fromSeq, docType, id)
	}

	// Fetch the source snapshot via VersionStore so the fork tip
	// carries the same bytes the conformance contract guarantees for
	// GetSnapshot. ListVersions on the SQLite backend already hydrates
	// Data, but we go through GetSnapshot to keep this path
	// backend-agnostic if a future backend lazy-hydrates.
	data, err := vs.versions.GetSnapshot(ctx, source.VersionID)
	if err != nil {
		return Version{}, fmt.Errorf("store: fork: snapshot: %w", err)
	}

	v, err := vs.versions.AppendVersion(ctx, docType, id, data, []string{source.VersionID})
	if err != nil {
		return Version{}, fmt.Errorf("store: fork: %w", err)
	}
	return v, nil
}

// Merge appends a version with both source and target as parents.
// data is the merged payload chosen by the caller; conflict
// detection is the caller's job in MVP (spec §3 decision 5). The
// returned Version has parent edges [sourceVersionID,
// targetVersionID] in that order.
//
// Live/dead side-effect (decision #10): both source and target are
// marked dead BEFORE the merge tip is appended. At call time they
// are still graph-topology heads (no children); after the append
// they are non-heads (the merge tip is their child) but the dead
// bit persists. The merge tip is born live, so the live-head count
// stays >= 1 (source/target were heads at merge time; net is N-1).
//
// 404-equivalent: returns an error if (docType, id) has no history.
// Out-of-range sourceSeq or targetSeq returns an error.
func (vs *VersionedDocumentStore) Merge(ctx context.Context, docType, id string, sourceSeq, targetSeq int, data json.RawMessage) (Version, error) {
	versions, err := vs.versions.ListVersions(ctx, docType, id)
	if err != nil {
		return Version{}, err
	}
	if len(versions) == 0 {
		return Version{}, fmt.Errorf("store: no history for %s/%s", docType, id)
	}

	var source, target *Version
	for i := range versions {
		switch versions[i].Seq {
		case sourceSeq:
			source = &versions[i]
		case targetSeq:
			target = &versions[i]
		}
	}
	if source == nil {
		return Version{}, fmt.Errorf("store: version %d not found for %s/%s", sourceSeq, docType, id)
	}
	if target == nil {
		return Version{}, fmt.Errorf("store: version %d not found for %s/%s", targetSeq, docType, id)
	}

	// Mark source and target dead BEFORE appending the merge tip.
	// In the typical case both are graph-topology heads at this
	// call; the dead bit lands on them while they're still heads,
	// then the AppendVersion below makes them non-heads (the merge
	// tip becomes their child). The bit persists for any future
	// liveness query.
	//
	// Lenient on non-head parents: existing pre-prune-track Merge
	// allows source/target to already have children (e.g. a second
	// merge of the same parents producing a redundant tip). In that
	// case SetLive returns ErrNotAHead — we swallow it because the
	// liveness bit is only meaningful on heads, and a non-head's
	// retention is governed by the prune algorithm's transitive
	// retain rule (its descendants' liveness, not its own).
	//
	// Skip the target call when source==target (degenerate self-
	// merge): one SetLive is enough.
	if err := vs.versions.SetLive(ctx, docType, id, source.VersionID, false); err != nil && !errors.Is(err, ErrNotAHead) {
		return Version{}, fmt.Errorf("store: merge: mark source dead: %w", err)
	}
	if target.VersionID != source.VersionID {
		if err := vs.versions.SetLive(ctx, docType, id, target.VersionID, false); err != nil && !errors.Is(err, ErrNotAHead) {
			return Version{}, fmt.Errorf("store: merge: mark target dead: %w", err)
		}
	}

	v, err := vs.versions.AppendVersion(ctx, docType, id, data, []string{source.VersionID, target.VersionID})
	if err != nil {
		return Version{}, fmt.Errorf("store: merge: %w", err)
	}
	return v, nil
}

// BranchesOption configures [VersionedDocumentStore.Branches].
type BranchesOption func(*branchesOpts)

// branchesOpts is the internal accumulator for Branches options.
type branchesOpts struct {
	liveOnly bool
}

// WithLiveOnly tells [VersionedDocumentStore.Branches] to filter the
// returned heads to those with Live=true. Default behavior (no opts)
// returns all heads — live and dead — preserving the pre-prune-track
// public API byte-for-byte.
func WithLiveOnly() BranchesOption {
	return func(o *branchesOpts) { o.liveOnly = true }
}

// Branches returns the heads (tips) of the version DAG for
// (docType, id). A linear history returns exactly one head; a
// branched history returns two or more. Heads are returned ordered
// by ascending seq for deterministic iteration; callers that want
// most-recently-written-first can reverse the slice.
//
// Each returned Version is hydrated with type/id/version_id/seq/
// created_at/data — the same shape History returns. Returns an
// error if (docType, id) has no history (mirrors History's contract).
//
// Pass [WithLiveOnly] to filter to live heads only — useful for
// surfaces (UI, sync) that want the operator's canonical-head
// concept, not the full topology. The default behavior (no opts) is
// unchanged from the pre-prune-track public API and returns all
// heads regardless of liveness.
func (vs *VersionedDocumentStore) Branches(ctx context.Context, docType, id string, opts ...BranchesOption) ([]Version, error) {
	o := branchesOpts{}
	for _, opt := range opts {
		opt(&o)
	}

	versions, err := vs.versions.ListVersions(ctx, docType, id)
	if err != nil {
		return nil, err
	}
	if len(versions) == 0 {
		return nil, fmt.Errorf("store: no history for %s/%s", docType, id)
	}

	dag, err := vs.versions.LoadDAG(ctx, docType, id)
	if err != nil {
		return nil, fmt.Errorf("store: branches: load dag: %w", err)
	}
	headIDs := dag.Heads()
	if len(headIDs) == 0 {
		return nil, nil
	}

	headSet := make(map[string]struct{}, len(headIDs))
	for _, h := range headIDs {
		headSet[h] = struct{}{}
	}

	out := make([]Version, 0, len(headIDs))
	for i := range versions {
		if _, ok := headSet[versions[i].VersionID]; !ok {
			continue
		}
		if o.liveOnly && !versions[i].Live {
			continue
		}
		out = append(out, versions[i])
	}
	return out, nil
}

// Abandon marks the head version at (docType, id, seq) as dead.
// Idempotent: abandoning an already-dead head is a successful no-op.
//
// Errors:
//   - ErrNotAHead — seq exists but the version has children (not a
//     graph-topology head).
//   - ErrCannotAbandonLastLiveHead — abandoning seq would leave the
//     document with zero live heads. Operators wanting to drop the
//     last live head should call Delete (the document goes away) or
//     Update / Fork to create a new live head before abandoning.
//   - "no history" — (docType, id) has no recorded versions.
//   - "version N not found" — seq does not exist for this document.
//
// Abandon is the opt-in lever the prune algorithm needs to actually
// fire on operator-driven scenarios: marking a fork tail dead lets
// the retain floor exclude it, after which versions in that subtree
// become candidates the bottom-up fixed-point can prune.
func (vs *VersionedDocumentStore) Abandon(ctx context.Context, docType, id string, seq int) error {
	versions, err := vs.versions.ListVersions(ctx, docType, id)
	if err != nil {
		return err
	}
	if len(versions) == 0 {
		return fmt.Errorf("store: no history for %s/%s", docType, id)
	}

	var target *Version
	for i := range versions {
		if versions[i].Seq == seq {
			target = &versions[i]
			break
		}
	}
	if target == nil {
		return fmt.Errorf("store: version %d not found for %s/%s", seq, docType, id)
	}

	// Already dead → no-op success. Idempotent contract.
	if !target.Live {
		return nil
	}

	// Validate it's a head. SetLive double-checks but checking up
	// front lets us return ErrNotAHead with the same shape regardless
	// of which backend is wired.
	dag, err := vs.versions.LoadDAG(ctx, docType, id)
	if err != nil {
		return fmt.Errorf("store: abandon: load dag: %w", err)
	}
	if children := dag.Children(target.VersionID); len(children) > 0 {
		return fmt.Errorf("%w: version %d (%s) has %d child(ren)", ErrNotAHead, seq, target.VersionID, len(children))
	}

	// Count current live heads. If exactly one and it's the target,
	// refuse — this is the at-least-one-live-head invariant
	// (decision #2). Operators wanting to drop the last live head
	// should call Delete (document goes away).
	headIDs := dag.Heads()
	liveHeads := 0
	for _, h := range headIDs {
		// Find the corresponding Version row to read its Live flag.
		for i := range versions {
			if versions[i].VersionID == h && versions[i].Live {
				liveHeads++
				break
			}
		}
	}
	if liveHeads <= 1 {
		return ErrCannotAbandonLastLiveHead
	}

	if err := vs.versions.SetLive(ctx, docType, id, target.VersionID, false); err != nil {
		return fmt.Errorf("store: abandon: %w", err)
	}
	return nil
}

// Revert restores a document to the given version sequence number.
// The revert itself creates a new version entry. Note: Revert
// reuses Update internally, so it benefits automatically from the
// shared-tx path when the backend supports it.
//
// Live/dead side-effect (decision #10): the pre-revert head (the
// latest-seq version at call time, on the live branch the revert
// targets) is marked dead BEFORE Update appends the revert tip.
// After Update, the pre-revert head has the revert tip as a child
// (it's no longer a graph-head); the dead bit persists. The revert
// tip is born live, so the live-head count is preserved.
//
// On a branched document, the pre-revert head is the head whose
// ancestor chain includes the target seq — the same head Update's
// parentsFor lookup picks (the most-recent-seq head). This matches
// the existing pre-prune Revert semantic byte-for-byte.
func (vs *VersionedDocumentStore) Revert(ctx context.Context, docType, id string, seq int) (Document, error) {
	versions, err := vs.versions.ListVersions(ctx, docType, id)
	if err != nil {
		return Document{}, err
	}

	var target *Version
	for i := range versions {
		if versions[i].Seq == seq {
			target = &versions[i]
			break
		}
	}
	if target == nil {
		return Document{}, fmt.Errorf("store: version %d not found for %s/%s", seq, docType, id)
	}

	// Identify the pre-revert head: the most-recent-seq version that
	// is currently a graph-topology head. parentsFor uses ORDER BY
	// seq DESC LIMIT 1 — Update extends from that. We mirror the
	// same lookup so the dead bit lands on the version whose linear
	// extension the Update path will create.
	preHead, err := vs.parentsFor(ctx, docType, id)
	if err != nil {
		return Document{}, err
	}
	if len(preHead) == 1 {
		// Defensive: SetLive returns ErrNotAHead if parentsFor's pick
		// somehow has children (shouldn't happen because the
		// most-recent seq is a topology head by construction in the
		// linear case). Skip the dead-mark on degenerate input rather
		// than fail Revert outright.
		if err := vs.versions.SetLive(ctx, docType, id, preHead[0], false); err != nil {
			if !errors.Is(err, ErrNotAHead) {
				return Document{}, fmt.Errorf("store: revert: mark pre-revert head dead: %w", err)
			}
		}
	}

	return vs.Update(ctx, docType, id, target.Data)
}

// parentsFor returns the single-parent linear-history slice expected
// by Update: the most-recent version's ID, or nil if the document
// has no history yet. Used on the non-tx path.
func (vs *VersionedDocumentStore) parentsFor(ctx context.Context, docType, id string) ([]string, error) {
	existing, err := vs.versions.ListVersions(ctx, docType, id)
	if err != nil {
		return nil, err
	}
	if len(existing) == 0 {
		return nil, nil
	}
	return []string{existing[len(existing)-1].VersionID}, nil
}

// parentsForTx is the tx-aware variant: reads the current head from
// the same transaction the version write will commit on, so the
// parent ID is consistent with the row that's about to be inserted.
func (vs *VersionedDocumentStore) parentsForTx(ctx context.Context, tx sqlExec, docType, id string) ([]string, error) {
	row := tx.QueryRowContext(ctx,
		`SELECT version_id FROM versions WHERE type = ? AND id = ? ORDER BY seq DESC LIMIT 1`,
		docType, id,
	)
	var vid string
	if err := row.Scan(&vid); err != nil {
		if err == sql.ErrNoRows {
			return nil, nil
		}
		return nil, fmt.Errorf("store: parent lookup: %w", err)
	}
	return []string{vid}, nil
}

func docKey(docType, id string) string {
	return docType + ":" + id
}

// RetentionPolicy bounds how many versions per (type, id) and how
// long a version may live before becoming a prune candidate.
//
// Both fields are optional; zero-value means "no limit on this
// dimension." If both are set, a version is a prune candidate only
// when it exceeds BOTH limits (AND-rule, spec §3 decision #1).
//
// A prune candidate is then evaluated against the DAG-aware
// "is this safe to remove" rule (spec §3 decision #3): a candidate
// is actually pruned only when it is not a head and none of its
// descendants are retained. See [VersionedDocumentStore.Prune].
type RetentionPolicy struct {
	// MaxVersions caps the number of most-recent versions to retain
	// per (type, id). 0 = unlimited; otherwise versions older than
	// the most recent N (by seq) are prune candidates.
	MaxVersions int

	// MaxAge bounds how old a version may be before it becomes a
	// prune candidate. 0 = unlimited; otherwise versions whose
	// CreatedAt is older than now-MaxAge are candidates.
	MaxAge time.Duration
}

// PruneResult reports what [VersionedDocumentStore.Prune] removed.
//
// VersionsRemoved is in seq order (oldest first). BlobsFreed counts
// blobs whose refcount hit zero and were deleted; blobs that
// survived (still referenced by other versions) do not contribute.
// BytesFreed is the sum of len(data) over freed blobs.
type PruneResult struct {
	VersionsRemoved []string `json:"versions_removed"`
	BlobsFreed      int      `json:"blobs_freed"`
	BytesFreed      int64    `json:"bytes_freed"`
}

// Prune walks the version DAG for (docType, id), removes prunable
// versions per policy (spec §3 decision #3), decrements refcounts
// on their snapshot blobs through the existing dedup primitives,
// deletes blobs that hit refcount 0, and returns what was removed.
//
// Heads are always retained (spec §3 #2). Pruning never rewrites
// retained versions' parent_ids; a candidate with a retained
// descendant is retained transitively (spec §3 #3, #4).
//
// Prune is a single write transaction. SQLite uses BEGIN IMMEDIATE;
// in-memory uses its existing mutex. Under concurrent AppendVersion,
// the storage-layer serialization decides the order; Prune always
// sees a consistent point-in-time snapshot.
//
// Returns ErrRefcountUnderflow if the dedup join is corrupt (a
// pruned version's hash is missing from snapshot_blobs or has
// refcount 0). Surfaces rather than clamping, per the dedup contract.
//
// (docType, id) with no recorded history returns a non-nil error
// matching [VersionedDocumentStore.History]'s contract.
func (vs *VersionedDocumentStore) Prune(ctx context.Context, docType, id string, policy RetentionPolicy) (PruneResult, error) {
	versions, err := vs.versions.ListVersions(ctx, docType, id)
	if err != nil {
		return PruneResult{}, err
	}
	if len(versions) == 0 {
		return PruneResult{}, fmt.Errorf("store: no history for %s/%s", docType, id)
	}

	dag, err := vs.versions.LoadDAG(ctx, docType, id)
	if err != nil {
		return PruneResult{}, fmt.Errorf("store: prune: load dag: %w", err)
	}

	prunable := computePrunable(versions, dag, policy, time.Now())
	if len(prunable) == 0 {
		return PruneResult{}, nil
	}

	// Order prunable IDs by seq (oldest first) — DeleteVersions
	// accepts any order, but PruneResult.VersionsRemoved contract is
	// "seq order (oldest first)" per spec §4.
	ids := make([]string, 0, len(prunable))
	for _, v := range versions {
		if _, ok := prunable[v.VersionID]; ok {
			ids = append(ids, v.VersionID)
		}
	}

	freed, err := vs.versions.DeleteVersions(ctx, docType, id, ids)
	if err != nil {
		return PruneResult{}, err
	}

	out := PruneResult{
		VersionsRemoved: ids,
		BlobsFreed:      len(freed),
	}
	for _, fb := range freed {
		out.BytesFreed += fb.Bytes
	}
	return out, nil
}

// computePrunable applies the spec §3 decision #3 + #10 rule to the
// supplied (versions, dag, policy, now) snapshot and returns the set
// of version_ids that are safe to remove.
//
// Algorithm:
//
//  1. live_heads = {graph-head h | versions[h].Live == true}.
//  2. retain_floor = union(ancestors(h) ∪ {h}) for h in live_heads —
//     the protected set. Pruning never touches it. (decision #4)
//  3. Initial candidate set: versions exceeding policy bounds AND
//     not in retain_floor. Dead heads (graph-head h with Live=false)
//     can now be candidates — that's the load-bearing change vs. the
//     old rule, which excluded ALL graph-heads. (decision #10)
//  4. Bottom-up fixed-point: a candidate is prunable iff every child
//     is also a candidate (a dead head with no children is vacuously
//     prunable). Repeat until no candidate is removed. The retain
//     floor never enters the candidate set, so any candidate with a
//     retain-floor child is removed in the first pass.
//
// Returns the set of prunable version_ids. The empty map is the
// no-op signal (linear-history-with-single-live-head, etc).
//
// Pre-condition: at least one live head exists (decision #2; enforced
// by Abandon's at-least-one-live-head check). If somehow no live
// heads exist (defense-in-depth — caller should not reach here),
// returns nil to refuse the prune; emptying the document is Delete's
// concern, not Prune's.
func computePrunable(versions []Version, dag *version.DAG, policy RetentionPolicy, now time.Time) map[string]struct{} {
	if len(versions) == 0 {
		return nil
	}

	// Build a versionID → Version-pointer index so the live-head
	// lookup and per-version Live reads are O(1).
	byID := make(map[string]*Version, len(versions))
	for i := range versions {
		byID[versions[i].VersionID] = &versions[i]
	}

	// 1. live_heads — graph-heads that are Live=true.
	headIDs := dag.Heads()
	liveHeads := make([]string, 0, len(headIDs))
	for _, h := range headIDs {
		v, ok := byID[h]
		if !ok {
			// Stale DAG entry — defensive. Skip.
			continue
		}
		if v.Live {
			liveHeads = append(liveHeads, h)
		}
	}
	if len(liveHeads) == 0 {
		// No live heads — refuse to prune. Defensive against the
		// at-least-one-live-head invariant being violated.
		return nil
	}

	// 2. retain_floor — union of ancestors of every live head plus
	//    the live head itself. dag.Ancestors returns the EXCLUSIVE
	//    ancestor set; we add the head itself.
	retainFloor := make(map[string]struct{})
	for _, h := range liveHeads {
		retainFloor[h] = struct{}{}
		for _, a := range dag.Ancestors(h) {
			retainFloor[a] = struct{}{}
		}
	}

	// 3. Initial candidate set: versions exceeding policy bounds AND
	//    not in retain_floor.
	totalVersions := len(versions)
	candidates := make(map[string]struct{})
	for i, v := range versions {
		if _, retained := retainFloor[v.VersionID]; retained {
			continue
		}
		if !policyExceeds(v, policy, now, i, totalVersions) {
			continue
		}
		candidates[v.VersionID] = struct{}{}
	}
	if len(candidates) == 0 {
		return nil
	}

	// 4. Bottom-up fixed-point: a candidate is prunable iff every
	//    child is also a candidate. Dead heads with no children pass
	//    vacuously. Iterate until no candidate is dropped.
	for {
		changed := false
		for vid := range candidates {
			children := dag.Children(vid)
			retainedDescendant := false
			for _, c := range children {
				if _, ok := candidates[c]; !ok {
					retainedDescendant = true
					break
				}
			}
			if retainedDescendant {
				delete(candidates, vid)
				changed = true
			}
		}
		if !changed {
			break
		}
	}
	return candidates
}

// policyExceeds reports whether v exceeds the policy bounds.
//
// Index/totalVersions are v's position in the seq-ordered slice and
// the total count, used to evaluate MaxVersions: the most-recent N
// versions are retained, so v is over the count bound iff
// (totalVersions - index) > MaxVersions.
//
// AND-rule (spec §3 #1): when both MaxVersions and MaxAge are set,
// v must exceed BOTH to be a candidate. When only one is set, that
// dimension alone decides. When neither is set, v is never a
// candidate (Prune is a no-op).
func policyExceeds(v Version, policy RetentionPolicy, now time.Time, index, totalVersions int) bool {
	hasCount := policy.MaxVersions > 0
	hasAge := policy.MaxAge > 0
	if !hasCount && !hasAge {
		return false
	}

	exceedsCount := false
	if hasCount {
		// Most-recent N (by seq) are retained: index runs 0..total-1
		// in seq order, so v is over the bound iff its position from
		// the tail exceeds MaxVersions.
		exceedsCount = (totalVersions - index) > policy.MaxVersions
	}

	exceedsAge := false
	if hasAge {
		t, err := time.Parse(time.RFC3339Nano, v.CreatedAt)
		if err == nil {
			exceedsAge = now.Sub(t) > policy.MaxAge
		}
	}

	switch {
	case hasCount && hasAge:
		return exceedsCount && exceedsAge
	case hasCount:
		return exceedsCount
	default:
		return exceedsAge
	}
}
