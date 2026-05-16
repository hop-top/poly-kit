// `kit serve` is the reference implementation of the engine wire
// protocol. Routes, request/response shapes, status codes, and the
// error envelope below conform to docs/engine-protocol.md, with
// per-row protocol-of-record decisions captured in
// docs/adr/0018-engine-sdk-protocol-reconciliation.md (audit:
// docs/audits/engine-sdk-drift.md). Wire-shape changes here MUST
// land in lockstep with both SDKs (engine/sdk/ts-kit-engine,
// engine/sdk/py-kit-engine) and the parity test under
// engine/sdk/parity, otherwise cross-SDK parity (T-0390) breaks.

package main

import (
	"context"
	"crypto/ed25519"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	stdsync "sync"
	"time"

	"github.com/spf13/cobra"
	"hop.top/kit/engine/store"
	"hop.top/kit/go/console/cli"
	kitlog "hop.top/kit/go/console/log"
	"hop.top/kit/go/runtime/bus"
	kitsync "hop.top/kit/go/runtime/sync"
	"hop.top/kit/go/storage/secret"
	_ "hop.top/kit/go/storage/secret/memory"
	"hop.top/kit/go/transport/api"
)

var validTypeRe = regexp.MustCompile(`^[a-z][a-z0-9_-]{0,63}$`)

func validType(t string) bool { return validTypeRe.MatchString(t) }

func jsonError(w http.ResponseWriter, status int, msg string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(struct {
		api.APIError
		Error string `json:"error"`
	}{
		APIError: api.APIError{
			Status:  status,
			Code:    errorCode(status, msg),
			Message: msg,
		},
		Error: msg,
	})
}

func errorCode(status int, msg string) string {
	switch status {
	case http.StatusBadRequest:
		switch msg {
		case "invalid json":
			return "invalid_json"
		case "invalid type":
			return "invalid_type"
		default:
			return "bad_request"
		}
	case http.StatusUnauthorized:
		return "unauthorized"
	case http.StatusNotFound:
		return "not_found"
	case http.StatusConflict:
		return "conflict"
	default:
		if status >= 500 {
			return "internal_error"
		}
		return "error"
	}
}

