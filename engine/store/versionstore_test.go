package store

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"path/filepath"
	"sort"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// versionstore_test.go is the conformance suite for the
// [VersionStore] interface. Every scenario runs once per backend
// registered in [conformanceBackends]; new backends plug in with
// one entry. The suite drives implementations through the public
// VersionStore surface only — no tx coupling, no implementation
// internals — so any future backend (Badger, Postgres, ...) gets
// the same coverage by free.

// backendFactory pairs a backend name with a constructor that
// returns a fresh, isolated [VersionStore] for the test that asks
// for one. Each scenario gets its own factory call so state never
// leaks between scenarios.
type backendFactory struct {
	name string
	make func(t *testing.T) VersionStore
}

// conformanceBackends returns every VersionStore impl that the
// conformance suite must cover. Add a new backend here and every
// scenario picks it up automatically.
func conformanceBackends() []backendFactory {
	return []backendFactory{
		{
			name: "in-memory",
			make: func(t *testing.T) VersionStore {
				t.Helper()
				return NewInMemoryVersionStore()
			},
		},
		{
			name: "sqlite",
			make: func(t *testing.T) VersionStore {
				t.Helper()
				return openConformanceSQLiteVersionStore(t)
			},
		},
	}
}

// openConformanceSQLiteVersionStore opens a temp on-disk SQLite
// DocumentStore (so the version-tables migration runs) and returns
// a SQLite VersionStore over the shared *sql.DB, mirroring kit
// serve's wiring. Self-contained: no shared state with
// versionstore_sqlite_test.go's helpers.
func openConformanceSQLiteVersionStore(t *testing.T) VersionStore {
	t.Helper()
	dbPath := filepath.Join(t.TempDir(), "conformance.db")
	ds, err := NewDocumentStore(dbPath)
	require.NoError(t, err)
	t.Cleanup(func() { _ = ds.Close() })

	vs, err := NewSQLiteVersionStore(ds.DB())
	require.NoError(t, err)
	return vs
}

// TestVersionStoreConformance is the single entry point for the
// suite. It expands into one subtest per (scenario, backend) pair.
// Use `go test -run TestVersionStoreConformance/sqlite/AppendLinear`
// to target a specific cell.
//
// scenario.skipBackends names backends that opt out of the
// scenario, with the reason recorded inline. This is reserved for
// asserting contracts the spec mandates but a backend has not yet
// upheld; any entry here is a known bug to be tracked in its own
// follow-up issue, not a license to weaken the spec.
func TestVersionStoreConformance(t *testing.T) {
	type scenario struct {
		name         string
		run          func(t *testing.T, vs VersionStore)
		skipBackends map[string]string // backend name -> skip reason
	}
	scenarios := []scenario{
		{name: "AppendLinear", run: runAppendLinear},
		{name: "ListOrdering", run: runListOrdering},
		{name: "SnapshotRoundTrip", run: runSnapshotRoundTrip},
		{name: "DeleteHistoryCascades", run: runDeleteHistoryCascades},
		{name: "LoadDAGReconstructsParents", run: runLoadDAGReconstructsParents},
		{name: "EmptyHistory", run: runEmptyHistory},
		{name: "UnknownParentRejected", run: runUnknownParentRejected},
		{name: "CrossDocumentParentRejected", run: runCrossDocumentParentRejected},
		{name: "ConcurrencySmoke", run: runConcurrencySmoke},
		{name: "LoadDAGCacheCoherence", run: runLoadDAGCacheCoherence},
		{name: "DedupReusesIdenticalSnapshots", run: runDedupReusesIdenticalSnapshots},
		{name: "DedupCrossDocumentSharing", run: runDedupCrossDocumentSharing},
		{name: "RefcountedDeleteCascadesCleanly", run: runRefcountedDeleteCascadesCleanly},
		{name: "SeqMonotonicAcrossPrune", run: runSeqMonotonicAcrossPrune},
		{name: "SeqRestartsAfterDeleteHistory", run: runSeqRestartsAfterDeleteHistory},
		// NOTE: ErrHashCollision is intentionally NOT exercised here.
		// util.Short(data, 16) has a birthday bound near 2^64, so
		// engineering a real collision in a fixture is impractical;
		// the only way to exercise the sentinel is fault injection
		// (rewrite a snapshot_blobs row's bytes while keeping its
		// hash). That requires backend-specific knowledge — directly
		// at odds with the conformance suite's "drive both impls
		// through the public surface only" rule. The dedup-migration
		// tests (dedup_migration_test.go::TestDedup_HashCollisionSentinel)
		// already cover the sentinel via the SQLite backend with the
		// internal upsertSnapshotBlob helper. The in-memory backend's
		// matching ErrHashCollision branch is exercised through the
		// property test added in T-0405.
	}

	for _, b := range conformanceBackends() {
		b := b
		t.Run(b.name, func(t *testing.T) {
			for _, sc := range scenarios {
				sc := sc
				t.Run(sc.name, func(t *testing.T) {
					if reason, skip := sc.skipBackends[b.name]; skip {
						t.Skipf("skipped on %s: %s", b.name, reason)
					}
					sc.run(t, b.make(t))
				})
			}
		})
	}
}

// runAppendLinear: parents chain correctly across three sequential
// appends. Each version's seq is the 1-based index of the call.
func runAppendLinear(t *testing.T, vs VersionStore) {
	ctx := context.Background()

	v1, err := vs.AppendVersion(ctx, "doc", "d1", json.RawMessage(`{"v":1}`), nil)
	require.NoError(t, err)
	assert.Equal(t, 1, v1.Seq)
	assert.NotEmpty(t, v1.VersionID)

	v2, err := vs.AppendVersion(ctx, "doc", "d1", json.RawMessage(`{"v":2}`), []string{v1.VersionID})
	require.NoError(t, err)
	assert.Equal(t, 2, v2.Seq)

	v3, err := vs.AppendVersion(ctx, "doc", "d1", json.RawMessage(`{"v":3}`), []string{v2.VersionID})
	require.NoError(t, err)
	assert.Equal(t, 3, v3.Seq)

	// Reload through LoadDAG and verify the ParentIDs chain end-to-end.
	dag, err := vs.LoadDAG(ctx, "doc", "d1")
	require.NoError(t, err)

	got2, ok := dag.Get(v2.VersionID)
	require.True(t, ok, "v2 must be in DAG")
	assert.Equal(t, []string{v1.VersionID}, got2.ParentIDs, "v2's parent must be v1")

	got3, ok := dag.Get(v3.VersionID)
	require.True(t, ok, "v3 must be in DAG")
	assert.Equal(t, []string{v2.VersionID}, got3.ParentIDs, "v3's parent must be v2")
}

// runListOrdering: ListVersions returns versions ordered by
// ascending seq with no gaps for N sequential appends.
func runListOrdering(t *testing.T, vs VersionStore) {
	ctx := context.Background()

	const n = 7
	var prev string
	for i := 1; i <= n; i++ {
		var parents []string
		if prev != "" {
			parents = []string{prev}
		}
		v, err := vs.AppendVersion(ctx, "doc", "list", json.RawMessage(fmt.Sprintf(`{"step":%d}`, i)), parents)
		require.NoError(t, err)
		require.Equal(t, i, v.Seq)
		prev = v.VersionID
	}

	got, err := vs.ListVersions(ctx, "doc", "list")
	require.NoError(t, err)
	require.Len(t, got, n)
	for i, v := range got {
		assert.Equal(t, i+1, v.Seq, "ListVersions[%d] should have seq %d", i, i+1)
	}
}

// runSnapshotRoundTrip: GetSnapshot returns byte-identical data,
// preserving whitespace, unicode and escape sequences.
func runSnapshotRoundTrip(t *testing.T, vs VersionStore) {
	ctx := context.Background()

	// Hand-crafted payload with whitespace, unicode (snowman, emoji
	// flag composed of two regional indicators) and escape
	// sequences. Bytes must come back exactly.
	payload := json.RawMessage("{\n  \"title\": \"hello \\\"world\\\"\",\n  \"snow\": \"☃\",\n  \"flag\": \"\U0001F1E8\U0001F1E6\",\n  \"tab\": \"a\\tb\",\n  \"nl\": \"line1\\nline2\"\n}")

	v, err := vs.AppendVersion(ctx, "doc", "snap", payload, nil)
	require.NoError(t, err)

	got, err := vs.GetSnapshot(ctx, v.VersionID)
	require.NoError(t, err)
	assert.Equal(t, []byte(payload), []byte(got), "snapshot must be byte-identical")

	// And the same bytes must surface via ListVersions.Data.
	list, err := vs.ListVersions(ctx, "doc", "snap")
	require.NoError(t, err)
	require.Len(t, list, 1)
	assert.Equal(t, []byte(payload), []byte(list[0].Data), "ListVersions.Data must be byte-identical")
}

// runDeleteHistoryCascades: deleting (typeA, id1) wipes its
// versions and snapshots while leaving (typeA, id2) and
// (typeB, id1) intact.
func runDeleteHistoryCascades(t *testing.T, vs VersionStore) {
	ctx := context.Background()

	// Target: (note, n1) — three versions.
	a1, err := vs.AppendVersion(ctx, "note", "n1", json.RawMessage(`{"v":1}`), nil)
	require.NoError(t, err)
	a2, err := vs.AppendVersion(ctx, "note", "n1", json.RawMessage(`{"v":2}`), []string{a1.VersionID})
	require.NoError(t, err)
	a3, err := vs.AppendVersion(ctx, "note", "n1", json.RawMessage(`{"v":3}`), []string{a2.VersionID})
	require.NoError(t, err)

	// Guard: same type, different id.
	g1, err := vs.AppendVersion(ctx, "note", "n2", json.RawMessage(`{"keep":"me"}`), nil)
	require.NoError(t, err)

	// Guard: different type, same id.
	g2, err := vs.AppendVersion(ctx, "task", "n1", json.RawMessage(`{"keep":"too"}`), nil)
	require.NoError(t, err)

	require.NoError(t, vs.DeleteHistory(ctx, "note", "n1"))

	// Target gone: empty list, snapshots not found.
	got, err := vs.ListVersions(ctx, "note", "n1")
	require.NoError(t, err)
	assert.Empty(t, got, "ListVersions for deleted (note, n1) must be empty")

	for _, vid := range []string{a1.VersionID, a2.VersionID, a3.VersionID} {
		_, err := vs.GetSnapshot(ctx, vid)
		assert.Error(t, err, "GetSnapshot(%s) must fail after DeleteHistory", vid)
		assert.Contains(t, err.Error(), "not found")
	}

	// Guards intact.
	guardA, err := vs.ListVersions(ctx, "note", "n2")
	require.NoError(t, err)
	require.Len(t, guardA, 1, "(note, n2) must still have its version")
	assert.Equal(t, g1.VersionID, guardA[0].VersionID)
	snapA, err := vs.GetSnapshot(ctx, g1.VersionID)
	require.NoError(t, err)
	assert.JSONEq(t, `{"keep":"me"}`, string(snapA))

	guardB, err := vs.ListVersions(ctx, "task", "n1")
	require.NoError(t, err)
	require.Len(t, guardB, 1, "(task, n1) must still have its version")
	assert.Equal(t, g2.VersionID, guardB[0].VersionID)
	snapB, err := vs.GetSnapshot(ctx, g2.VersionID)
	require.NoError(t, err)
	assert.JSONEq(t, `{"keep":"too"}`, string(snapB))
}

