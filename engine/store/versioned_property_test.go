package store

import (
	"context"
	"encoding/json"
	"fmt"
	"math/rand"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// versionedPropertySeed is the random seed that drives
// TestVersioned_Property. Fixed (rather than time-derived) so a
// failure reproduces deterministically — paste the iteration index
// into the failure message and you can re-run a single scenario.
//
// Bumping this seed is allowed when adding new operation kinds to the
// op-set; do NOT bump it to make a flake go away. A flake here is a
// cross-backend divergence we want surfaced, not silenced.
const versionedPropertySeed int64 = 0xC0FFEE_2026_05_07

// versionedPropertyIterations is the number of randomized sequences
// the property test runs. Spec §7 calls for "at least 1000".
const versionedPropertyIterations = 1000

// opKind is the universe of operations the property test mixes.
type opKind int

const (
	opCreate opKind = iota
	opUpdate
	opRevert
)

type op struct {
	kind    opKind
	docType string
	docID   string
	data    json.RawMessage
	// For revert ops: the seq to revert to; resolved at run time
	// because seq numbers depend on prior history.
	revertSeq int
}

// touchedKey identifies a (type, id) pair the test has interacted
// with. Used to assert cross-backend equivalence at the end.
type touchedKey struct {
	docType string
	docID   string
}

// TestVersioned_Property feeds the same randomized op sequence into
// the in-memory and SQLite backends, then asserts:
//
//  1. For every (type, id) touched, both backends return histories
//     of identical length.
//  2. Within each history, seq is strictly monotonic starting at 1.
//  3. Snapshot data round-trips byte-identically across backends for
//     every (type, id, seq) — the data the in-memory backend stored
//     equals the data the SQLite backend stored.
//
// A divergence here is signal, not noise: it means one of the two
// backends is doing something the other isn't, and the spec-level
// guarantee that VersionedDocumentStore is backend-agnostic is
// broken. Per the task brief: STOP and report rather than shrink the
// iteration count to make this pass.
func TestVersioned_Property(t *testing.T) {
	rng := rand.New(rand.NewSource(versionedPropertySeed))

	// docTypes is intentionally small so iterations re-touch the same
	// keys often, exercising long histories per document.
	docTypes := []string{"note", "task", "doc"}

	for iter := 0; iter < versionedPropertyIterations; iter++ {
		iter := iter
		seqLen := 5 + rng.Intn(26) // 5..30 ops per iteration
		ops := generateOps(rng, docTypes, seqLen)

		// Fresh stores per iteration so prior iterations don't pollute
		// state (we want each iteration to be a self-contained
		// property check).
		mem := newVersionedStore(t)
		sqlitePath := filepath.Join(t.TempDir(), fmt.Sprintf("prop-%d.db", iter))
		ds, err := NewDocumentStore(sqlitePath)
		require.NoError(t, err)
		t.Cleanup(func() { _ = ds.Close() })
		sqliteVS, err := NewSQLiteVersionStore(ds.DB())
		require.NoError(t, err)
		sqlite := NewVersionedDocumentStore(ds, sqliteVS)

		touched := make(map[touchedKey]struct{})
		ctx := context.Background()

		for opIdx, o := range ops {
			switch o.kind {
			case opCreate:
				// Skip if the doc already exists for this iteration.
				// The op generator deduplicates on Create, but we
				// guard anyway so a regenerated id collision doesn't
				// blow up both backends with non-equivalent errors.
				if _, err := mem.Get(ctx, o.docType, o.docID); err == nil {
					continue
				}
				_, errMem := mem.Create(ctx, o.docType, o.data)
				_, errSQL := sqlite.Create(ctx, o.docType, o.data)
				requireErrorsAgree(t, iter, opIdx, "Create", errMem, errSQL)
				touched[touchedKey{o.docType, o.docID}] = struct{}{}

			case opUpdate:
				_, errMem := mem.Update(ctx, o.docType, o.docID, o.data)
				_, errSQL := sqlite.Update(ctx, o.docType, o.docID, o.data)
				requireErrorsAgree(t, iter, opIdx, "Update", errMem, errSQL)
				if errMem == nil {
					touched[touchedKey{o.docType, o.docID}] = struct{}{}
				}

			case opRevert:
				// Resolve the revert target dynamically. Both backends
				// should observe identical history lengths up to this
				// point (that's part of what the property is checking),
				// so picking the seq from one is fine; we use mem.
				hist, err := mem.History(ctx, o.docType, o.docID)
				if err != nil || len(hist) == 0 {
					// No history yet — Revert would error on both. Skip.
					continue
				}
				seq := 1 + rng.Intn(len(hist))
				_, errMem := mem.Revert(ctx, o.docType, o.docID, seq)
				_, errSQL := sqlite.Revert(ctx, o.docType, o.docID, seq)
				requireErrorsAgree(t, iter, opIdx, "Revert", errMem, errSQL)
				if errMem == nil {
					touched[touchedKey{o.docType, o.docID}] = struct{}{}
				}
			}
		}

		// Property assertions over every touched key.
		for key := range touched {
			memHist, errMem := mem.History(ctx, key.docType, key.docID)
			sqlHist, errSQL := sqlite.History(ctx, key.docType, key.docID)
			requireErrorsAgree(t, iter, -1, "History", errMem, errSQL)
			if errMem != nil {
				continue
			}
			require.Equalf(t, len(memHist), len(sqlHist),
				"iter=%d key=%s/%s: cross-backend history length divergence (mem=%d sqlite=%d)",
				iter, key.docType, key.docID, len(memHist), len(sqlHist),
			)

			// Monotonicity: seq starts at 1 and strictly increases.
			for i, v := range memHist {
				require.Equalf(t, i+1, v.Seq,
					"iter=%d key=%s/%s in-memory: expected seq=%d at index %d, got %d",
					iter, key.docType, key.docID, i+1, i, v.Seq,
				)
			}
			for i, v := range sqlHist {
				require.Equalf(t, i+1, v.Seq,
					"iter=%d key=%s/%s sqlite: expected seq=%d at index %d, got %d",
					iter, key.docType, key.docID, i+1, i, v.Seq,
				)
			}

			// Cross-backend snapshot equivalence: same data at same seq.
			for i := range memHist {
				assert.JSONEqf(t, string(memHist[i].Data), string(sqlHist[i].Data),
					"iter=%d key=%s/%s seq=%d: snapshot data divergence",
					iter, key.docType, key.docID, memHist[i].Seq,
				)
			}
		}
	}
}

// requireErrorsAgree asserts both backends either succeeded together
// or failed together. Mismatched outcomes mean one backend rejected
// an operation the other accepted — a contract divergence we want
// loud, not silent.
func requireErrorsAgree(t *testing.T, iter, opIdx int, label string, errMem, errSQL error) {
	t.Helper()
	memOK := errMem == nil
	sqlOK := errSQL == nil
	if memOK == sqlOK {
		return
	}
	t.Fatalf("iter=%d op=%d %s: cross-backend outcome divergence: mem=%v sqlite=%v",
		iter, opIdx, label, errMem, errSQL,
	)
}

// generateOps produces a sequence of plausible Create/Update/Revert
// ops. The first op for any (type, id) is always a Create so Update
// and Revert have a target; subsequent ops on the same key bias
// toward Update with occasional Reverts.
func generateOps(rng *rand.Rand, docTypes []string, n int) []op {
	ops := make([]op, 0, n)
	// Track keys we've created in this sequence so Update/Revert
	// targets are always realizable on at least one backend.
	created := make([]touchedKey, 0)

	for i := 0; i < n; i++ {
		// First op (or 25% of the time later) is a Create on a fresh id.
		if len(created) == 0 || rng.Intn(4) == 0 {
			docType := docTypes[rng.Intn(len(docTypes))]
			docID := fmt.Sprintf("%s-%d", docType, len(created))
			data := json.RawMessage(fmt.Sprintf(`{"id":%q,"v":1,"step":%d}`, docID, i))
			ops = append(ops, op{kind: opCreate, docType: docType, docID: docID, data: data})
			created = append(created, touchedKey{docType, docID})
			continue
		}

		// Otherwise pick an existing (type, id). Update vs Revert split:
		// 80% Update, 20% Revert. Update is the dominant write path
		// in real usage.
		key := created[rng.Intn(len(created))]
		if rng.Intn(5) == 0 {
			// Revert target seq is resolved at run time (we don't yet
			// know the history length the engine has assembled by
			// op-time). The runner re-rolls it within the actual
			// history bounds.
			ops = append(ops, op{kind: opRevert, docType: key.docType, docID: key.docID})
		} else {
			// Encode iteration index and a random nonce into payload
			// so consecutive Updates produce distinct snapshots —
			// otherwise byte-equivalence checks lose teeth.
			data := json.RawMessage(fmt.Sprintf(
				`{"id":%q,"v":%d,"nonce":%d}`, key.docID, i+2, rng.Intn(1<<30),
			))
			ops = append(ops, op{kind: opUpdate, docType: key.docType, docID: key.docID, data: data})
		}
	}
	return ops
}