func serveCmd(root *cli.Root) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "serve",
		Short: "Start the document engine HTTP server",
		Long: "Run the kit document-engine HTTP server: schema-validated " +
			"REST routes for document CRUD plus history/branching/pruning, " +
			"WebSocket event stream, peer sync, and Bearer-token auth. " +
			"--port 0 (default) lets the kernel pick a free port; --data " +
			"selects the on-disk SQLite store location.",
		RunE: func(cmd *cobra.Command, _ []string) error {
			port, _ := cmd.Flags().GetInt("port")
			dataDir, _ := cmd.Flags().GetString("data")
			noPeer, _ := cmd.Flags().GetBool("no-peer")
			noSync, _ := cmd.Flags().GetBool("no-sync")
			versionsBackend, _ := cmd.Flags().GetString("versions")

			if err := os.MkdirAll(dataDir, 0o755); err != nil {
				return fmt.Errorf("create data dir: %w", err)
			}

			ds, err := store.NewDocumentStore(filepath.Join(dataDir, "documents.db"))
			if err != nil {
				return err
			}
			defer func() { _ = ds.Close() }()

			var vstore store.VersionStore
			switch versionsBackend {
			case "memory":
				vstore = store.NewInMemoryVersionStore()
			case "", "sqlite":
				vstore, err = store.NewSQLiteVersionStore(ds.DB())
				if err != nil {
					return fmt.Errorf("init sqlite version store: %w", err)
				}
			default:
				return fmt.Errorf("unknown --versions backend %q (want sqlite|memory)", versionsBackend)
			}
			vds := store.NewVersionedDocumentStore(ds, vstore)

			ctx, cancel := context.WithCancel(cmd.Context())
			defer cancel()

			logger := kitlog.New(root.Viper)

			// Document mutation event bus. Sibling tools (sync workers,
			// indexers, observers) subscribe via kit/runtime/bus to
			// receive kit.engine.document.{created,updated,deleted} on
			// every successful HTTP write. See cmd/kit/events.go.
			eventBus := bus.New()
			defer func() {
				closeCtx, closeCancel := context.WithTimeout(context.Background(), 2*time.Second)
				defer closeCancel()
				_ = eventBus.Close(closeCtx)
			}()

			mws := []api.Middleware{
				api.RequestID(),
				api.Logger(logger.Info),
				api.Recovery(func(v any, r *http.Request) {
					logger.Error("panic", "error", v, "path", r.URL.Path)
				}),
				api.ContentType("application/json"),
			}

			// Mint the auth and shutdown tokens via the kit secret
			// store so the reference implementation models the
			// canonical pattern: callers should never mint random
			// bearer tokens directly.
			secrets, err := secret.Open(secret.Config{Backend: "memory", Service: "kit-engine"})
			if err != nil {
				return fmt.Errorf("open secret store: %w", err)
			}
			authToken, err := secret.Mint(ctx, secrets, "auth-token", 16)
			if err != nil {
				return fmt.Errorf("mint auth token: %w", err)
			}
			shutdownToken, err := secret.Mint(ctx, secrets, "shutdown-token", 16)
			if err != nil {
				return fmt.Errorf("mint shutdown token: %w", err)
			}

			mws = append(mws, requireAuth(authToken, shutdownToken))

			router := api.NewRouter(
				api.WithMiddleware(mws...),
				api.WithCapabilities("kit-engine", version),
			)

			registerDocumentRoutes(router, vds, eventBus)
			registerHistoryRoutes(router, vds, vstore)
			registerBranchingRoutes(router, vds, vstore)
			registerPruningRoutes(router, vds)
			if !noSync {
				registerSyncRoutes(router)
			}
			if root.Identity != nil {
				registerIdentityRoutes(router, root)
			}
			if root.Mesh != nil && !noPeer {
				registerPeerRoutes(router, root)
			}

			hub := api.NewHub()
			go hub.Run(ctx)
			router.Handle("GET", "/events", api.WSHandler(hub))
			startedAt := time.Now()
			router.Handle("GET", "/health", func(w http.ResponseWriter, _ *http.Request) {
				_ = json.NewEncoder(w).Encode(map[string]any{
					"status":         "ok",
					"pid":            os.Getpid(),
					"uptime_seconds": int(time.Since(startedAt).Seconds()),
				})
			})
			router.Handle("POST", "/shutdown", func(w http.ResponseWriter, r *http.Request) {
				if r.Header.Get("Authorization") != "Bearer "+shutdownToken {
					jsonError(w, http.StatusUnauthorized, "invalid token")
					return
				}
				w.WriteHeader(http.StatusNoContent)
				if f, ok := w.(http.Flusher); ok {
					f.Flush()
				}
				go cancel()
			})

			ln, err := net.Listen("tcp", ":"+strconv.Itoa(port))
			if err != nil {
				return fmt.Errorf("listen: %w", err)
			}
			addr := ln.Addr().(*net.TCPAddr)
			startupJSON, _ := json.Marshal(map[string]any{
				"port":           addr.Port,
				"pid":            os.Getpid(),
				"token":          authToken,
				"shutdown_token": shutdownToken,
			})
			fmt.Fprintln(os.Stdout, string(startupJSON))

			if root.Mesh != nil && !noPeer {
				go func() {
					if err := root.Mesh.Start(ctx); err != nil {
						logger.Error("mesh start", "error", err)
					}
				}()
			}

			srv := &http.Server{Handler: router}
			errCh := make(chan error, 1)
			go func() { errCh <- srv.Serve(ln) }()

			select {
			case err := <-errCh:
				return err
			case <-ctx.Done():
				shutCtx, shutCancel := context.WithTimeout(context.Background(), 5*time.Second)
				defer shutCancel()
				return srv.Shutdown(shutCtx)
			}
		},
	}

	xdg := os.Getenv("XDG_DATA_HOME")
	if xdg == "" {
		home, _ := os.UserHomeDir()
		xdg = filepath.Join(home, ".local", "share")
	}
	defaultData := filepath.Join(xdg, "kit-engine")

	cmd.Flags().Int("port", 0, "Listen port (0 = auto-assign)")
	cmd.Flags().String("data", defaultData, "Data directory")
	cmd.Flags().String("app", "", "Application namespace")
	cmd.Flags().Bool("daemon", false, "Detach and write PID file")
	cmd.Flags().Bool("no-peer", false, "Disable mDNS peer discovery")
	cmd.Flags().Bool("no-sync", false, "Disable sync replication")
	cmd.Flags().Bool("encrypt", false, "Encrypt data at rest")
	cmd.Flags().String("versions", "sqlite", "Version-history backend (sqlite|memory). sqlite is durable across restarts; memory is ephemeral.")

	cli.SetSideEffect(cmd, cli.SideEffectWrite)
	cli.SetIdempotency(cmd, cli.IdempotencyNo)
	cli.SetTopLevelVerb(cmd)
	return cmd
}

