package main

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"

	"hop.top/kit/engine/store"
	"hop.top/kit/go/transport/api"
)

// registerBranchingRoutes wires the three branching routes per
// docs/engine-protocol.md §"Branching" (track
// engine-versioned-branching, spec docs/specs/engine-versioned-branching.md
// §5):
//
//	GET  /:type/:id/branches  → list heads of the version DAG (most-recent-first).
//	POST /:type/:id/fork      → divergent branch from a given seq.
//	POST /:type/:id/merge     → caller-chosen merged payload with two parents.
//
// Wire shapes mirror /history and /revert (T-0353): the boundary
// uses `seq` numbers (`from_seq`, `source_seq`, `target_seq`); the
// store layer translates to opaque version_ids for parent edges.
//
// vs is consulted directly (alongside vds) to surface DAG topology
// (parent_ids per version) without enlarging the public surface of
// VersionedDocumentStore.
func registerBranchingRoutes(router *api.Router, vds *store.VersionedDocumentStore, vs store.VersionStore) {
	router.Handle("GET", "/{type}/{id}/branches", func(w http.ResponseWriter, r *http.Request) {
		docType := api.PathParam(r, "type")
		if !validType(docType) {
			jsonError(w, http.StatusBadRequest, "invalid type")
			return
		}
		id := api.PathParam(r, "id")

		// engine-version-pruning §5: ?live=1 filters to live heads
		// only. Default (no query param) returns ALL heads — live and
		// dead — preserving the pre-prune-track wire shape byte-for-
		// byte for backward compat.
		var opts []store.BranchesOption
		if r.URL.Query().Get("live") == "1" {
			opts = append(opts, store.WithLiveOnly())
		}

		heads, err := vds.Branches(r.Context(), docType, id, opts...)
		if err != nil {
			jsonError(w, http.StatusNotFound, "not found")
			return
		}

		parentIdx, _ := loadParentsIndex(r.Context(), vs, docType, id)

		// Branches returns ascending-seq for deterministic iteration
		// (engine/store contract); spec §5 wants most-recent-first on
		// the wire. Reverse here.
		out := make([]map[string]any, 0, len(heads))
		for i := len(heads) - 1; i >= 0; i-- {
			h := heads[i]
			out = append(out, branchEntry(h, parentIdx))
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"heads": out})
	})

	router.Handle("POST", "/{type}/{id}/fork", func(w http.ResponseWriter, r *http.Request) {
		docType := api.PathParam(r, "type")
		if !validType(docType) {
			jsonError(w, http.StatusBadRequest, "invalid type")
			return
		}
		id := api.PathParam(r, "id")

		r.Body = http.MaxBytesReader(w, r.Body, 1<<20)
		var body struct {
			FromSeq int `json:"from_seq"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			jsonError(w, http.StatusBadRequest, "invalid json")
			return
		}
		if body.FromSeq <= 0 {
			jsonError(w, http.StatusBadRequest, "invalid from_seq")
			return
		}

		v, err := vds.Fork(r.Context(), docType, id, body.FromSeq)
		if err != nil {
			writeBranchingError(w, err)
			return
		}
		parentIdx, _ := loadParentsIndex(r.Context(), vs, docType, id)
		w.WriteHeader(http.StatusCreated)
		_ = json.NewEncoder(w).Encode(branchEntry(v, parentIdx))
	})

	router.Handle("POST", "/{type}/{id}/merge", func(w http.ResponseWriter, r *http.Request) {
		docType := api.PathParam(r, "type")
		if !validType(docType) {
			jsonError(w, http.StatusBadRequest, "invalid type")
			return
		}
		id := api.PathParam(r, "id")

		r.Body = http.MaxBytesReader(w, r.Body, 1<<20)
		var body struct {
			SourceSeq int             `json:"source_seq"`
			TargetSeq int             `json:"target_seq"`
			Data      json.RawMessage `json:"data"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			jsonError(w, http.StatusBadRequest, "invalid json")
			return
		}
		if body.SourceSeq <= 0 || body.TargetSeq <= 0 {
			jsonError(w, http.StatusBadRequest, "invalid source_seq/target_seq")
			return
		}
		if len(body.Data) == 0 {
			jsonError(w, http.StatusBadRequest, "missing data")
			return
		}

		v, err := vds.Merge(r.Context(), docType, id, body.SourceSeq, body.TargetSeq, body.Data)
		if err != nil {
			writeBranchingError(w, err)
			return
		}
		parentIdx, _ := loadParentsIndex(r.Context(), vs, docType, id)
		w.WriteHeader(http.StatusCreated)
		_ = json.NewEncoder(w).Encode(branchEntry(v, parentIdx))
	})
}