// runLoadDAGReconstructsParents: a branched-merge graph
// (v1 <- v2, v1 <- v3, v4 = merge(v2, v3)) reconstructs correctly.
// Heads is just {v4}; ancestors of v4 cover {v1, v2, v3}.
func runLoadDAGReconstructsParents(t *testing.T, vs VersionStore) {
	ctx := context.Background()

	v1, err := vs.AppendVersion(ctx, "doc", "merge", json.RawMessage(`{"v":1}`), nil)
	require.NoError(t, err)
	v2, err := vs.AppendVersion(ctx, "doc", "merge", json.RawMessage(`{"v":2}`), []string{v1.VersionID})
	require.NoError(t, err)
	v3, err := vs.AppendVersion(ctx, "doc", "merge", json.RawMessage(`{"v":3}`), []string{v1.VersionID})
	require.NoError(t, err)
	v4, err := vs.AppendVersion(ctx, "doc", "merge", json.RawMessage(`{"v":4}`), []string{v2.VersionID, v3.VersionID})
	require.NoError(t, err)

	dag, err := vs.LoadDAG(ctx, "doc", "merge")
	require.NoError(t, err)

	for _, vid := range []string{v1.VersionID, v2.VersionID, v3.VersionID, v4.VersionID} {
		_, ok := dag.Get(vid)
		assert.True(t, ok, "DAG must contain %s", vid)
	}

	got4, ok := dag.Get(v4.VersionID)
	require.True(t, ok)
	// Parent order is preserved across both backends.
	assert.Equal(t, []string{v2.VersionID, v3.VersionID}, got4.ParentIDs, "merge commit parents must be [v2, v3]")

	heads := dag.Heads()
	assert.Equal(t, []string{v4.VersionID}, heads, "v4 is the only head after a branched merge")

	ancestors := dag.Ancestors(v4.VersionID)
	sort.Strings(ancestors)
	expected := []string{v1.VersionID, v2.VersionID, v3.VersionID}
	sort.Strings(expected)
	assert.Equal(t, expected, ancestors, "ancestors of v4 must be {v1, v2, v3}")
}

// runEmptyHistory:
//   - ListVersions for an unknown (type, id) returns empty (no error).
//   - LoadDAG for an unknown (type, id) returns empty-but-non-nil.
//   - GetSnapshot for an unknown version ID returns a not-found error.
func runEmptyHistory(t *testing.T, vs VersionStore) {
	ctx := context.Background()

	list, err := vs.ListVersions(ctx, "ghost", "missing")
	require.NoError(t, err)
	assert.Empty(t, list, "ListVersions on unknown (type, id) must be empty without erroring")

	dag, err := vs.LoadDAG(ctx, "ghost", "missing")
	require.NoError(t, err)
	require.NotNil(t, dag, "LoadDAG on unknown (type, id) must return a non-nil empty DAG")
	assert.Empty(t, dag.Heads(), "empty DAG has no heads")

	_, err = vs.GetSnapshot(ctx, "ghost-version-id")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "not found")
}

// runUnknownParentRejected: AppendVersion with a parent that has
// never been recorded fails, and the version is not persisted.
func runUnknownParentRejected(t *testing.T, vs VersionStore) {
	ctx := context.Background()

	_, err := vs.AppendVersion(ctx, "doc", "orphan", json.RawMessage(`{"v":1}`), []string{"never-existed"})
	require.Error(t, err, "AppendVersion with unknown parent must error")

	// The contract doesn't pin the exact wording, but both backends
	// surface "unknown parent" in the message. Soft-check for it
	// without making this test overly tight.
	assert.Contains(t, err.Error(), "unknown parent")

	// Failed append must not leave a row behind.
	list, err := vs.ListVersions(ctx, "doc", "orphan")
	require.NoError(t, err)
	assert.Empty(t, list, "failed AppendVersion must not persist a row")
}

// runCrossDocumentParentRejected: AppendVersion must validate
// parents against the target document's history, not globally.
// Passing a version_id that exists under a different (type, id)
// must fail the same way an entirely-unknown parent does.
//
// This guards a real bug class: a global parent lookup would
// persist a cross-document edge in version_parents, then
// LoadDAG for the child document would fail (the parent isn't
// in its per-document DAG) and DeleteHistory for the parent
// document could be blocked by the FK on version_parents.
func runCrossDocumentParentRejected(t *testing.T, vs VersionStore) {
	ctx := context.Background()

	// Append v1 under (note, n1).
	v1, err := vs.AppendVersion(ctx, "note", "n1", json.RawMessage(`{"v":1}`), nil)
	require.NoError(t, err)

	// Attempt to append under (note, n2) using v1 (which belongs
	// to n1) as a parent. Must error.
	_, err = vs.AppendVersion(ctx, "note", "n2", json.RawMessage(`{"v":1}`), []string{v1.VersionID})
	require.Error(t, err, "AppendVersion with a parent from a different (type, id) must error")
	assert.Contains(t, err.Error(), "unknown parent")

	// And the same across types — version_id from (note, n1) must
	// not be accepted as a parent of (task, n1).
	_, err = vs.AppendVersion(ctx, "task", "n1", json.RawMessage(`{"v":1}`), []string{v1.VersionID})
	require.Error(t, err, "AppendVersion with a parent from a different type must error")
	assert.Contains(t, err.Error(), "unknown parent")

	// Failed appends must not persist rows.
	listN2, err := vs.ListVersions(ctx, "note", "n2")
	require.NoError(t, err)
	assert.Empty(t, listN2, "rejected (note, n2) append must not leave a row")

	listTask, err := vs.ListVersions(ctx, "task", "n1")
	require.NoError(t, err)
	assert.Empty(t, listTask, "rejected (task, n1) append must not leave a row")

	// Original (note, n1) intact.
	listN1, err := vs.ListVersions(ctx, "note", "n1")
	require.NoError(t, err)
	require.Len(t, listN1, 1)
	assert.Equal(t, v1.VersionID, listN1[0].VersionID)
}

// runLoadDAGCacheCoherence: under concurrent LoadDAG + AppendVersion
// on the same (docType, id), the post-quiescence LoadDAG must
// return a DAG containing every committed version.
//
// Guards a real bug class in the SQLite backend: a naive miss-path
// "build → store" sequence races against AppendVersion's
// invalidation. If the build runs against a row snapshot taken
// before a concurrent commit, then stores into the cache after
// AppendVersion's invalidation, subsequent LoadDAG calls return
// the stale DAG (missing the newest version) until another write
// invalidates the cache. The fix uses a per-key generation
// counter to detect the race and skip caching the stale DAG.
//
// In-memory backend has no cache layer to race against — the test
// passes trivially there but still asserts the contract.
func runLoadDAGCacheCoherence(t *testing.T, vs VersionStore) {
	ctx := context.Background()

	// Seed the document so LoadDAG has something to find on the
	// first miss.
	first, err := vs.AppendVersion(ctx, "doc", "race", json.RawMessage(`{"v":0}`), nil)
	require.NoError(t, err)

	const (
		writers         = 4
		readers         = 4
		writesPerWriter = 25
		readsPerReader  = 100
	)

	var wg sync.WaitGroup
	wg.Add(writers + readers)
	errCh := make(chan error, (writers*writesPerWriter)+(readers*readsPerReader))

	// Writers append linearly. Each writer claims its own seq range
	// but they all target the same (type, id), so the per-document
	// chain is contended.
	type writeResult struct {
		seq int
		vid string
	}
	results := make(chan writeResult, writers*writesPerWriter)
	for w := 0; w < writers; w++ {
		go func(w int) {
			defer wg.Done()
			for i := 0; i < writesPerWriter; i++ {
				v, err := vs.AppendVersion(ctx, "doc", "race",
					json.RawMessage(fmt.Sprintf(`{"w":%d,"i":%d}`, w, i)), nil)
				if err != nil {
					errCh <- fmt.Errorf("writer %d step %d: %w", w, i, err)
					return
				}
				results <- writeResult{seq: v.Seq, vid: v.VersionID}
			}
		}(w)
	}

	// Readers pound LoadDAG. We don't assert anything about
	// in-flight reads (a build mid-write may legitimately see
	// fewer-than-final versions). The post-quiescence assertion
	// after wg.Wait() is what catches the bug.
	for r := 0; r < readers; r++ {
		go func() {
			defer wg.Done()
			for i := 0; i < readsPerReader; i++ {
				_, err := vs.LoadDAG(ctx, "doc", "race")
				if err != nil {
					errCh <- fmt.Errorf("LoadDAG: %w", err)
					return
				}
			}
		}()
	}

	wg.Wait()
	close(results)
	close(errCh)
	for err := range errCh {
		t.Errorf("concurrent op: %v", err)
	}

	// Total expected version count: the seed + every writer's
	// successful appends.
	expected := 1 + writers*writesPerWriter

	// Post-quiescence LoadDAG must reflect every committed write.
	// Pre-fix this assertion fails intermittently because a mid-
	// flight LoadDAG cached a DAG that's missing the most-recent
	// AppendVersion(s).
	dag, err := vs.LoadDAG(ctx, "doc", "race")
	require.NoError(t, err)

	// Confirm via Heads() the DAG has every version. Because every
	// AppendVersion above was made with parents=nil, every version
	// is its own head — so |Heads| equals the version count for
	// this test.
	require.Len(t, dag.Heads(), expected,
		"post-quiescence DAG must contain every committed version (%d expected, got %d heads)",
		expected, len(dag.Heads()))

	// Sanity: the very first version is in the DAG.
	_, ok := dag.Get(first.VersionID)
	require.True(t, ok, "first version must be in the DAG")

	// Cross-check via ListVersions, which reads directly from the
	// store with no cache.
	listed, err := vs.ListVersions(ctx, "doc", "race")
	require.NoError(t, err)
	require.Len(t, listed, expected,
		"ListVersions must report every committed version (cache-bypass cross-check)")

	// Sort heads + listed VersionIDs for a clean equivalence check.
	heads := dag.Heads()
	sort.Strings(heads)
	listedIDs := make([]string, len(listed))
	for i, v := range listed {
		listedIDs[i] = v.VersionID
	}
	sort.Strings(listedIDs)
	assert.Equal(t, listedIDs, heads,
		"DAG heads must match ListVersions IDs")
}