func registerDocumentRoutes(router *api.Router, vds *store.VersionedDocumentStore, eventBus bus.Bus, opts ...EventOption) {
	cfg := newEventConfig(opts...)
	router.Handle("POST", "/{type}/", func(w http.ResponseWriter, r *http.Request) {
		docType := api.PathParam(r, "type")
		if !validType(docType) {
			jsonError(w, http.StatusBadRequest, "invalid type")
			return
		}
		r.Body = http.MaxBytesReader(w, r.Body, 1<<20)
		var data json.RawMessage
		if err := json.NewDecoder(r.Body).Decode(&data); err != nil {
			jsonError(w, http.StatusBadRequest, "invalid json")
			return
		}
		doc, ver, err := vds.CreateAndVersion(r.Context(), docType, data)
		if err != nil {
			jsonError(w, http.StatusInternalServerError, err.Error())
			return
		}
		publishDocEvent(r.Context(), eventBus, cfg.topics.Created, cfg.source, payloadFromDoc(doc, ver))
		w.WriteHeader(http.StatusCreated)
		_ = json.NewEncoder(w).Encode(doc)
	})

	router.Handle("GET", "/{type}/", func(w http.ResponseWriter, r *http.Request) {
		docType := api.PathParam(r, "type")
		if !validType(docType) {
			jsonError(w, http.StatusBadRequest, "invalid type")
			return
		}
		q := store.Query{
			Sort:   r.URL.Query().Get("sort"),
			Search: r.URL.Query().Get("search"),
		}
		if raw := r.URL.Query().Get("limit"); raw != "" {
			n, err := strconv.Atoi(raw)
			if err != nil || n < 0 {
				jsonError(w, http.StatusBadRequest, "invalid limit")
				return
			}
			q.Limit = n
		}
		if raw := r.URL.Query().Get("offset"); raw != "" {
			n, err := strconv.Atoi(raw)
			if err != nil || n < 0 {
				jsonError(w, http.StatusBadRequest, "invalid offset")
				return
			}
			q.Offset = n
		}
		docs, err := vds.List(r.Context(), docType, q)
		if err != nil {
			jsonError(w, http.StatusInternalServerError, err.Error())
			return
		}
		if docs == nil {
			docs = []store.Document{}
		}
		_ = json.NewEncoder(w).Encode(docs)
	})

	router.Handle("GET", "/{type}/{id}", func(w http.ResponseWriter, r *http.Request) {
		docType := api.PathParam(r, "type")
		if !validType(docType) {
			jsonError(w, http.StatusBadRequest, "invalid type")
			return
		}
		doc, err := vds.Get(r.Context(), docType, api.PathParam(r, "id"))
		if err != nil {
			jsonError(w, http.StatusNotFound, "not found")
			return
		}
		_ = json.NewEncoder(w).Encode(doc)
	})

	router.Handle("PUT", "/{type}/{id}", func(w http.ResponseWriter, r *http.Request) {
		docType := api.PathParam(r, "type")
		if !validType(docType) {
			jsonError(w, http.StatusBadRequest, "invalid type")
			return
		}
		r.Body = http.MaxBytesReader(w, r.Body, 1<<20)
		var data json.RawMessage
		if err := json.NewDecoder(r.Body).Decode(&data); err != nil {
			jsonError(w, http.StatusBadRequest, "invalid json")
			return
		}
		doc, ver, err := vds.UpdateAndVersion(r.Context(), docType, api.PathParam(r, "id"), data)
		if err != nil {
			jsonError(w, http.StatusNotFound, "not found")
			return
		}
		publishDocEvent(r.Context(), eventBus, cfg.topics.Updated, cfg.source, payloadFromDoc(doc, ver))
		_ = json.NewEncoder(w).Encode(doc)
	})

	router.Handle("DELETE", "/{type}/{id}", func(w http.ResponseWriter, r *http.Request) {
		docType := api.PathParam(r, "type")
		if !validType(docType) {
			jsonError(w, http.StatusBadRequest, "invalid type")
			return
		}
		id := api.PathParam(r, "id")
		err := vds.Delete(r.Context(), docType, id)
		if err != nil {
			jsonError(w, http.StatusNotFound, "not found")
			return
		}
		publishDocEvent(r.Context(), eventBus, cfg.topics.Deleted, cfg.source, DocumentEventPayload{Type: docType, ID: id})
		w.WriteHeader(http.StatusNoContent)
	})
}

