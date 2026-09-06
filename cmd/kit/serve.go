// `kit serve` is the reference implementation of the engine wire
// protocol, served as kit's own api service (see engine below).
// Routes, request/response shapes, status codes, and the
// error envelope below conform to docs/engine-protocol.md, with
// per-row protocol-of-record decisions captured in
// docs/adr/0018-engine-sdk-protocol-reconciliation.md (audit:
// docs/audits/engine-sdk-drift.md). Wire-shape changes here MUST
// land in lockstep with both SDKs (engine/sdk/ts-kit-engine,
// engine/sdk/py-kit-engine) and the parity test under
// engine/sdk/parity, otherwise cross-SDK parity breaks.

package main

import (
	"context"
	"crypto/ed25519"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	stdsync "sync"
	"time"

	"github.com/spf13/cobra"
	"hop.top/kit/engine/store"
	"hop.top/kit/go/ai/toolspec"
	"hop.top/kit/go/console/cli"
	kitlog "hop.top/kit/go/console/log"
	"hop.top/kit/go/console/serve"
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

// serveNetOpts resolves the effective peer/sync opt-outs for
// `kit serve`. The kit-global --offline flag is the highest-precedence
// network override: when it tagged the command context (see
// cli.IsOffline) both opt-outs flip on regardless of the individual
// --no-peer/--no-sync values. The override only forces the opt-outs
// ON — an explicitly passed --no-peer/--no-sync is never un-set.
func serveNetOpts(cmd *cobra.Command) (noPeer, noSync bool) {
	noPeer, _ = cmd.Flags().GetBool("no-peer")
	noSync, _ = cmd.Flags().GetBool("no-sync")
	if cli.IsOffline(cmd.Context()) {
		noPeer, noSync = true, true
	}
	return noPeer, noSync
}

// routeRegistrar is the slice of *api.Router the engine's route
// registrars need. The api service builds the router; the engine wraps
// it in a capRouter so every route also lands in the /capabilities set
// the leaf `serve` command used to get from api.WithCapabilities.
type routeRegistrar interface {
	Handle(method, path string, handler http.HandlerFunc)
}

// engine is the document engine behind `kit serve`: the stores it
// opens before the api service starts, the tokens it mints for the
// run, and the bus its document events publish to.
//
// It is the adopter side of kit's own migration from a hand-written
// leaf `serve` command to the api service: the routes are unchanged,
// the server, the listener, the startup line and the shutdown are the
// service's. See docs/adopters/guides/migrate-to-served-commands.md.
type engine struct {
	bus bus.Bus

	dataDir  string
	versions string
	noPeer   bool
	noSync   bool

	ds     *store.DocumentStore
	vstore store.VersionStore
	vds    *store.VersionedDocumentStore

	authToken     string
	shutdownToken string

	// bg bounds the goroutines the routes start (the WebSocket hub, the
	// mesh); close ends them.
	bg       context.Context
	bgCancel context.CancelFunc
}

func newEngine() *engine {
	bg, cancel := context.WithCancel(context.Background())
	return &engine{bus: bus.New(), bg: bg, bgCancel: cancel}
}

// close releases what prepare opened. Idempotent.
func (e *engine) close() {
	e.bgCancel()
	if e.ds != nil {
		_ = e.ds.Close()
		e.ds = nil
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	_ = e.bus.Close(ctx)
}

// mountFlags puts the engine's flags on the kit-owned serve parent,
// beside the api service's own --addr. They predate the hierarchy and
// every SDK sidecar passes them, so they stay where scripts expect
// them; each configures the api service and nothing else.
func (e *engine) mountFlags(root *cli.Root) {
	for _, c := range root.Cmd.Commands() {
		if c.Name() != "serve" {
			continue
		}
		xdgData := os.Getenv("XDG_DATA_HOME")
		if xdgData == "" {
			home, _ := os.UserHomeDir()
			xdgData = filepath.Join(home, ".local", "share")
		}
		c.Flags().Int("port", 0, "Listen port for the api service on loopback (0 = auto-assign); --addr overrides it")
		c.Flags().String("data", filepath.Join(xdgData, "kit-engine"), "Data directory")
		c.Flags().String("app", "", "Application namespace")
		c.Flags().Bool("daemon", false, "Detach and write PID file")
		c.Flags().Bool("no-peer", false, "Disable mDNS peer discovery")
		c.Flags().Bool("no-sync", false, "Disable sync replication")
		c.Flags().Bool("encrypt", false, "Encrypt data at rest")
		c.Flags().String("versions", "sqlite", "Version-history backend (sqlite|memory). sqlite is durable across restarts; memory is ephemeral.")
		c.Long += "\n\nThe api service is the kit document engine: schema-validated REST\n" +
			"routes for document CRUD plus history, branching and pruning, a\n" +
			"WebSocket event stream at /events, peer sync, and Bearer-token auth\n" +
			"on every write. --port, --data, --versions, --no-peer and --no-sync\n" +
			"configure it; the startup JSON line on stdout carries the bound port\n" +
			"and the tokens for this run."
		return
	}
}

// prepare is the root's PrePersistentRunE hook. For a `serve` that
// will start the api service it maps --port onto the service's listen
// address, opens the stores and mints the tokens, so a bad data
// directory is refused before anything binds. Every other command,
// `serve --list`, and a `serve <other service>` pass through untouched.
func (e *engine) prepare(root *cli.Root, cmd *cobra.Command, args []string) error {
	if cmd.Name() != "serve" {
		return nil
	}
	if list, _ := cmd.Flags().GetBool("list"); list {
		return nil
	}
	if len(args) == 1 && args[0] != cli.APIServiceName {
		return nil
	}

	if port := cmd.Flags().Lookup("port"); port != nil && port.Changed {
		if addr := cmd.Flags().Lookup("addr"); addr == nil || !addr.Changed {
			p, _ := cmd.Flags().GetInt("port")
			root.Viper.Set("services.api.addr", net.JoinHostPort("127.0.0.1", strconv.Itoa(p)))
		}
	}

	e.dataDir, _ = cmd.Flags().GetString("data")
	e.versions, _ = cmd.Flags().GetString("versions")
	e.noPeer, e.noSync = serveNetOpts(cmd)

	if err := os.MkdirAll(e.dataDir, 0o755); err != nil {
		return fmt.Errorf("create data dir: %w", err)
	}
	ds, err := store.NewDocumentStore(filepath.Join(e.dataDir, "documents.db"))
	if err != nil {
		return err
	}
	e.ds = ds
	switch e.versions {
	case "memory":
		e.vstore = store.NewInMemoryVersionStore()
	case "", "sqlite":
		e.vstore, err = store.NewSQLiteVersionStore(ds.DB())
		if err != nil {
			return fmt.Errorf("init sqlite version store: %w", err)
		}
	default:
		return fmt.Errorf("unknown --versions backend %q (want sqlite|memory)", e.versions)
	}
	e.vds = store.NewVersionedDocumentStore(ds, e.vstore)

	// Mint the auth and shutdown tokens via the kit secret store so the
	// reference implementation models the canonical pattern: callers
	// should never mint random bearer tokens directly.
	secrets, err := secret.Open(secret.Config{Backend: "memory", Service: "kit-engine"})
	if err != nil {
		return fmt.Errorf("open secret store: %w", err)
	}
	if e.authToken, err = secret.Mint(cmd.Context(), secrets, "auth-token", 16); err != nil {
		return fmt.Errorf("mint auth token: %w", err)
	}
	if e.shutdownToken, err = secret.Mint(cmd.Context(), secrets, "shutdown-token", 16); err != nil {
		return fmt.Errorf("mint shutdown token: %w", err)
	}
	return nil
}

// auth is the api service's AuthFunc, with the engine's rule: reads
// are open, everything else carries one of the run's bearer tokens.
// Setting it is also what lets an operator move the api off loopback
// with --addr; the reads stay open there as they were.
func (e *engine) auth(r *http.Request) (any, error) {
	if r.Method == http.MethodGet || r.Method == http.MethodHead {
		return nil, nil
	}
	got := r.Header.Get("Authorization")
	for _, t := range []string{e.authToken, e.shutdownToken} {
		if t != "" && got == "Bearer "+t {
			return api.Claims{Subject: "kit-engine"}, nil
		}
	}
	return nil, errors.New("unauthorized")
}

// registerRoutes is the api service's Handlers hook. It runs when the
// service starts — after prepare opened the stores — and mounts the
// engine's routes ahead of the command projection, so they keep every
// path they had.
func (e *engine) registerRoutes(router *api.Router, root *cli.Root) {
	{
		cr := &capRouter{Router: router, cs: toolspec.NewCapabilitySet("kit-engine", version)}

		registerDocumentRoutes(cr, e.vds, e.bus)
		registerHistoryRoutes(cr, e.vds, e.vstore)
		registerBranchingRoutes(cr, e.vds, e.vstore)
		registerPruningRoutes(cr, e.vds)
		if !e.noSync {
			registerSyncRoutes(cr)
		}
		if root.Identity != nil {
			registerIdentityRoutes(cr, root)
		}
		if root.Mesh != nil && !e.noPeer {
			registerPeerRoutes(cr, root)
			go func() {
				if err := root.Mesh.Start(e.bg); err != nil {
					kitlog.New(root.Viper).Error("mesh start", "error", err)
				}
			}()
		}

		hub := api.NewHub()
		go hub.Run(e.bg)
		cr.Handle("GET", "/events", api.WSHandler(hub))

		startedAt := time.Now()
		cr.Handle("GET", "/health", func(w http.ResponseWriter, _ *http.Request) {
			_ = json.NewEncoder(w).Encode(map[string]any{
				"status":         "ok",
				"pid":            os.Getpid(),
				"uptime_seconds": int(time.Since(startedAt).Seconds()),
			})
		})
		cr.Handle("POST", "/shutdown", e.shutdown(root))
		cr.Handle("GET", "/capabilities", cr.capabilities)
	}
}

// shutdown stops the api service on request. Stopping the service
// makes its Start return cleanly, which ends the supervisor's run the
// same way a signal would: exit 0, stores closed by main.
func (e *engine) shutdown(root *cli.Root) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer "+e.shutdownToken {
			jsonError(w, http.StatusUnauthorized, "invalid token")
			return
		}
		w.WriteHeader(http.StatusNoContent)
		if f, ok := w.(http.Flusher); ok {
			f.Flush()
		}
		svc, ok := root.ServeRegistry().Lookup(cli.APIServiceName)
		if !ok {
			return
		}
		go func() {
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			_ = svc.Stop(ctx)
		}()
	}
}