// versionedBackendFactory pairs a backend name with a constructor
// that returns a fresh, isolated [*VersionedDocumentStore] for the
// branching-conformance scenario that asks for one. Distinct from
// [versionedFactory] in versioned_test.go (which round-trips through
// Close/reopen for durability assertions): the branching-conformance
// scenarios run a single in-process flow per case, so a plain
// open-only factory is enough.
type versionedBackendFactory struct {
	name string
	make func(t *testing.T) *VersionedDocumentStore
}

// versionedConformanceFactories returns every backend pairing the
// branching-conformance suite must cover. Add a new (DocumentStore,
// VersionStore) pairing here and every branching scenario picks it up
// automatically. Mirrors [conformanceBackends] but yields the wider
// [*VersionedDocumentStore] needed to exercise Fork / Merge /
// Branches — methods that live on the wrapper, not on the bare
// [VersionStore] interface.
func versionedConformanceFactories() []versionedBackendFactory {
	return []versionedBackendFactory{
		{
			name: "in-memory",
			make: func(t *testing.T) *VersionedDocumentStore {
				t.Helper()
				return NewInMemoryVersionedDocumentStore(newTestStore(t))
			},
		},
		{
			name: "sqlite",
			make: func(t *testing.T) *VersionedDocumentStore {
				t.Helper()
				return openConformanceSQLiteVersionedStore(t)
			},
		},
	}
}

// openConformanceSQLiteVersionedStore mirrors
// [openConformanceSQLiteVersionStore] but wires a full
// [*VersionedDocumentStore] over a shared on-disk SQLite — the
// production wiring kit serve uses. Both DocumentStore and
// VersionStore share the same *sql.DB so transactional code paths
// engage as they would in production.
func openConformanceSQLiteVersionedStore(t *testing.T) *VersionedDocumentStore {
	t.Helper()
	dbPath := filepath.Join(t.TempDir(), "branching-conformance.db")
	ds, err := NewDocumentStore(dbPath)
	require.NoError(t, err)
	t.Cleanup(func() { _ = ds.Close() })

	vs, err := NewSQLiteVersionStore(ds.DB())
	require.NoError(t, err)
	return NewVersionedDocumentStore(ds, vs)
}

// TestVersionedDocumentStoreBranchingConformance is the cross-backend
// conformance suite for the public branching API — Fork, Merge,
// Branches — added by the engine-versioned-branching track. It runs
// each scenario against every backend wired in
// [versionedConformanceFactories], with the same expand-into-subtests
// pattern as [TestVersionStoreConformance]. Use
// `go test -run TestVersionedDocumentStoreBranchingConformance/sqlite/MergePreservesParentOrder`
// to target a specific cell.
//
// The bare-VersionStore conformance suite stays focused on the
// interface contract; this top-level test asserts the public
// VersionedDocumentStore API the engine HTTP/WS layer hits.
func TestVersionedDocumentStoreBranchingConformance(t *testing.T) {
	type scenario struct {
		name string
		run  func(t *testing.T, vds *VersionedDocumentStore)
	}
	scenarios := []scenario{
		{name: "ForkMaterializesNewVersion", run: runForkMaterializesNewVersion},
		{name: "MergePreservesParentOrder", run: runMergePreservesParentOrder},
		{name: "BranchesIdentifiesHeads", run: runBranchesIdentifiesHeads},
		{name: "BranchedRevertConsistency", run: runBranchedRevertConsistency},
	}

	for _, f := range versionedConformanceFactories() {
		f := f
		t.Run(f.name, func(t *testing.T) {
			for _, sc := range scenarios {
				sc := sc
				t.Run(sc.name, func(t *testing.T) {
					sc.run(t, f.make(t))
				})
			}
		})
	}
}

// runForkMaterializesNewVersion: Fork at an older seq materializes a
// sibling version carrying that seq's snapshot. The new version
// becomes the latest seq, so the DAG ends with two heads (the
// original linear tip + the fork sibling). A second Fork at the same
// fromSeq materializes a third sibling — divergence is expressed by
// repeated Fork calls in MVP (spec §3 decision 1).
func runForkMaterializesNewVersion(t *testing.T, vds *VersionedDocumentStore) {
	ctx := context.Background()

	// Linear v1, v2, v3 via Create + 2 Updates.
	_, err := vds.Create(ctx, "note", json.RawMessage(`{"id":"n1","v":1}`))
	require.NoError(t, err)
	_, err = vds.Update(ctx, "note", "n1", json.RawMessage(`{"id":"n1","v":2}`))
	require.NoError(t, err)
	_, err = vds.Update(ctx, "note", "n1", json.RawMessage(`{"id":"n1","v":3}`))
	require.NoError(t, err)

	hist, err := vds.History(ctx, "note", "n1")
	require.NoError(t, err)
	require.Len(t, hist, 3)
	v2 := hist[1]
	v3 := hist[2]

	// Fork at seq 2: appends a sibling of v2 carrying v2's data. The
	// fork tip becomes seq 4 (latest) and parents on v2.
	fork1, err := vds.Fork(ctx, "note", "n1", 2)
	require.NoError(t, err)
	assert.Equal(t, 4, fork1.Seq, "fork tip is the latest seq after Fork appends")
	assert.JSONEq(t, `{"id":"n1","v":2}`, string(fork1.Data),
		"fork tip carries fromSeq's snapshot bytes")

	dag, err := vds.versions.LoadDAG(ctx, "note", "n1")
	require.NoError(t, err)
	forkNode, ok := dag.Get(fork1.VersionID)
	require.True(t, ok, "fork tip reachable via DAG")
	assert.Equal(t, []string{v2.VersionID}, forkNode.ParentIDs,
		"fork tip's parent is fromSeq's version_id")

	// Branches surfaces both heads: linear tip (seq 3) + fork sibling (seq 4).
	heads, err := vds.Branches(ctx, "note", "n1")
	require.NoError(t, err)
	require.Len(t, heads, 2, "post-Fork DAG has two heads")
	headSeqs := []int{heads[0].Seq, heads[1].Seq}
	sort.Ints(headSeqs)
	assert.Equal(t, []int{3, 4}, headSeqs,
		"heads are linear tip seq 3 + fork sibling seq 4")

	// A second Fork at the same fromSeq materializes a third sibling.
	// MVP idempotency reading: repeated Fork calls each produce a new
	// row (spec §3 decision 1).
	fork2, err := vds.Fork(ctx, "note", "n1", 2)
	require.NoError(t, err)
	assert.Equal(t, 5, fork2.Seq)
	assert.JSONEq(t, `{"id":"n1","v":2}`, string(fork2.Data))
	assert.NotEqual(t, fork1.VersionID, fork2.VersionID,
		"second Fork at same fromSeq materializes a distinct sibling")

	// Total versions: 3 linear + 2 forks = 5.
	hist2, err := vds.History(ctx, "note", "n1")
	require.NoError(t, err)
	require.Len(t, hist2, 5)

	// Three heads now: seq 3 (linear), seq 4 (first fork), seq 5 (second fork).
	heads2, err := vds.Branches(ctx, "note", "n1")
	require.NoError(t, err)
	require.Len(t, heads2, 3, "two siblings + linear tip = three heads")
	headSeqs2 := []int{heads2[0].Seq, heads2[1].Seq, heads2[2].Seq}
	sort.Ints(headSeqs2)
	assert.Equal(t, []int{3, 4, 5}, headSeqs2)

	// Sanity: v3 is still a head (the linear chain wasn't disturbed).
	headIDSet := map[string]struct{}{}
	for _, h := range heads2 {
		headIDSet[h.VersionID] = struct{}{}
	}
	_, hasV3 := headIDSet[v3.VersionID]
	assert.True(t, hasV3, "original linear tip remains a head after siblings appended")
}

// runMergePreservesParentOrder is the bug-catching scenario for the
// SQLite buildDAG ORDER BY rowid fix in fe875f7. The contract: a
// Merge(sourceSeq, targetSeq, ...) records parents
// [sourceVersionID, targetVersionID] in that order (spec §4). Pre-fix
// the SQLite backend returned parents lex-by-parent_id, so the order
// would silently rearrange after the round trip through LoadDAG.
//
// The existing LoadDAGReconstructsParents scenario in this file
// happens to assert merge parents in order, but only for one merge
// shape with version IDs that happen to sort correctly under lex
// ordering on some seeds. This scenario locks down ordering for both
// (source, target) directions so neither lex nor reverse-lex luck can
// hide a regression.
func runMergePreservesParentOrder(t *testing.T, vds *VersionedDocumentStore) {
	ctx := context.Background()

	// Linear v1, v2, v3.
	_, err := vds.Create(ctx, "note", json.RawMessage(`{"id":"n1","v":1}`))
	require.NoError(t, err)
	_, err = vds.Update(ctx, "note", "n1", json.RawMessage(`{"id":"n1","v":2}`))
	require.NoError(t, err)
	_, err = vds.Update(ctx, "note", "n1", json.RawMessage(`{"id":"n1","v":3}`))
	require.NoError(t, err)

	// Fork at seq 2 → seq 4 (sibling of v3).
	_, err = vds.Fork(ctx, "note", "n1", 2)
	require.NoError(t, err)

	hist, err := vds.History(ctx, "note", "n1")
	require.NoError(t, err)
	require.Len(t, hist, 4)
	v3 := hist[2]
	v4 := hist[3]

	// Merge(source=3, target=4) → seq 5. Parents must be [v3, v4].
	merge1, err := vds.Merge(ctx, "note", "n1", 3, 4,
		json.RawMessage(`{"id":"n1","v":"merged-3-into-4"}`))
	require.NoError(t, err)
	assert.Equal(t, 5, merge1.Seq)

	dag, err := vds.versions.LoadDAG(ctx, "note", "n1")
	require.NoError(t, err)
	merge1Node, ok := dag.Get(merge1.VersionID)
	require.True(t, ok)
	require.Len(t, merge1Node.ParentIDs, 2, "merge records two parent edges")
	assert.Equal(t,
		[]string{v3.VersionID, v4.VersionID},
		merge1Node.ParentIDs,
		"Merge(source=3, target=4) parents must be [v3, v4] in that exact order")

	// Now the reverse: Merge(source=4, target=3) → seq 6, parents [v4, v3].
	// This catches any backend that "normalizes" parent order (e.g. by
	// seq, by rowid of the parent row, by lex on version_id) instead
	// of preserving the call-site order.
	merge2, err := vds.Merge(ctx, "note", "n1", 4, 3,
		json.RawMessage(`{"id":"n1","v":"merged-4-into-3"}`))
	require.NoError(t, err)
	assert.Equal(t, 6, merge2.Seq)

	dag, err = vds.versions.LoadDAG(ctx, "note", "n1")
	require.NoError(t, err)
	merge2Node, ok := dag.Get(merge2.VersionID)
	require.True(t, ok)
	require.Len(t, merge2Node.ParentIDs, 2)
	assert.Equal(t,
		[]string{v4.VersionID, v3.VersionID},
		merge2Node.ParentIDs,
		"Merge(source=4, target=3) parents must be [v4, v3] — reverse order preserved")
}