// registerHistoryRoutes wires GET /:type/:id/history and
// POST /:type/:id/revert per docs/engine-protocol.md §"Document
// History" / §"Revert Document". The wire shape uses `version`
// (sequence number) on the boundary; internally [store.Version]
// uses Seq, so handlers map between the two.
//
// The history route also honors `?topology=1` (track
// engine-versioned-branching, spec §5): when present, the response
// includes per-version `parent_ids` plus a top-level `heads` slice
// listing the DAG tips. Default (no query param) behavior is
// unchanged from T-0353 — strict backward compat for linear callers.
// vs is consulted directly for DAG topology since
// [store.VersionedDocumentStore] does not surface parent edges
// today.
func registerHistoryRoutes(router *api.Router, vds *store.VersionedDocumentStore, vs store.VersionStore) {
	router.Handle("GET", "/{type}/{id}/history", func(w http.ResponseWriter, r *http.Request) {
		docType := api.PathParam(r, "type")
		if !validType(docType) {
			jsonError(w, http.StatusBadRequest, "invalid type")
			return
		}
		id := api.PathParam(r, "id")
		versions, err := vds.History(r.Context(), docType, id)
		if err != nil {
			jsonError(w, http.StatusNotFound, "not found")
			return
		}

		if r.URL.Query().Get("topology") == "1" {
			parentIdx, _ := loadParentsIndex(r.Context(), vs, docType, id)
			dag, _ := vs.LoadDAG(r.Context(), docType, id)
			var heads []string
			if dag != nil {
				heads = dag.Heads()
			}
			if heads == nil {
				heads = []string{}
			}
			// Preserve newest-first ordering on the topology variant
			// for parity with the default response.
			topo := make([]map[string]any, 0, len(versions))
			for i := len(versions) - 1; i >= 0; i-- {
				topo = append(topo, branchEntry(versions[i], parentIdx))
			}
			_ = json.NewEncoder(w).Encode(map[string]any{
				"heads":    heads,
				"versions": topo,
			})
			return
		}

		// Default (linear) shape — unchanged from T-0353. Spec calls
		// for newest-first; ListVersions returns ascending by seq, so
		// reverse on the wire.
		out := make([]map[string]any, 0, len(versions))
		for i := len(versions) - 1; i >= 0; i-- {
			v := versions[i]
			operation := "update"
			if v.Seq == 1 {
				operation = "create"
			}
			out = append(out, map[string]any{
				"version":   v.Seq,
				"data":      v.Data,
				"timestamp": v.CreatedAt,
				"operation": operation,
			})
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"versions": out})
	})

	router.Handle("POST", "/{type}/{id}/revert", func(w http.ResponseWriter, r *http.Request) {
		docType := api.PathParam(r, "type")
		if !validType(docType) {
			jsonError(w, http.StatusBadRequest, "invalid type")
			return
		}
		id := api.PathParam(r, "id")

		r.Body = http.MaxBytesReader(w, r.Body, 1<<20)
		var body struct {
			Version int `json:"version"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			jsonError(w, http.StatusBadRequest, "invalid json")
			return
		}
		if body.Version <= 0 {
			jsonError(w, http.StatusBadRequest, "invalid version")
			return
		}

		doc, err := vds.Revert(r.Context(), docType, id, body.Version)
		if err != nil {
			// Per spec: 409 if version does not exist.
			jsonError(w, http.StatusConflict, err.Error())
			return
		}
		_ = json.NewEncoder(w).Encode(doc)
	})
}

func registerSyncRoutes(router *api.Router) {
	type remote struct {
		Name   string `json:"name"`
		URL    string `json:"url"`
		Mode   string `json:"mode"`
		Filter string `json:"filter"`
	}
	var (
		mu      stdsync.Mutex
		remotes = map[string]remote{}
	)

	router.Handle("POST", "/sync/remotes", func(w http.ResponseWriter, r *http.Request) {
		r.Body = http.MaxBytesReader(w, r.Body, 1<<20)
		var body remote
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			jsonError(w, http.StatusBadRequest, "invalid json")
			return
		}
		if body.Name == "" || body.URL == "" {
			jsonError(w, http.StatusBadRequest, "missing remote name or url")
			return
		}
		if body.Mode == "" {
			body.Mode = "both"
		}
		switch body.Mode {
		case "push", "pull", "both":
		default:
			jsonError(w, http.StatusBadRequest, "invalid remote mode")
			return
		}

		mu.Lock()
		defer mu.Unlock()
		if _, ok := remotes[body.Name]; ok {
			jsonError(w, http.StatusConflict, "remote already exists")
			return
		}
		remotes[body.Name] = body
		w.WriteHeader(http.StatusCreated)
		_ = json.NewEncoder(w).Encode(body)
	})

	router.Handle("DELETE", "/sync/remotes/{name}", func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		delete(remotes, api.PathParam(r, "name"))
		mu.Unlock()
		w.WriteHeader(http.StatusNoContent)
	})

	router.Handle("POST", "/sync/push", func(w http.ResponseWriter, r *http.Request) {
		r.Body = http.MaxBytesReader(w, r.Body, 1<<20)
		var diffs []kitsync.Diff
		if err := json.NewDecoder(r.Body).Decode(&diffs); err != nil {
			jsonError(w, http.StatusBadRequest, "invalid json")
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]int{"accepted": len(diffs), "rejected": 0})
	})
	router.Handle("GET", "/sync/pull", func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`[]`))
	})
	router.Handle("GET", "/sync/status", func(w http.ResponseWriter, _ *http.Request) {
		mu.Lock()
		status := make([]map[string]any, 0, len(remotes))
		for _, r := range remotes {
			status = append(status, map[string]any{
				"name":          r.Name,
				"url":           r.URL,
				"mode":          r.Mode,
				"filter":        r.Filter,
				"connected":     false,
				"last_sync":     nil,
				"pending_diffs": 0,
				"last_error":    nil,
				"lag_ms":        0,
			})
		}
		mu.Unlock()
		_ = json.NewEncoder(w).Encode(map[string]any{"remotes": status})
	})
	router.Handle("GET", "/sync/ping", func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	})
}

func registerIdentityRoutes(router *api.Router, root *cli.Root) {
	router.Handle("GET", "/identity", func(w http.ResponseWriter, _ *http.Request) {
		pubPEM, _ := root.Identity.MarshalPublicKey()
		_ = json.NewEncoder(w).Encode(map[string]string{
			"id":          root.Identity.PublicKeyID(),
			"fingerprint": root.Identity.PublicKeyID(),
			"public_key":  string(pubPEM),
		})
	})

	router.Handle("POST", "/identity/verify", func(w http.ResponseWriter, r *http.Request) {
		r.Body = http.MaxBytesReader(w, r.Body, 1<<20)
		var body struct {
			Data      string `json:"data"`
			Signature string `json:"signature"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			jsonError(w, http.StatusBadRequest, "invalid json")
			return
		}
		sig, err := base64.StdEncoding.DecodeString(body.Signature)
		if err != nil {
			_ = json.NewEncoder(w).Encode(map[string]bool{"valid": false})
			return
		}
		valid := ed25519.Verify(root.Identity.PublicKey, []byte(body.Data), sig)
		_ = json.NewEncoder(w).Encode(map[string]bool{"valid": valid})
	})
}