// announce prints the startup JSON line the SDK sidecars read — port,
// pid, and the two tokens — when the api service reports ready. The
// bound port is knowable only then, which is why the line rides on
// kit.serve.service.ready_reported rather than being printed by a
// command that no longer owns the listener.
func (e *engine) announce(out io.Writer) {
	e.bus.Subscribe("kit.serve.service.ready_reported", func(_ context.Context, ev bus.Event) error {
		p, ok := ev.Payload.(serve.EventPayload)
		if !ok || p.Service != cli.APIServiceName {
			return nil
		}
		_, portStr, err := net.SplitHostPort(p.Address)
		if err != nil {
			return nil
		}
		port, _ := strconv.Atoi(portStr)
		line, _ := json.Marshal(map[string]any{
			"port":           port,
			"pid":            os.Getpid(),
			"token":          e.authToken,
			"shutdown_token": e.shutdownToken,
		})
		fmt.Fprintln(out, string(line))
		return nil
	})
}

// capRouter records every route the engine registers so GET
// /capabilities can describe them, the way api.WithCapabilities did on
// the leaf command's own router.
type capRouter struct {
	*api.Router
	cs      toolspec.CapabilitySet
	methods map[string][]string
	paths   []string
}

func (c *capRouter) Handle(method, path string, handler http.HandlerFunc) {
	if c.methods == nil {
		c.methods = map[string][]string{}
	}
	if _, seen := c.methods[path]; !seen {
		c.paths = append(c.paths, path)
	}
	c.methods[path] = append(c.methods[path], method)
	c.Router.Handle(method, path, handler)
}