// runBranchesIdentifiesHeads: head detection across linear, forked,
// merged, two-level forked, and empty topologies. Branches() returns
// versions ordered ascending by seq (callers reverse for
// most-recent-first); each topology pins the expected head count and
// the seq of the heads.
func runBranchesIdentifiesHeads(t *testing.T, vds *VersionedDocumentStore) {
	ctx := context.Background()

	// Empty document: Branches returns an error matching History's
	// shape (spec §4: "no history" surfaces as a higher-level error).
	_, err := vds.Branches(ctx, "ghost", "missing")
	require.Error(t, err, "Branches on unknown doc returns an error")

	// Linear: Create + 2 Updates → 1 head.
	_, err = vds.Create(ctx, "note", json.RawMessage(`{"id":"linear","v":1}`))
	require.NoError(t, err)
	_, err = vds.Update(ctx, "note", "linear", json.RawMessage(`{"id":"linear","v":2}`))
	require.NoError(t, err)
	_, err = vds.Update(ctx, "note", "linear", json.RawMessage(`{"id":"linear","v":3}`))
	require.NoError(t, err)

	heads, err := vds.Branches(ctx, "note", "linear")
	require.NoError(t, err)
	require.Len(t, heads, 1, "linear history has exactly one head")
	assert.Equal(t, 3, heads[0].Seq, "linear head is the latest seq")

	// Forked: Fork at seq 2 on a fresh doc → 2 heads.
	_, err = vds.Create(ctx, "note", json.RawMessage(`{"id":"forked","v":1}`))
	require.NoError(t, err)
	_, err = vds.Update(ctx, "note", "forked", json.RawMessage(`{"id":"forked","v":2}`))
	require.NoError(t, err)
	_, err = vds.Update(ctx, "note", "forked", json.RawMessage(`{"id":"forked","v":3}`))
	require.NoError(t, err)
	_, err = vds.Fork(ctx, "note", "forked", 2)
	require.NoError(t, err)

	heads, err = vds.Branches(ctx, "note", "forked")
	require.NoError(t, err)
	require.Len(t, heads, 2, "forked history surfaces two heads")
	headSeqs := []int{heads[0].Seq, heads[1].Seq}
	assert.True(t, sort.IntsAreSorted(headSeqs),
		"heads ordered by ascending seq: %v", headSeqs)
	assert.Equal(t, []int{3, 4}, headSeqs,
		"heads are linear tip seq 3 + fork sibling seq 4")

	// Merged: from the forked doc, Merge → collapses to 1 head.
	merge, err := vds.Merge(ctx, "note", "forked", 3, 4,
		json.RawMessage(`{"id":"forked","v":"merged"}`))
	require.NoError(t, err)

	heads, err = vds.Branches(ctx, "note", "forked")
	require.NoError(t, err)
	require.Len(t, heads, 1, "Merge collapses heads to the merge tip")
	assert.Equal(t, merge.VersionID, heads[0].VersionID)
	assert.Equal(t, 5, heads[0].Seq, "merge tip is the latest seq")

	// Two-level forked: Fork at seq 2, then Fork at seq 2 again on a
	// fresh doc → 3 heads (linear tip + two siblings). No merge.
	_, err = vds.Create(ctx, "note", json.RawMessage(`{"id":"twolevel","v":1}`))
	require.NoError(t, err)
	_, err = vds.Update(ctx, "note", "twolevel", json.RawMessage(`{"id":"twolevel","v":2}`))
	require.NoError(t, err)
	_, err = vds.Update(ctx, "note", "twolevel", json.RawMessage(`{"id":"twolevel","v":3}`))
	require.NoError(t, err)
	_, err = vds.Fork(ctx, "note", "twolevel", 2)
	require.NoError(t, err)
	_, err = vds.Fork(ctx, "note", "twolevel", 2)
	require.NoError(t, err)

	heads, err = vds.Branches(ctx, "note", "twolevel")
	require.NoError(t, err)
	require.Len(t, heads, 3, "two-level forked history has three heads")
	headSeqs = []int{heads[0].Seq, heads[1].Seq, heads[2].Seq}
	assert.True(t, sort.IntsAreSorted(headSeqs),
		"heads ordered by ascending seq: %v", headSeqs)
	assert.Equal(t, []int{3, 4, 5}, headSeqs,
		"linear tip + two fork siblings")
}

// runBranchedRevertConsistency locks down Revert's behavior in a
// branched context. Revert reuses Update internally (versioned.go
// L415), and Update parents on the most-recent version overall
// (parentsFor returns the last entry of ListVersions, which is the
// latest seq across the whole DAG, not per-branch). So in a branched
// history Revert appends a new version whose parent is the latest seq
// of any branch — not necessarily the branch the reverted seq lives
// on. This is the implementation's actual behavior and the contract
// this scenario pins.
//
// If a future refactor makes Revert branch-aware (e.g. parents on the
// branch the reverted seq lives on, or on a caller-supplied head),
// this scenario breaks deliberately so the contract change is
// explicit.
func runBranchedRevertConsistency(t *testing.T, vds *VersionedDocumentStore) {
	ctx := context.Background()

	// Linear v1, v2, v3.
	_, err := vds.Create(ctx, "note", json.RawMessage(`{"id":"n1","v":1}`))
	require.NoError(t, err)
	_, err = vds.Update(ctx, "note", "n1", json.RawMessage(`{"id":"n1","v":2}`))
	require.NoError(t, err)
	_, err = vds.Update(ctx, "note", "n1", json.RawMessage(`{"id":"n1","v":3}`))
	require.NoError(t, err)

	// Fork at seq 2 twice → seq 4, seq 5 (two siblings).
	_, err = vds.Fork(ctx, "note", "n1", 2)
	require.NoError(t, err)
	_, err = vds.Fork(ctx, "note", "n1", 2)
	require.NoError(t, err)

	hist, err := vds.History(ctx, "note", "n1")
	require.NoError(t, err)
	require.Len(t, hist, 5)
	v2 := hist[1]
	v5 := hist[4]

	// Revert(seq=2). Per the implementation: new version with
	// data=v2.Data, seq=6, parents=[v5.VersionID] (the latest seq
	// overall before this call).
	doc, err := vds.Revert(ctx, "note", "n1", 2)
	require.NoError(t, err)
	assert.JSONEq(t, `{"id":"n1","v":2}`, string(doc.Data),
		"Revert restores the target seq's data")

	hist, err = vds.History(ctx, "note", "n1")
	require.NoError(t, err)
	require.Len(t, hist, 6, "Revert appends a new version")

	revertVer := hist[5]
	assert.Equal(t, 6, revertVer.Seq, "Revert appends with the next seq")
	assert.JSONEq(t, `{"id":"n1","v":2}`, string(revertVer.Data),
		"reverted version carries the target seq's snapshot")

	dag, err := vds.versions.LoadDAG(ctx, "note", "n1")
	require.NoError(t, err)
	revertNode, ok := dag.Get(revertVer.VersionID)
	require.True(t, ok, "reverted version reachable via DAG")
	assert.Equal(t,
		[]string{v5.VersionID},
		revertNode.ParentIDs,
		"Revert parents on the latest seq overall (Update semantics) — not on the reverted seq")

	// Sanity: the v2 row itself is still in history at its original
	// seq, and the DAG still has it as a node.
	assert.Equal(t, 2, v2.Seq)
	_, ok = dag.Get(v2.VersionID)
	assert.True(t, ok, "original v2 reachable via DAG after Revert")

	// History is ordered by ascending seq across all branches.
	for i, v := range hist {
		assert.Equal(t, i+1, v.Seq, "History[%d].Seq must be %d", i, i+1)
	}

	// Branches: pre-Revert there were 3 heads (seq 3, 4, 5). After
	// Revert appended seq 6 parenting on seq 5, seq 5 is no longer a
	// head — so heads = {seq 3, seq 4, seq 6}.
	heads, err := vds.Branches(ctx, "note", "n1")
	require.NoError(t, err)
	require.Len(t, heads, 3, "Revert parents on seq 5, leaving 3 heads")
	headSeqs := []int{heads[0].Seq, heads[1].Seq, heads[2].Seq}
	sort.Ints(headSeqs)
	assert.Equal(t, []int{3, 4, 6}, headSeqs,
		"heads are seq 3 (linear tip), seq 4 (other fork sibling), seq 6 (revert tip)")
}

// runDedupReusesIdenticalSnapshots: two AppendVersion calls under
// the same (type, id) carrying byte-identical payloads produce two
// distinct version_ids that resolve to byte-identical snapshots.
// DeleteHistory then drops both versions cleanly — observable proof
// that the underlying refcount went 1 → 2 (second append) and back
// 2 → 1 → 0 (delete iterating both versions) without leaking or
// underflowing.
//
// Why this is meaningful at the conformance level despite dedup
// being a storage optimization: the public-API behavior under
// repeated identical payloads is exactly the same shape as under
// distinct payloads (covered by AppendLinear / DeleteHistoryCascades),
// but the underlying code path is different (blob upsert with
// refcount bump vs. fresh insert). This scenario exercises that
// alternate path through the public surface only — any backend
// that gets dedup wrong (e.g. fails the second append, returns
// stale bytes from the first version, leaks a blob on delete) will
// trip on at least one of the assertions below.
func runDedupReusesIdenticalSnapshots(t *testing.T, vs VersionStore) {
	ctx := context.Background()

	payload := json.RawMessage(`{"v":1}`)

	v1, err := vs.AppendVersion(ctx, "note", "n1", payload, nil)
	require.NoError(t, err)

	// Second append, same (type, id), same payload. Must succeed
	// (no spurious ErrHashCollision) and return a distinct version_id
	// — version IDs are per-version, not per-blob.
	v2, err := vs.AppendVersion(ctx, "note", "n1", payload, []string{v1.VersionID})
	require.NoError(t, err)
	assert.NotEqual(t, v1.VersionID, v2.VersionID,
		"identical payload must still produce a distinct version_id")
	assert.Equal(t, 2, v2.Seq)

	// Both snapshots resolve to the same bytes.
	got1, err := vs.GetSnapshot(ctx, v1.VersionID)
	require.NoError(t, err)
	assert.Equal(t, []byte(payload), []byte(got1))

	got2, err := vs.GetSnapshot(ctx, v2.VersionID)
	require.NoError(t, err)
	assert.Equal(t, []byte(payload), []byte(got2))
	assert.Equal(t, []byte(got1), []byte(got2),
		"both versions resolve to byte-identical snapshots")

	// DeleteHistory iterates both versions; the underlying refcount
	// goes 2 → 1 → 0 and the blob is removed. Observable through
	// both GetSnapshots failing with not-found.
	require.NoError(t, vs.DeleteHistory(ctx, "note", "n1"))

	for _, vid := range []string{v1.VersionID, v2.VersionID} {
		_, err := vs.GetSnapshot(ctx, vid)
		require.Error(t, err, "GetSnapshot(%s) must fail after DeleteHistory", vid)
		assert.Contains(t, err.Error(), "not found")
	}

	list, err := vs.ListVersions(ctx, "note", "n1")
	require.NoError(t, err)
	assert.Empty(t, list, "ListVersions must be empty after DeleteHistory")
}

