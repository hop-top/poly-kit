package main

import (
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"time"

	"hop.top/kit/engine/store"
	"hop.top/kit/go/transport/api"
)

// registerPruningRoutes wires the two pruning routes per
// docs/engine-protocol.md §"Pruning + Liveness" (track
// engine-version-pruning, spec docs/specs/engine-version-pruning.md
// §5):
//
//	POST /:type/:id/prune    → apply RetentionPolicy, drop prunable versions.
//	POST /:type/:id/abandon  → mark a head dead (filtered out by ?live=1).
//
// The third route in spec §5 — GET /:type/:id/branches?live=1 —
// extends the existing branches handler in serve_branches.go rather
// than introducing a parallel route, since the wire shape is
// identical and only the head-set is filtered.
//
// Wire shapes mirror the locked spec §5 byte-for-byte:
//   - /prune body: max_versions, max_age_seconds (both int; either
//     may be omitted/0 meaning "unlimited"; both 0 → 400). seconds
//     not nanoseconds because operators rarely express retention in
//     ns (per spec); the handler converts to time.Duration.
//   - /abandon body: seq (int). Empty 200 response on success;
//     idempotent — abandoning an already-dead head is a no-op success.
func registerPruningRoutes(router *api.Router, vds *store.VersionedDocumentStore) {
	router.Handle("POST", "/{type}/{id}/prune", func(w http.ResponseWriter, r *http.Request) {
		docType := api.PathParam(r, "type")
		if !validType(docType) {
			jsonError(w, http.StatusBadRequest, "invalid type")
			return
		}
		id := api.PathParam(r, "id")

		r.Body = http.MaxBytesReader(w, r.Body, 1<<20)
		var body pruneRequest
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			jsonError(w, http.StatusBadRequest, "invalid json")
			return
		}
		// Spec §5: explicit reject when both bounds are zero. A
		// silent 200 with empty result would be ambiguous against the
		// legitimate no-op case (policy set but nothing exceeds it).
		if body.MaxVersions == 0 && body.MaxAgeSeconds == 0 {
			jsonError(w, http.StatusBadRequest, "policy must set at least one of max_versions, max_age_seconds")
			return
		}

		policy := store.RetentionPolicy{
			MaxVersions: body.MaxVersions,
			MaxAge:      time.Duration(body.MaxAgeSeconds) * time.Second,
		}
		result, err := vds.Prune(r.Context(), docType, id, policy)
		if err != nil {
			writePruningError(w, err)
			return
		}
		// Normalize empty slice to non-nil for stable JSON: the
		// no-op case must wire as `"versions_removed": []`, not
		// `null` (spec §5 example shape).
		removed := result.VersionsRemoved
		if removed == nil {
			removed = []string{}
		}
		_ = json.NewEncoder(w).Encode(pruneResponse{
			VersionsRemoved: removed,
			BlobsFreed:      result.BlobsFreed,
			BytesFreed:      result.BytesFreed,
		})
	})

	router.Handle("POST", "/{type}/{id}/abandon", func(w http.ResponseWriter, r *http.Request) {
		docType := api.PathParam(r, "type")
		if !validType(docType) {
			jsonError(w, http.StatusBadRequest, "invalid type")
			return
		}
		id := api.PathParam(r, "id")

		r.Body = http.MaxBytesReader(w, r.Body, 1<<20)
		var body abandonRequest
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			jsonError(w, http.StatusBadRequest, "invalid json")
			return
		}
		if body.Seq <= 0 {
			jsonError(w, http.StatusBadRequest, "invalid seq")
			return
		}

		if err := vds.Abandon(r.Context(), docType, id, body.Seq); err != nil {
			writeAbandonError(w, err)
			return
		}
		// Spec §5: empty body, 200 status. Idempotent — repeated
		// abandons of the same head all return 200.
		w.WriteHeader(http.StatusOK)
	})
}

// pruneRequest is the wire shape of POST /:type/:id/prune. Both
// fields are optional; zero means "unlimited on this dimension."
// MaxAgeSeconds is in seconds (not nanoseconds) per spec §5 — the
// handler converts to time.Duration for the engine API.
type pruneRequest struct {
	MaxVersions   int   `json:"max_versions"`
	MaxAgeSeconds int64 `json:"max_age_seconds"`
}

// pruneResponse is the wire shape of the /prune 200 response. Field
// names match [store.PruneResult]'s JSON tags (versions_removed,
// blobs_freed, bytes_freed).
type pruneResponse struct {
	VersionsRemoved []string `json:"versions_removed"`
	BlobsFreed      int      `json:"blobs_freed"`
	BytesFreed      int64    `json:"bytes_freed"`
}

// abandonRequest is the wire shape of POST /:type/:id/abandon. Seq
// MUST be a current head of the DAG; the engine returns
// [store.ErrNotAHead] otherwise.
type abandonRequest struct {
	Seq int `json:"seq"`
}

// writePruningError maps engine errors from
// [store.VersionedDocumentStore.Prune] to HTTP status codes per spec
// §5: 404 if the document has no recorded history; otherwise 500
// (the prune algorithm doesn't surface other documented error
// classes today — refcount underflow is corruption-class and
// belongs in a 500).
func writePruningError(w http.ResponseWriter, err error) {
	msg := err.Error()
	if strings.Contains(msg, "no history") {
		jsonError(w, http.StatusNotFound, "not found")
		return
	}
	jsonError(w, http.StatusInternalServerError, msg)
}

// writeAbandonError maps engine errors from
// [store.VersionedDocumentStore.Abandon] to HTTP status codes per
// spec §5:
//
//   - 409 for [store.ErrNotAHead] (target seq has children) and
//     [store.ErrCannotAbandonLastLiveHead] (would empty live-heads).
//   - 404 for "no history" (unknown doc) and "version N not found"
//     (unknown seq for a known doc) — these are fmt-only errors
//     today, so string-match is the available signal. Mirrors the
//     same shape /branches uses for its 404.
//   - 500 fallthrough so caller bugs surface loudly.
func writeAbandonError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, store.ErrNotAHead),
		errors.Is(err, store.ErrCannotAbandonLastLiveHead):
		jsonError(w, http.StatusConflict, err.Error())
	default:
		msg := err.Error()
		switch {
		case strings.Contains(msg, "no history"),
			strings.Contains(msg, "not found"):
			jsonError(w, http.StatusNotFound, "not found")
		default:
			jsonError(w, http.StatusInternalServerError, msg)
		}
	}
}