func (c *capRouter) capabilities(w http.ResponseWriter, _ *http.Request) {
	if len(c.cs.Capabilities) == 0 {
		paths := append([]string(nil), c.paths...)
		sort.Strings(paths)
		for _, p := range paths {
			c.cs.Add(toolspec.Capability{
				Name: "endpoint:" + p, Type: "endpoint", Path: p, Methods: c.methods[p],
			})
		}
	}
	body, err := c.cs.JSON()
	if err != nil {
		jsonError(w, http.StatusInternalServerError, "failed to serialize capabilities")
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(body)
}

func registerDocumentRoutes(router routeRegistrar, vds *store.VersionedDocumentStore, eventBus bus.Bus, opts ...EventOption) {
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
		id := api.PathParam(r, "id")
		doc, err := vds.Get(r.Context(), docType, id)
		if err != nil {
			jsonError(w, http.StatusNotFound, "not found")
			return
		}
		// The ETag is the document's current version id, the token a
		// client sends back as If-Match. Absent when the document has
		// no version history, so a client cannot forge a precondition
		// against a version that does not exist.
		if vid, verr := vds.CurrentVersionID(r.Context(), docType, id); verr == nil && vid != "" {
			w.Header().Set("ETag", strconv.Quote(vid))
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
		// If-Match is optional: absent means an unconditional write,
		// preserving last-writer-wins for clients that never opt in.
		// Present means the write applies only to the named version.
		expected, perr := preconditionVersion(r.Header.Get("If-Match"))
		if perr != nil {
			jsonError(w, http.StatusBadRequest, perr.Error())
			return
		}
		doc, ver, err := vds.UpdateAndVersionIfMatch(r.Context(), docType, api.PathParam(r, "id"), data, expected)
		if errors.Is(err, store.ErrPreconditionFailed) {
			jsonError(w, http.StatusPreconditionFailed, "version precondition failed")
			return
		}
		if err != nil {
			jsonError(w, http.StatusNotFound, "not found")
			return
		}
		if ver.VersionID != "" {
			w.Header().Set("ETag", strconv.Quote(ver.VersionID))
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
// unchanged from the original linear response shape — strict
// backward compat for linear callers. vs is consulted directly for
// DAG topology since [store.VersionedDocumentStore] does not surface
// parent edges today.
func registerHistoryRoutes(router routeRegistrar, vds *store.VersionedDocumentStore, vs store.VersionStore) {
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

		// Default (linear) shape — unchanged from the original
		// response. Spec calls for newest-first; ListVersions returns
		// ascending by seq, so reverse on the wire.
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

func registerSyncRoutes(router routeRegistrar) {
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

func registerIdentityRoutes(router routeRegistrar, root *cli.Root) {
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

func registerPeerRoutes(router routeRegistrar, root *cli.Root) {
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