// runDedupCrossDocumentSharing: two distinct (type, id) documents
// each append the same payload bytes. Both GetSnapshots succeed and
// return identical bytes. Deleting one document leaves the other's
// snapshot fully intact — observable proof that the shared blob's
// refcount went 1 → 2 (second cross-doc append) and 2 → 1 (first
// delete) without taking the second doc down with it.
//
// This is the dedup-headline-win scenario at the conformance level:
// pre-dedup, two writes produced two physically separate snapshots
// table rows; post-dedup, one row with refcount=2. The public-API
// observable is the same — but a backend that doesn't share blobs
// across documents would still pass it (the surface is identical).
// The subsequent half-delete is what catches a backend that scopes
// blobs per-document and would orphan the survivor.
func runDedupCrossDocumentSharing(t *testing.T, vs VersionStore) {
	ctx := context.Background()

	payload := json.RawMessage(`{"shared":"value"}`)

	// Append the same payload under (note, n1) and (task, t1) —
	// distinct (type, id) pairs.
	a1, err := vs.AppendVersion(ctx, "note", "n1", payload, nil)
	require.NoError(t, err)

	b1, err := vs.AppendVersion(ctx, "task", "t1", payload, nil)
	require.NoError(t, err)
	assert.NotEqual(t, a1.VersionID, b1.VersionID,
		"cross-doc identical payload must still produce distinct version_ids")

	// Both resolve to byte-identical snapshots.
	gotA, err := vs.GetSnapshot(ctx, a1.VersionID)
	require.NoError(t, err)
	assert.Equal(t, []byte(payload), []byte(gotA))

	gotB, err := vs.GetSnapshot(ctx, b1.VersionID)
	require.NoError(t, err)
	assert.Equal(t, []byte(payload), []byte(gotB))

	// Delete (note, n1). The shared blob's refcount drops 2 → 1; the
	// other doc's snapshot must still resolve.
	require.NoError(t, vs.DeleteHistory(ctx, "note", "n1"))

	// (note, n1) is gone.
	_, err = vs.GetSnapshot(ctx, a1.VersionID)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "not found")

	listA, err := vs.ListVersions(ctx, "note", "n1")
	require.NoError(t, err)
	assert.Empty(t, listA)

	// (task, t1) is fully intact — GetSnapshot returns identical
	// bytes, ListVersions still surfaces the row.
	gotB2, err := vs.GetSnapshot(ctx, b1.VersionID)
	require.NoError(t, err)
	assert.Equal(t, []byte(payload), []byte(gotB2),
		"surviving doc's snapshot bytes intact after sibling DeleteHistory")

	listB, err := vs.ListVersions(ctx, "task", "t1")
	require.NoError(t, err)
	require.Len(t, listB, 1)
	assert.Equal(t, b1.VersionID, listB[0].VersionID)

	// Final delete drops the blob to zero; surviving doc's snapshot
	// no longer resolves.
	require.NoError(t, vs.DeleteHistory(ctx, "task", "t1"))
	_, err = vs.GetSnapshot(ctx, b1.VersionID)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "not found")
}

// runRefcountedDeleteCascadesCleanly: three sequential AppendVersion
// calls under the same (type, id) carry byte-identical payloads, so
// the underlying refcount climbs 1 → 2 → 3. DeleteHistory must walk
// all three versions and decrement the refcount cleanly back to zero
// (no underflow surfaced, no orphan blob left behind).
//
// Overlaps surface-wise with DeleteHistoryCascades, but exercises a
// distinct internal path: same-hash repeated decrements rather than
// distinct-hash one-decrement-each. A backend that gets the
// decrement-loop wrong (e.g. deletes the blob after the first
// decrement, then underflows on the second/third) only trips here,
// not on DeleteHistoryCascades.
func runRefcountedDeleteCascadesCleanly(t *testing.T, vs VersionStore) {
	ctx := context.Background()

	payload := json.RawMessage(`{"same":"data"}`)

	v1, err := vs.AppendVersion(ctx, "note", "n1", payload, nil)
	require.NoError(t, err)
	v2, err := vs.AppendVersion(ctx, "note", "n1", payload, []string{v1.VersionID})
	require.NoError(t, err)
	v3, err := vs.AppendVersion(ctx, "note", "n1", payload, []string{v2.VersionID})
	require.NoError(t, err)

	// All three appends visible; all three snapshots resolve to the
	// same bytes (refcount=3 conceptually, observable via behavior).
	list, err := vs.ListVersions(ctx, "note", "n1")
	require.NoError(t, err)
	require.Len(t, list, 3)
	for i, v := range list {
		assert.Equal(t, i+1, v.Seq)
	}
	for _, vid := range []string{v1.VersionID, v2.VersionID, v3.VersionID} {
		got, err := vs.GetSnapshot(ctx, vid)
		require.NoError(t, err)
		assert.Equal(t, []byte(payload), []byte(got),
			"snapshot for %s must equal payload", vid)
	}

	// One DeleteHistory call walks all three versions and decrements
	// the same blob three times — must not underflow, must not leak.
	require.NoError(t, vs.DeleteHistory(ctx, "note", "n1"),
		"DeleteHistory must not surface ErrRefcountUnderflow when a "+
			"single blob is referenced N times by N versions of one doc")

	// All three versions gone.
	listAfter, err := vs.ListVersions(ctx, "note", "n1")
	require.NoError(t, err)
	assert.Empty(t, listAfter)

	for _, vid := range []string{v1.VersionID, v2.VersionID, v3.VersionID} {
		_, err := vs.GetSnapshot(ctx, vid)
		require.Error(t, err, "GetSnapshot(%s) must fail after DeleteHistory", vid)
		assert.Contains(t, err.Error(), "not found")
	}

	// And the (type, id) is in a clean state for re-use: appending a
	// fresh version with the same payload starts again at seq=1 and
	// re-creates the blob. This is the smoke test that DeleteHistory
	// truly bottomed out at refcount=0 (not, say, refcount=-1 hidden
	// by an unsigned cast somewhere).
	fresh, err := vs.AppendVersion(ctx, "note", "n1", payload, nil)
	require.NoError(t, err)
	assert.Equal(t, 1, fresh.Seq, "post-delete fresh append starts at seq=1")
	gotFresh, err := vs.GetSnapshot(ctx, fresh.VersionID)
	require.NoError(t, err)
	assert.Equal(t, []byte(payload), []byte(gotFresh))
}

// runSeqMonotonicAcrossPrune is the T-0432 regression scenario:
// AppendVersion -> DeleteVersions (Prune) -> AppendVersion must
// never reuse a seq already issued for that (type, id), and both
// backends must produce the same seq + version_id for the
// post-Prune append. Pre-fix the in-memory backend used
// len(existing)+1 and the SQLite backend used MAX(seq)+1; both
// reissued seq=N after the row holding seq=N was pruned, so a
// second append after Prune deterministically collided on
// version_id (util.Short("type:id-seq-data")).
//
// This scenario nails down the conformance contract so any future
// backend that gets the seq derivation wrong fails here, not deep
// in the property test.
func runSeqMonotonicAcrossPrune(t *testing.T, vs VersionStore) {
	ctx := context.Background()
	const docType, id = "doc", "monotonic"

	// Append three sequential versions: seq 1, 2, 3.
	v1, err := vs.AppendVersion(ctx, docType, id, json.RawMessage(`{"v":1}`), nil)
	require.NoError(t, err)
	require.Equal(t, 1, v1.Seq)
	v2, err := vs.AppendVersion(ctx, docType, id, json.RawMessage(`{"v":2}`), []string{v1.VersionID})
	require.NoError(t, err)
	require.Equal(t, 2, v2.Seq)
	v3, err := vs.AppendVersion(ctx, docType, id, json.RawMessage(`{"v":3}`), []string{v2.VersionID})
	require.NoError(t, err)
	require.Equal(t, 3, v3.Seq)

	// Prune away seq 3 (the row holding the current MAX(seq)). Note
	// this leaves seqs {1, 2} but the high-water counter must
	// remember 3.
	freed, err := vs.DeleteVersions(ctx, docType, id, []string{v3.VersionID})
	require.NoError(t, err)
	// freed may be empty if the pruned blob is shared; we don't
	// assert on its shape here — the monotonicity property is the
	// load-bearing assertion.
	_ = freed

	// Append a fourth version. It MUST land at seq=4, not seq=3.
	// Pre-fix this returned seq=3 (in-memory: len({1,2})+1 = 3;
	// SQLite: MAX({1,2})+1 = 3) and the version_id collided with
	// the pruned v3's version_id.
	v4, err := vs.AppendVersion(ctx, docType, id, json.RawMessage(`{"v":4}`), []string{v2.VersionID})
	require.NoError(t, err, "AppendVersion after Prune of MAX(seq) row must succeed")
	assert.Equal(t, 4, v4.Seq,
		"AppendVersion after pruning seq=3 must issue seq=4, not reuse seq=3 (T-0432)")
	assert.NotEqual(t, v3.VersionID, v4.VersionID,
		"post-Prune append must NOT collide on the pruned version's version_id (T-0432)")

	// And another, just to confirm the counter keeps advancing.
	v5, err := vs.AppendVersion(ctx, docType, id, json.RawMessage(`{"v":5}`), []string{v4.VersionID})
	require.NoError(t, err)
	assert.Equal(t, 5, v5.Seq)

	// Final state: 4 retained versions with seqs {1, 2, 4, 5}. Gaps
	// are part of the contract — pruned seqs are tombstones, not
	// reusable slots.
	list, err := vs.ListVersions(ctx, docType, id)
	require.NoError(t, err)
	gotSeqs := make([]int, len(list))
	for i, v := range list {
		gotSeqs[i] = v.Seq
	}
	assert.Equal(t, []int{1, 2, 4, 5}, gotSeqs,
		"retained seqs after Prune+Append+Append must be {1,2,4,5} — pruned seqs are tombstones")
}