// requireAuth accepts any of the supplied bearer tokens for non-GET/HEAD
// requests. The shutdown route has its own additional check for its
// dedicated token; the middleware just gates the auth header at the
// transport level.
func requireAuth(tokens ...string) api.Middleware {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.Method == "GET" || r.Method == "HEAD" {
				next.ServeHTTP(w, r)
				return
			}
			auth := r.Header.Get("Authorization")
			for _, t := range tokens {
				if t != "" && auth == "Bearer "+t {
					next.ServeHTTP(w, r)
					return
				}
			}
			jsonError(w, http.StatusUnauthorized, "unauthorized")
		})
	}
}

func registerPeerRoutes(router *api.Router, root *cli.Root) {
	router.Handle("GET", "/peers", func(w http.ResponseWriter, _ *http.Request) {
		peers := root.Mesh.Peers()
		_ = json.NewEncoder(w).Encode(peers)
	})

	router.Handle("POST", "/peers/{id}/trust", func(w http.ResponseWriter, r *http.Request) {
		if err := root.PeerTrust.Trust(api.PathParam(r, "id")); err != nil {
			jsonError(w, http.StatusBadRequest, err.Error())
			return
		}
		w.WriteHeader(http.StatusNoContent)
	})

	router.Handle("POST", "/peers/{id}/block", func(w http.ResponseWriter, r *http.Request) {
		if err := root.PeerTrust.Block(api.PathParam(r, "id")); err != nil {
			jsonError(w, http.StatusBadRequest, err.Error())
			return
		}
		w.WriteHeader(http.StatusNoContent)
	})
}