// branchEntry is the per-version envelope used by /branches, /fork,
// /merge, and the topology variant of /history. Matches spec §5
// byte-for-byte: version_id, seq, parent_ids (always non-nil for
// JSON), timestamp.
//
// engine-version-pruning §5: dead versions (Live=false) carry an
// explicit "live": false key. Live=true versions omit the key
// entirely — that's the default the SDK MarshalJSON convention on
// [store.Version] picked, and it preserves the pre-prune-track wire
// shape byte-for-byte for SDK callers that don't parse the field.
func branchEntry(v store.Version, parents map[string][]string) map[string]any {
	out := map[string]any{
		"version_id": v.VersionID,
		"seq":        v.Seq,
		"parent_ids": parentIdsOrEmpty(parents, v.VersionID),
		"timestamp":  v.CreatedAt,
	}
	if !v.Live {
		out["live"] = false
	}
	return out
}

// parentIdsOrEmpty returns the parent IDs of versionID from idx, or
// an empty slice (not nil) so the JSON renders as `[]` rather than
// `null` — matches the spec example shape and is friendly to SDKs
// that distinguish absent vs empty.
func parentIdsOrEmpty(idx map[string][]string, versionID string) []string {
	if p, ok := idx[versionID]; ok && len(p) > 0 {
		return p
	}
	return []string{}
}

// loadParentsIndex builds a version_id → parent_ids map by walking
// the DAG returned from [store.VersionStore.LoadDAG]. Used to enrich
// /branches, /fork, /merge, and /history?topology=1 responses with
// parent edges that the [store.Version] struct itself does not
// surface.
//
// Returns an empty map (not an error) if the DAG cannot be loaded —
// callers fall back to empty parent_ids slices, which matches the
// "no parents recorded" wire shape and avoids cascading failures
// for the (rare) case where the document has versions but the DAG
// snapshot is unavailable.
func loadParentsIndex(ctx context.Context, vs store.VersionStore, docType, id string) (map[string][]string, error) {
	dag, err := vs.LoadDAG(ctx, docType, id)
	if err != nil || dag == nil {
		return map[string][]string{}, err
	}
	heads := dag.Heads()
	out := make(map[string][]string)
	visited := make(map[string]bool)
	queue := append([]string(nil), heads...)
	for len(queue) > 0 {
		cur := queue[0]
		queue = queue[1:]
		if visited[cur] {
			continue
		}
		visited[cur] = true
		v, ok := dag.Get(cur)
		if !ok {
			continue
		}
		// version.DAG.Get returns parents in insertion order — preserved
		// per engine-versioned-branching §3 decision 3 (Merge order).
		out[cur] = append([]string(nil), v.ParentIDs...)
		queue = append(queue, v.ParentIDs...)
	}
	return out, nil
}

// writeBranchingError maps store errors from Fork/Merge to HTTP
// status codes per spec §5: 404 if the document has no history,
// 409 if a referenced seq is out of range. All other errors fall
// back to 500 so caller bugs surface loudly.
func writeBranchingError(w http.ResponseWriter, err error) {
	msg := err.Error()
	switch {
	case strings.Contains(msg, "no history"):
		jsonError(w, http.StatusNotFound, "not found")
	case strings.Contains(msg, "not found"):
		// Out-of-range seq surfaces as "version N not found"; spec
		// maps that to 409, mirroring /revert's semantics for the
		// same error class.
		jsonError(w, http.StatusConflict, msg)
	default:
		jsonError(w, http.StatusInternalServerError, msg)
	}
}