// runSeqRestartsAfterDeleteHistory: DeleteHistory removes the
// document outright; the high-water counter resets so a fresh
// AppendVersion under the same (type, id) starts at seq=1. This is
// the documented divergence between DeleteVersions (Prune; seq
// stays monotonic) and DeleteHistory (drop document; fresh
// lifecycle). Both backends must agree.
func runSeqRestartsAfterDeleteHistory(t *testing.T, vs VersionStore) {
	ctx := context.Background()
	const docType, id = "doc", "restart"

	// Build a small chain.
	v1, err := vs.AppendVersion(ctx, docType, id, json.RawMessage(`{"v":1}`), nil)
	require.NoError(t, err)
	v2, err := vs.AppendVersion(ctx, docType, id, json.RawMessage(`{"v":2}`), []string{v1.VersionID})
	require.NoError(t, err)
	require.Equal(t, 2, v2.Seq)

	// Drop the entire document.
	require.NoError(t, vs.DeleteHistory(ctx, docType, id))

	// A fresh append must start at seq=1, NOT seq=3 (the high-water
	// counter has been reset). This is the contract that lets
	// callers reuse a (type, id) after Delete without dragging stale
	// counters across the lifecycle boundary.
	fresh, err := vs.AppendVersion(ctx, docType, id, json.RawMessage(`{"v":1}`), nil)
	require.NoError(t, err)
	assert.Equal(t, 1, fresh.Seq,
		"AppendVersion after DeleteHistory must restart at seq=1, not continue from the pre-delete high-water")
}

// runConcurrencySmoke: N goroutines each build their own chain
// against a distinct (type, id) pair. Per-pair seq stays monotonic
// 1..M and the overall store remains correct under contention. Run
// with `-race` to surface any data races.
func runConcurrencySmoke(t *testing.T, vs VersionStore) {
	ctx := context.Background()

	const (
		workers = 8
		perKey  = 5
	)

	var wg sync.WaitGroup
	wg.Add(workers)
	errCh := make(chan error, workers*perKey)

	for w := 0; w < workers; w++ {
		go func(w int) {
			defer wg.Done()
			docType := "doc"
			id := fmt.Sprintf("worker-%d", w)
			var prev string
			for i := 1; i <= perKey; i++ {
				var parents []string
				if prev != "" {
					parents = []string{prev}
				}
				v, err := vs.AppendVersion(ctx, docType, id,
					json.RawMessage(fmt.Sprintf(`{"w":%d,"i":%d}`, w, i)), parents)
				if err != nil {
					errCh <- fmt.Errorf("worker %d step %d: %w", w, i, err)
					return
				}
				if v.Seq != i {
					errCh <- fmt.Errorf("worker %d step %d: expected seq %d, got %d", w, i, i, v.Seq)
					return
				}
				prev = v.VersionID
			}
		}(w)
	}

	wg.Wait()
	close(errCh)
	for err := range errCh {
		t.Errorf("concurrent append: %v", err)
	}

	// Verify each per-worker chain landed monotonically.
	for w := 0; w < workers; w++ {
		id := fmt.Sprintf("worker-%d", w)
		got, err := vs.ListVersions(ctx, "doc", id)
		require.NoError(t, err)
		require.Len(t, got, perKey, "worker %d should have %d versions", w, perKey)
		for i, v := range got {
			assert.Equal(t, i+1, v.Seq, "worker %d position %d", w, i)
		}
	}
}

// TestVersionedDocumentStorePruningConformance is the cross-backend
// conformance suite for the public Prune + Abandon + Branches-with-
// liveness API added by the engine-version-pruning track. It mirrors
// [TestVersionedDocumentStoreBranchingConformance] in shape: every
// scenario runs once per backend wired in
// [versionedConformanceFactories]. Use
// `go test -run TestVersionedDocumentStorePruningConformance/sqlite/PruneByCountWithAbandonedForkTail`
// to target a specific cell.
//
// The scenarios encode spec §7's locked list. The load-bearing cases
// — PruneByCountWithMergedTails and PruneByCountWithRevertOrphan —
// cross-check that Merge/Revert's automatic dead-head marking
// (decision #10) is observable through the public API on BOTH
// backends, not just the in-memory one. A divergence here surfaces a
// SetLive integration gap on the SQLite path.
func TestVersionedDocumentStorePruningConformance(t *testing.T) {
	type scenario struct {
		name string
		run  func(t *testing.T, vds *VersionedDocumentStore)
	}
	scenarios := []scenario{
		{name: "PruneByCountLinearNoOp", run: runPruneByCountLinearNoOp},
		{name: "PruneByCountWithAbandonedForkTail", run: runPruneByCountWithAbandonedForkTail},
		{name: "PruneByCountWithMergedTails", run: runPruneByCountWithMergedTails},
		{name: "PruneByCountWithRevertOrphan", run: runPruneByCountWithRevertOrphan},
		{name: "PruneByAge", run: runPruneByAge},
		{name: "PruneWithBothLimits", run: runPruneWithBothLimits},
		{name: "PruneNeverEmpties", run: runPruneNeverEmpties},
		{name: "PruneRespectsLiveBranches", run: runPruneRespectsLiveBranches},
		{name: "PruneAfterAbandoningOneOfTwoLiveHeads", run: runPruneAfterAbandoningOneOfTwoLiveHeads},
		{name: "PruneWithDedup", run: runPruneWithDedup},
		{name: "PruneNoOpWhenUnderLimit", run: runPruneNoOpWhenUnderLimit},
		{name: "PruneMultiplePasses", run: runPruneMultiplePasses},
		{name: "AbandonOnNonHead", run: runAbandonOnNonHead},
		{name: "AbandonIdempotent", run: runAbandonIdempotent},
		{name: "BranchesDefaultIncludesDead", run: runBranchesDefaultIncludesDead},
	}

	for _, f := range versionedConformanceFactories() {
		f := f
		t.Run(f.name, func(t *testing.T) {
			for _, sc := range scenarios {
				sc := sc
				t.Run(sc.name, func(t *testing.T) {
					sc.run(t, f.make(t))
				})
			}
		})
	}
}

// runPruneByCountLinearNoOp: a linear history of 10 versions with
// MaxVersions=5 must be a no-op. Spec §3 #3: every ancestor of the
// single live head is a retained descendant of the next-older
// candidate, so the bottom-up fixed-point removes every candidate
// from the prune set.
//
// This is the conformance-suite copy of TestPrune_LinearHistory_NoOp;
// lifted here so both backends run the same assertion.
func runPruneByCountLinearNoOp(t *testing.T, vds *VersionedDocumentStore) {
	ctx := context.Background()

	doc := mustCreate(t, vds, "note", `{"v":1}`)
	for i := 2; i <= 10; i++ {
		mustUpdate(t, vds, "note", doc.ID, fmt.Sprintf(`{"v":%d}`, i))
	}

	res, err := vds.Prune(ctx, "note", doc.ID, RetentionPolicy{MaxVersions: 5})
	require.NoError(t, err)
	assert.Empty(t, res.VersionsRemoved,
		"linear-history single-live-head must never prune (spec §3 #3)")

	hist, err := vds.History(ctx, "note", doc.ID)
	require.NoError(t, err)
	assert.Len(t, hist, 10, "history untouched after no-op Prune")
}

// runPruneByCountWithAbandonedForkTail: linear of 10, fork at seq 3,
// extend the fork by 2 Updates (seqs 12, 13), Abandon the fork tail,
// then Prune. The fork subtree {seq 11, 12, 13} all prunes because
// the tail is a dead head whose ancestors are no longer in any live
// retain floor (the main line ends at seq 10 which is the only live
// head). Main line versions {1..10} stay.
//
// Note on policy choice: with this 13-version topology and the fork
// tail at positions 10..12 (seqs 11..13), MaxVersions=5 alone only
// marks seqs 1..8 as candidates — all of them in seq 10's retain
// floor → no-op. To make the abandoned fork subtree observably
// prunable we use MaxAge=1ns so every version is a candidate by
// policy, then rely on the live retain floor (= seq 10's ancestor
// set) to protect the main line. This faithfully exercises the
// spec scenario's intent: an abandoned fork-tail chain prunes
// cleanly while the main line is untouched.
//
// Load-bearing for spec §3 decisions #3, #4, #10: live/dead head
// distinction makes pruning fire exactly on operator-abandoned
// subtrees.
func runPruneByCountWithAbandonedForkTail(t *testing.T, vds *VersionedDocumentStore) {
	ctx := context.Background()

	// Linear seqs 1..10.
	doc := mustCreate(t, vds, "note", `{"v":1}`)
	for i := 2; i <= 10; i++ {
		mustUpdate(t, vds, "note", doc.ID, fmt.Sprintf(`{"v":%d}`, i))
	}
	// Fork at seq 3 → seq 11 (sibling carrying seq-3 data).
	_, err := vds.Fork(ctx, "note", doc.ID, 3)
	require.NoError(t, err)
	// Extend fork: Update parents on the most-recent head (seq 11),
	// producing seqs 12, 13. Fork tail is seq 13.
	mustUpdate(t, vds, "note", doc.ID, `{"v":12,"branch":"fork"}`)
	mustUpdate(t, vds, "note", doc.ID, `{"v":13,"branch":"fork"}`)

	// Heads: {seq 10 (main), seq 13 (fork tail)}.
	heads, err := vds.Branches(ctx, "note", doc.ID)
	require.NoError(t, err)
	require.Len(t, heads, 2)

	// Abandon fork tail.
	require.NoError(t, vds.Abandon(ctx, "note", doc.ID, 13))

	// Sanity: only seq 10 is live.
	live, err := vds.Branches(ctx, "note", doc.ID, WithLiveOnly())
	require.NoError(t, err)
	require.Len(t, live, 1)
	assert.Equal(t, 10, live[0].Seq)

	// Prune with MaxAge=1ns: every version is a candidate by policy;
	// retain_floor = ancestors(seq 10) ∪ {seq 10} keeps the main
	// line; fork subtree (seqs 11, 12, 13) is OUT of retain_floor
	// and IN candidates → prunable bottom-up.
	res, err := vds.Prune(ctx, "note", doc.ID, RetentionPolicy{MaxAge: time.Nanosecond})
	require.NoError(t, err)
	assert.Len(t, res.VersionsRemoved, 3,
		"fork subtree (seqs 11, 12, 13) prunes; main line untouched")

	// History after prune: just main line {1..10}.
	hist, err := vds.History(ctx, "note", doc.ID)
	require.NoError(t, err)
	require.Len(t, hist, 10, "main line of 10 versions retained")
	for i, v := range hist {
		assert.Equal(t, i+1, v.Seq, "history[%d] is seq %d", i, i+1)
	}
}

// runPruneByCountWithMergedTails: linear of 5, fork at seq 2 →
// seq 6 (sibling), Merge(5, 6) → seq 7 (which marks BOTH seq 5 and
// seq 6 as dead per spec §2 + decision #10).
//
// Load-bearing assertion for cross-backend Merge → dead-head
// behavior: BOTH backends must mark seqs 5 and 6 Live=false at
// Merge time via SetLive (spec §2). The History row's Live flag is
// the public-API observable.
//
// Prune with MaxAge=1ns is then a no-op because the merge tip seq 7
// is the only live head and its retain floor covers every ancestor
// — the dead heads seqs 5 and 6 are non-graph-heads now (the merge
// tip is their child) and their ancestor set is fully in seq 7's
// retain floor. Pruning per spec §3 #3 only fires when a candidate
// has NO retained descendant; here every candidate has seq 7 as a
// descendant.
func runPruneByCountWithMergedTails(t *testing.T, vds *VersionedDocumentStore) {
	ctx := context.Background()

	// Linear seqs 1..5.
	doc := mustCreate(t, vds, "note", `{"v":1}`)
	for i := 2; i <= 5; i++ {
		mustUpdate(t, vds, "note", doc.ID, fmt.Sprintf(`{"v":%d}`, i))
	}
	// Fork at seq 2 → seq 6 (sibling head).
	_, err := vds.Fork(ctx, "note", doc.ID, 2)
	require.NoError(t, err)
	// Merge seqs 5 and 6 → seq 7 (merge tip).
	_, err = vds.Merge(ctx, "note", doc.ID, 5, 6, json.RawMessage(`{"merged":true}`))
	require.NoError(t, err)

	// Load-bearing: seqs 5 and 6 are Live=false; seq 7 is Live=true.
	// This is the public-API observable for Merge's automatic
	// SetLive(false) call on consumed heads.
	hist, err := vds.History(ctx, "note", doc.ID)
	require.NoError(t, err)
	require.Len(t, hist, 7)
	for _, v := range hist {
		switch v.Seq {
		case 5, 6:
			assert.False(t, v.Live,
				"Merge consumed seq %d → must be Live=false (spec §2 + #10)", v.Seq)
		default:
			assert.True(t, v.Live,
				"non-consumed seq %d stays Live=true", v.Seq)
		}
	}

	// Branches(WithLiveOnly): only seq 7.
	liveHeads, err := vds.Branches(ctx, "note", doc.ID, WithLiveOnly())
	require.NoError(t, err)
	require.Len(t, liveHeads, 1)
	assert.Equal(t, 7, liveHeads[0].Seq)

	// Prune(MaxAge=1ns): no-op because seq 7's retain floor covers
	// every ancestor (seqs 5, 6 are non-graph-heads now and live in
	// seq 7's ancestor set transitively).
	res, err := vds.Prune(ctx, "note", doc.ID, RetentionPolicy{MaxAge: time.Nanosecond})
	require.NoError(t, err)
	assert.Empty(t, res.VersionsRemoved,
		"merge tip retain floor covers all ancestors → no-op")
}

// runPruneByCountWithRevertOrphan: linear of 5, Revert(seq=2) →
// seq 6 (which marks pre-revert head seq 5 as dead per spec §2 +
// decision #10).
//
// Load-bearing assertion for cross-backend Revert → dead-head
// behavior: BOTH backends must flip seq 5's Live=false at Revert
// time. The History Live flag is the public-API observable.
//
// Prune with MaxAge=1ns is a no-op because the revert tip seq 6 is
// the only live head and its parent (seq 5, dead) plus all
// upstream ancestors are in seq 6's retain floor. The dead-head
// bit on seq 5 is observable but doesn't trigger prune-fire here
// because seq 5 is no longer a graph-topology head (it has child
// seq 6, the revert tip). The prune-fire side effect of a dead
// head requires that head to be outside any live retain floor —
// covered by runPruneByCountWithAbandonedForkTail.
//
// This scenario exists alongside MergedTails to cross-check the
// two automatic-dead-head call sites (Merge + Revert) at the
// public-API surface. Either backend disagreeing surfaces a
// SetLive integration gap.
func runPruneByCountWithRevertOrphan(t *testing.T, vds *VersionedDocumentStore) {
	ctx := context.Background()

	doc := mustCreate(t, vds, "note", `{"v":1}`)
	for i := 2; i <= 5; i++ {
		mustUpdate(t, vds, "note", doc.ID, fmt.Sprintf(`{"v":%d}`, i))
	}
	// Revert to seq 2 → seq 6 (revert tip; parents on seq 5 per
	// Update semantics). Pre-revert head seq 5 is marked dead.
	_, err := vds.Revert(ctx, "note", doc.ID, 2)
	require.NoError(t, err)

	hist, err := vds.History(ctx, "note", doc.ID)
	require.NoError(t, err)
	require.Len(t, hist, 6)
	for _, v := range hist {
		switch v.Seq {
		case 5:
			assert.False(t, v.Live,
				"Revert pre-revert head seq 5 → Live=false (spec §2 + #10)")
		default:
			assert.True(t, v.Live,
				"non-pre-revert seq %d stays Live=true", v.Seq)
		}
	}

	// Branches(WithLiveOnly): only the revert tip.
	liveHeads, err := vds.Branches(ctx, "note", doc.ID, WithLiveOnly())
	require.NoError(t, err)
	require.Len(t, liveHeads, 1)
	assert.Equal(t, 6, liveHeads[0].Seq)

	// Prune(MaxAge=1ns): no-op because seq 6's retain floor covers
	// seq 5 (its parent) and transitively covers seqs 1..4.
	res, err := vds.Prune(ctx, "note", doc.ID, RetentionPolicy{MaxAge: time.Nanosecond})
	require.NoError(t, err)
	assert.Empty(t, res.VersionsRemoved,
		"revert tip retain floor covers all ancestors → no-op")
}

// runPruneByAge: MaxAge=1ms, sleep past the threshold, Abandon a
// fork tail, Prune → only the abandoned fork subtree prunes; the
// main-line ancestors stay (live retain floor).
func runPruneByAge(t *testing.T, vds *VersionedDocumentStore) {
	ctx := context.Background()

	doc := mustCreate(t, vds, "note", `{"v":1}`)
	mustUpdate(t, vds, "note", doc.ID, `{"v":2}`)
	mustUpdate(t, vds, "note", doc.ID, `{"v":3}`)
	// Fork at seq 1 → seq 4 (sibling head).
	_, err := vds.Fork(ctx, "note", doc.ID, 1)
	require.NoError(t, err)
	// Extend fork: seq 5 (fork tail).
	mustUpdate(t, vds, "note", doc.ID, `{"v":5,"branch":"fork"}`)

	// Sleep past the 1ms threshold so seqs 1..5 are all old.
	time.Sleep(5 * time.Millisecond)

	// Abandon fork tail; main-line head seq 3 stays live.
	require.NoError(t, vds.Abandon(ctx, "note", doc.ID, 5))

	res, err := vds.Prune(ctx, "note", doc.ID, RetentionPolicy{MaxAge: time.Millisecond})
	require.NoError(t, err)
	assert.Len(t, res.VersionsRemoved, 2,
		"abandoned fork subtree prunes by age; live ancestors retained")

	hist, err := vds.History(ctx, "note", doc.ID)
	require.NoError(t, err)
	require.Len(t, hist, 3)
	for _, v := range hist {
		assert.Contains(t, []int{1, 2, 3}, v.Seq, "main line retained")
	}
}

// runPruneWithBothLimits: AND-rule per spec §3 #1. With
// MaxVersions=5 + MaxAge=1ms, a version is a candidate iff it
// exceeds BOTH limits.
//
// Topology: linear of 6 + fork tail of 2 = 8 versions; sleep past
// 1ms; Abandon the fork tail. Most-recent-5 by seq are positions
// 3..7 (seqs 4..8). MaxVersions-bound candidates are seqs 1..3.
// AND with MaxAge (all old) → AND-set = {1, 2, 3}, all in seq 6's
// retain floor → no-op.
//
// Cross-check: dropping the count bound, MaxAge alone DOES catch
// the abandoned fork subtree (seqs 7, 8). Confirms the AND really
// did narrow vs. age-alone — operators reading both bounds behave
// as the spec promises ("conservative compose").
func runPruneWithBothLimits(t *testing.T, vds *VersionedDocumentStore) {
	ctx := context.Background()

	// Linear seqs 1..6.
	doc := mustCreate(t, vds, "note", `{"v":1}`)
	for i := 2; i <= 6; i++ {
		mustUpdate(t, vds, "note", doc.ID, fmt.Sprintf(`{"v":%d}`, i))
	}
	// Fork at seq 1 → seq 7 (sibling head).
	_, err := vds.Fork(ctx, "note", doc.ID, 1)
	require.NoError(t, err)
	// Extend fork: seq 8 (fork tail).
	mustUpdate(t, vds, "note", doc.ID, `{"v":8,"branch":"fork"}`)

	time.Sleep(5 * time.Millisecond)
	require.NoError(t, vds.Abandon(ctx, "note", doc.ID, 8))

	// AND-rule: MaxVersions=5 + MaxAge=1ms. Fork tail (seqs 7, 8) is
	// inside most-recent-5, so NOT a candidate by count → no-op.
	res, err := vds.Prune(ctx, "note", doc.ID, RetentionPolicy{
		MaxVersions: 5,
		MaxAge:      time.Millisecond,
	})
	require.NoError(t, err)
	assert.Empty(t, res.VersionsRemoved,
		"AND-rule narrows: fork tail inside most-recent-5 → no candidate fires")

	// Cross-check: drop the count bound; age alone catches the
	// abandoned fork subtree. Confirms the AND really did narrow.
	res, err = vds.Prune(ctx, "note", doc.ID, RetentionPolicy{MaxAge: time.Millisecond})
	require.NoError(t, err)
	assert.Len(t, res.VersionsRemoved, 2,
		"age alone (no count bound) prunes the abandoned fork subtree")
}

// runPruneNeverEmpties: a fresh document with one (live) version
// against any policy is a no-op (spec §3 #2). And Abandon on the
// only live head returns ErrCannotAbandonLastLiveHead.
func runPruneNeverEmpties(t *testing.T, vds *VersionedDocumentStore) {
	ctx := context.Background()

	doc := mustCreate(t, vds, "note", `{"v":1}`)

	// Aggressive policy → no-op (single live head retained).
	res, err := vds.Prune(ctx, "note", doc.ID, RetentionPolicy{
		MaxVersions: 0,
		MaxAge:      time.Nanosecond,
	})
	require.NoError(t, err)
	assert.Empty(t, res.VersionsRemoved, "single-version doc never prunes")

	hist, err := vds.History(ctx, "note", doc.ID)
	require.NoError(t, err)
	assert.Len(t, hist, 1)

	// Abandon on the only live head must fail with the sentinel.
	err = vds.Abandon(ctx, "note", doc.ID, 1)
	assert.True(t, errors.Is(err, ErrCannotAbandonLastLiveHead),
		"Abandon on last live head must return ErrCannotAbandonLastLiveHead, got: %v", err)
}

// runPruneRespectsLiveBranches: doc with two LIVE heads sharing
// some ancestors. Even with an aggressive policy, nothing prunes
// because every version is in some live head's retain floor.
// Verifies spec §3 decision #4 — branched docs prune per-branch
// (union of live-ancestors).
func runPruneRespectsLiveBranches(t *testing.T, vds *VersionedDocumentStore) {
	ctx := context.Background()

	doc := mustCreate(t, vds, "note", `{"v":1}`)
	mustUpdate(t, vds, "note", doc.ID, `{"v":2}`)
	mustUpdate(t, vds, "note", doc.ID, `{"v":3}`) // head A: linear tip
	_, err := vds.Fork(ctx, "note", doc.ID, 2)
	require.NoError(t, err)
	mustUpdate(t, vds, "note", doc.ID, `{"v":5,"branch":"fork"}`) // head B

	live, err := vds.Branches(ctx, "note", doc.ID, WithLiveOnly())
	require.NoError(t, err)
	require.Len(t, live, 2, "both heads live before any Abandon")

	res, err := vds.Prune(ctx, "note", doc.ID, RetentionPolicy{MaxAge: time.Nanosecond})
	require.NoError(t, err)
	assert.Empty(t, res.VersionsRemoved,
		"both live heads retain their ancestor sets → nothing prunable")

	hist, err := vds.History(ctx, "note", doc.ID)
	require.NoError(t, err)
	assert.Len(t, hist, 5, "all 5 versions retained")
}

// runPruneAfterAbandoningOneOfTwoLiveHeads: doc with two live heads
// A and B sharing ancestors. Abandon(B). Prune → ancestors UNIQUE
// to B's chain prune; ancestors SHARED with A retain. Spec §3
// decision #4.
//
// Topology:
//
//	seq 1 → seq 2 → seq 3 (head A, live)
//	         ↓
//	         seq 4 → seq 5 (head B, abandoned)
//
// retain_floor (post-Abandon B) = ancestors(seq 3) ∪ {seq 3} =
// {1, 2, 3}. Outside retain_floor: {seq 4, seq 5}. Both prunable.
func runPruneAfterAbandoningOneOfTwoLiveHeads(t *testing.T, vds *VersionedDocumentStore) {
	ctx := context.Background()

	doc := mustCreate(t, vds, "note", `{"v":1}`)
	mustUpdate(t, vds, "note", doc.ID, `{"v":2}`)
	mustUpdate(t, vds, "note", doc.ID, `{"v":3}`) // head A
	_, err := vds.Fork(ctx, "note", doc.ID, 2)
	require.NoError(t, err)
	mustUpdate(t, vds, "note", doc.ID, `{"v":5,"branch":"fork"}`) // head B

	// Pre-Abandon: nothing prunable.
	preRes, err := vds.Prune(ctx, "note", doc.ID, RetentionPolicy{MaxAge: time.Nanosecond})
	require.NoError(t, err)
	assert.Empty(t, preRes.VersionsRemoved, "pre-Abandon: every version retained")

	// Abandon B (seq 5).
	require.NoError(t, vds.Abandon(ctx, "note", doc.ID, 5))

	res, err := vds.Prune(ctx, "note", doc.ID, RetentionPolicy{MaxAge: time.Nanosecond})
	require.NoError(t, err)
	assert.Len(t, res.VersionsRemoved, 2,
		"seqs 4, 5 (unique to abandoned head B) prune; shared {1,2} retained via head A")

	hist, err := vds.History(ctx, "note", doc.ID)
	require.NoError(t, err)
	require.Len(t, hist, 3)
	for _, v := range hist {
		assert.Contains(t, []int{1, 2, 3}, v.Seq, "shared/main line retained")
	}
}

// runPruneWithDedup: Fork's tip carries the source's bytes byte-
// for-byte (per dedup spec). After Abandon + Prune, the shared
// blob's refcount decrements but the blob survives (still
// referenced by the source version on the live branch); the
// unique-to-fork-tail blob is freed. Verifies spec §3 decision #5.
func runPruneWithDedup(t *testing.T, vds *VersionedDocumentStore) {
	ctx := context.Background()

	doc := mustCreate(t, vds, "note", `{"v":1}`)
	mustUpdate(t, vds, "note", doc.ID, `{"v":2}`)
	mustUpdate(t, vds, "note", doc.ID, `{"v":3}`) // head A
	// Fork at seq 1 → seq 4 carries seq 1's bytes (shared blob).
	_, err := vds.Fork(ctx, "note", doc.ID, 1)
	require.NoError(t, err)
	// Extend fork: seq 5 with UNIQUE bytes (its own blob).
	mustUpdate(t, vds, "note", doc.ID, `{"v":99,"unique":"to-fork-tail"}`)

	require.NoError(t, vds.Abandon(ctx, "note", doc.ID, 5))

	res, err := vds.Prune(ctx, "note", doc.ID, RetentionPolicy{MaxAge: time.Nanosecond})
	require.NoError(t, err)
	assert.Len(t, res.VersionsRemoved, 2, "seqs 4, 5 prune (abandoned fork subtree)")
	assert.Equal(t, 1, res.BlobsFreed,
		"only the unique seq-5 blob is freed; the shared seq-1/seq-4 blob survives at refcount=1")
	assert.Greater(t, res.BytesFreed, int64(0),
		"BytesFreed reflects the freed blob's size")

	// Sanity: seq 1's snapshot still resolves byte-identical (the
	// shared blob survived).
	hist, err := vds.History(ctx, "note", doc.ID)
	require.NoError(t, err)
	require.Len(t, hist, 3)
	for _, v := range hist {
		if v.Seq == 1 {
			assert.JSONEq(t, `{"v":1}`, string(v.Data),
				"shared blob still resolves to original bytes after partial prune")
		}
	}
}

// runPruneNoOpWhenUnderLimit: MaxVersions=100 on a 10-version doc
// → no version exceeds the count bound → empty PruneResult.
func runPruneNoOpWhenUnderLimit(t *testing.T, vds *VersionedDocumentStore) {
	ctx := context.Background()

	doc := mustCreate(t, vds, "note", `{"v":1}`)
	for i := 2; i <= 10; i++ {
		mustUpdate(t, vds, "note", doc.ID, fmt.Sprintf(`{"v":%d}`, i))
	}

	res, err := vds.Prune(ctx, "note", doc.ID, RetentionPolicy{MaxVersions: 100})
	require.NoError(t, err)
	assert.Empty(t, res.VersionsRemoved, "no version exceeds MaxVersions=100 in a 10-version doc")
	assert.Equal(t, 0, res.BlobsFreed)
	assert.Equal(t, int64(0), res.BytesFreed)
}

// runPruneMultiplePasses: two Prune calls in a row with the same
// policy + same Abandon state — the second is a no-op (idempotent
// contract: the first already removed everything prunable).
func runPruneMultiplePasses(t *testing.T, vds *VersionedDocumentStore) {
	ctx := context.Background()

	doc := mustCreate(t, vds, "note", `{"v":1}`)
	for i := 2; i <= 5; i++ {
		mustUpdate(t, vds, "note", doc.ID, fmt.Sprintf(`{"v":%d}`, i))
	}
	_, err := vds.Fork(ctx, "note", doc.ID, 1)
	require.NoError(t, err)
	mustUpdate(t, vds, "note", doc.ID, `{"v":7,"branch":"fork"}`)

	require.NoError(t, vds.Abandon(ctx, "note", doc.ID, 7))

	policy := RetentionPolicy{MaxAge: time.Nanosecond}
	r1, err := vds.Prune(ctx, "note", doc.ID, policy)
	require.NoError(t, err)
	require.Len(t, r1.VersionsRemoved, 2, "first pass prunes the fork subtree")

	r2, err := vds.Prune(ctx, "note", doc.ID, policy)
	require.NoError(t, err)
	assert.Empty(t, r2.VersionsRemoved, "second pass is a no-op")
	assert.Equal(t, 0, r2.BlobsFreed)
	assert.Equal(t, int64(0), r2.BytesFreed)
}

// runAbandonOnNonHead: Abandon on a version with children returns
// ErrNotAHead.
func runAbandonOnNonHead(t *testing.T, vds *VersionedDocumentStore) {
	ctx := context.Background()

	doc := mustCreate(t, vds, "note", `{"v":1}`)
	mustUpdate(t, vds, "note", doc.ID, `{"v":2}`)
	mustUpdate(t, vds, "note", doc.ID, `{"v":3}`)

	// seq 2 has child seq 3 → not a head.
	err := vds.Abandon(ctx, "note", doc.ID, 2)
	assert.True(t, errors.Is(err, ErrNotAHead),
		"Abandon on non-head must return ErrNotAHead, got: %v", err)
}

// runAbandonIdempotent: Abandon twice on the same dead head — the
// second call is a successful no-op. Setup needs ≥2 live heads so
// the at-least-one-live-head invariant doesn't kick in first.
func runAbandonIdempotent(t *testing.T, vds *VersionedDocumentStore) {
	ctx := context.Background()

	doc := mustCreate(t, vds, "note", `{"v":1}`)
	mustUpdate(t, vds, "note", doc.ID, `{"v":2}`)
	_, err := vds.Fork(ctx, "note", doc.ID, 1)
	require.NoError(t, err)
	// heads = {seq 2 (live), seq 3 (live)}.

	require.NoError(t, vds.Abandon(ctx, "note", doc.ID, 3))
	// Second call must be nil — idempotent contract.
	require.NoError(t, vds.Abandon(ctx, "note", doc.ID, 3),
		"Abandon on already-dead head must be a successful no-op")
}

// runBranchesDefaultIncludesDead: after Abandon, default Branches()
// returns BOTH heads (live and dead) — dead head has Live=false.
// Branches(WithLiveOnly()) filters to live heads only. Verifies
// spec §4 default-returns-all backward-compat decision.
func runBranchesDefaultIncludesDead(t *testing.T, vds *VersionedDocumentStore) {
	ctx := context.Background()

	doc := mustCreate(t, vds, "note", `{"v":1}`)
	mustUpdate(t, vds, "note", doc.ID, `{"v":2}`)
	_, err := vds.Fork(ctx, "note", doc.ID, 1)
	require.NoError(t, err)
	// heads = {seq 2 (live), seq 3 (live)}.

	require.NoError(t, vds.Abandon(ctx, "note", doc.ID, 3))

	// Default: both heads (live + dead). Dead head has Live=false.
	all, err := vds.Branches(ctx, "note", doc.ID)
	require.NoError(t, err)
	require.Len(t, all, 2, "default Branches returns all heads (live + dead)")
	var sawLive, sawDead bool
	for _, h := range all {
		if h.Seq == 2 {
			assert.True(t, h.Live, "seq 2 (untouched head) is live")
			sawLive = true
		}
		if h.Seq == 3 {
			assert.False(t, h.Live, "seq 3 (abandoned) is dead")
			sawDead = true
		}
	}
	assert.True(t, sawLive, "live head present in default Branches")
	assert.True(t, sawDead, "dead head present in default Branches")

	// WithLiveOnly: only seq 2.
	live, err := vds.Branches(ctx, "note", doc.ID, WithLiveOnly())
	require.NoError(t, err)
	require.Len(t, live, 1)
	assert.Equal(t, 2, live[0].Seq)
	assert.True(t, live[0].Live, "WithLiveOnly result is Live=true")
}
