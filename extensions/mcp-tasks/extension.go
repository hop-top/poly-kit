// Copyright 2026 The Model Context Protocol Authors. All rights reserved.
// Use of this source code is governed by the license
// that can be found in the LICENSE file.

package tasks

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"sync"
	"sync/atomic"
	"time"

	"github.com/modelcontextprotocol/go-sdk/jsonrpc"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// Default lifecycle tuning, used when Options leaves the fields zero.
const (
	DefaultTTL          = 15 * time.Minute
	DefaultPollInterval = 5 * time.Second
)

// markerMetaKey carries the created task from StartTask's marker
// CallToolResult to the receiving middleware installed by Attach. It
// exists in process memory only; the middleware strips it before
// anything reaches the wire.
const markerMetaKey = "mcpext.example/tasks#created"

// Options configures an Extension. The zero value (or nil) is usable:
// in-memory store, default TTL and poll interval, and an empty
// principal for every request.
type Options struct {
	// Store persists task records. Nil means NewMemStore.
	Store Store

	// TTL is the task time-to-live advertised as ttlMs and enforced
	// by the store's pruning. Zero means DefaultTTL; negative means
	// unlimited (ttlMs null on the wire).
	TTL time.Duration

	// PollInterval is the suggested client polling interval
	// advertised as pollIntervalMs. Zero means DefaultPollInterval;
	// negative omits the field.
	PollInterval time.Duration

	// Principal derives the caller identity a task is bound to from
	// the request's HTTP headers. Every tasks/* request is authorized
	// against the creating principal; a mismatch is indistinguishable
	// from an unknown task ID. Nil means every request maps to the
	// empty principal — with no authentication in front of the
	// server, all callers can then poll all tasks, exactly as
	// unauthenticated callers share everything else on such a server.
	Principal func(http.Header) string

	// Logger receives diagnostics from detached task execution.
	// Nil means slog.Default.
	Logger *slog.Logger
}

// Extension implements the server side of the MCP tasks extension
// (SEP-2663) for go-sdk servers. See the package documentation for
// the wiring: DeclareServerCapability, Attach, and StartTask
// from inside a tool handler.
type Extension struct {
	store     Store
	ttlMs     int64 // 0 = unlimited
	pollMs    int64 // 0 = omitted
	principal func(http.Header) string
	logger    *slog.Logger

	// attached reports whether Attach installed the middleware that
	// converts StartTask's marker into the wire CreateTaskResult.
	// StartTask refuses to run without it, so the marker can never
	// reach a client.
	attached atomic.Bool

	mu   sync.Mutex
	live map[string]*Handle
}

// New builds an Extension from opts (which may be nil).
func New(opts *Options) *Extension {
	if opts == nil {
		opts = &Options{}
	}
	e := &Extension{
		store:     opts.Store,
		principal: opts.Principal,
		logger:    opts.Logger,
		live:      make(map[string]*Handle),
	}
	if e.store == nil {
		e.store = NewMemStore()
	}
	if e.logger == nil {
		e.logger = slog.Default()
	}
	switch {
	case opts.TTL > 0:
		e.ttlMs = opts.TTL.Milliseconds()
	case opts.TTL == 0:
		e.ttlMs = DefaultTTL.Milliseconds()
	}
	switch {
	case opts.PollInterval > 0:
		e.pollMs = opts.PollInterval.Milliseconds()
	case opts.PollInterval == 0:
		e.pollMs = DefaultPollInterval.Milliseconds()
	}
	return e
}

// DeclareServerCapability declares the tasks extension under
// capabilities.extensions on so, creating the Capabilities struct
// when absent (preserving the SDK's default logging capability, which
// a custom Capabilities value would otherwise drop). Call it before
// mcp.NewServer; the SDK then advertises the extension on both the
// legacy initialize handshake and server/discover.
func DeclareServerCapability(so *mcp.ServerOptions) {
	if so.Capabilities == nil {
		so.Capabilities = &mcp.ServerCapabilities{Logging: &mcp.LoggingCapabilities{}}
	}
	so.Capabilities.AddExtension(ExtensionID, map[string]any{})
}

// ClientDeclares reports whether the calling client declared the
// tasks extension for this request. Per SEP-2663 the declaration is
// per request (the "io.modelcontextprotocol/clientCapabilities" _meta
// field), and the extension is only defined from protocol version
// 2026-06-30 on: clients on older protocol versions are treated as
// non-declaring even when they send the capability. A server must
// never return a CreateTaskResult to a non-declaring client.
func ClientDeclares(req *mcp.CallToolRequest) bool {
	if req == nil {
		return false
	}
	if req.ProtocolVersion() < MinProtocolVersion {
		return false
	}
	caps := req.ClientCapabilities()
	if caps == nil {
		return false
	}
	_, ok := caps.Extensions[ExtensionID]
	return ok
}

// Attach wires the extension into s. It registers tasks/get,
// tasks/update and tasks/cancel as custom methods on the server, so
// the SDK's own transport dispatches them and they inherit every check
// it applies to a standard method; and it installs the receiving
// middleware that converts the marker result StartTask returns into
// the wire CreateTaskResult and carries each request's HTTP headers
// (the principal's source) through to the tasks/* handlers.
//
// Attach is a prerequisite for task creation: StartTask fails until it
// has been called, so a host that wires the extension incompletely
// gets a clear error instead of a spec-violating result on the wire.
// It reports an error only if a tasks/* method could not be registered
// — attaching the same extension to two servers, for instance.
func (e *Extension) Attach(s *mcp.Server) error {
	s.AddReceivingMiddleware(func(next mcp.MethodHandler) mcp.MethodHandler {
		return func(ctx context.Context, method string, req mcp.Request) (mcp.Result, error) {
			if extra := req.GetExtra(); extra != nil && extra.Header != nil {
				ctx = context.WithValue(ctx, headerContextKey{}, extra.Header)
			}
			res, err := next(ctx, method, req)
			if err != nil || method != "tools/call" {
				return res, err
			}
			ctr, ok := res.(*mcp.CallToolResult)
			if !ok || ctr == nil {
				return res, err
			}
			seed, ok := ctr.Meta[markerMetaKey].(*Task)
			if !ok {
				return res, err
			}
			return &createTaskResult{ResultType: "task", Task: *seed}, nil
		}
	})
	if err := e.registerMethods(s); err != nil {
		return err
	}
	e.attached.Store(true)
	return nil
}

// ExecuteFunc runs the long-lived operation behind a task. It
// receives a context detached from the originating request (cancelled
// only by tasks/cancel) and the task's Handle for input requests and
// status messages. Its outcome maps onto the task exactly as SEP-2663
// separates faults: a non-nil error is a protocol-level fault and
// moves the task to failed with the JSON-RPC error (a returned
// *jsonrpc.Error is preserved verbatim; anything else becomes
// -32603); a returned result — including one with IsError set —
// completes the task with that result.
type ExecuteFunc func(ctx context.Context, h *Handle) (*mcp.CallToolResult, error)

// StartTask creates a task for the current tools/call and hands run
// to detached execution. It durably persists the task before
// returning, per the spec's durable-before-respond requirement, and
// binds it to the principal derived from the request headers.
//
// The returned CallToolResult is a marker: return it from the tool
// handler unchanged, and the middleware installed by Attach replaces
// it with the wire CreateTaskResult. Attach is therefore a
// prerequisite — StartTask returns an error, creating nothing, if the
// extension was never attached. Callers must have verified
// ClientDeclares first — returning a CreateTaskResult to a
// non-declaring client violates the spec — and must resolve any
// multi-round-trip exchanges (SEP-2322) synchronously before calling
// StartTask, since MRTR composition requires all MRTR exchanges to
// finish before the CreateTaskResult response.
func (e *Extension) StartTask(ctx context.Context, req *mcp.CallToolRequest, run ExecuteFunc) (*mcp.CallToolResult, error) {
	if req == nil {
		return nil, errors.New("tasks: StartTask: nil request")
	}
	if run == nil {
		return nil, errors.New("tasks: StartTask: nil ExecuteFunc")
	}
	// Fail closed on incomplete wiring. Without the middleware the
	// marker below would marshal onto the wire as a resultType
	// "complete" result carrying the internal key and a live taskId —
	// a spec violation, and a detached task the client was never told
	// about. Refusing here means neither exists.
	if !e.attached.Load() {
		return nil, errors.New("tasks: StartTask: extension not attached to a server; call Extension.Attach before creating tasks")
	}
	var hdr http.Header
	if req.Extra != nil {
		hdr = req.Extra.Header
	}
	now := time.Now().UTC()
	rec := &Record{
		Task: Task{
			TaskID:         NewTaskID(),
			Status:         StatusWorking,
			CreatedAt:      now,
			LastUpdatedAt:  now,
			PollIntervalMs: e.pollMs,
		},
		Principal: e.principalOf(hdr),
	}
	if e.ttlMs > 0 {
		ttl := e.ttlMs
		rec.TTLMs = &ttl
	}
	if err := e.store.Create(ctx, rec); err != nil {
		return nil, fmt.Errorf("tasks: creating task: %w", err)
	}

	runCtx, cancel := context.WithCancel(context.WithoutCancel(ctx))
	h := &Handle{ext: e, id: rec.TaskID, cancel: cancel, used: make(map[string]bool)}
	e.mu.Lock()
	e.live[rec.TaskID] = h
	e.mu.Unlock()
	go e.execute(runCtx, h, run)

	seed := rec.Task
	return &mcp.CallToolResult{Meta: mcp.Meta{markerMetaKey: &seed}}, nil
}

// execute runs the ExecuteFunc and records its terminal outcome.
func (e *Extension) execute(ctx context.Context, h *Handle, run ExecuteFunc) {
	defer func() {
		e.mu.Lock()
		delete(e.live, h.id)
		e.mu.Unlock()
	}()
	res, err := run(ctx, h)
	wctx := context.WithoutCancel(ctx)
	_, mErr := e.store.Mutate(wctx, h.id, func(rec *Record) error {
		if rec.Status.terminal() {
			return nil // work finished after a terminal transition; keep it
		}
		rec.LastUpdatedAt = time.Now().UTC()
		rec.InputRequests = nil
		switch {
		case err != nil && h.cancelRequested.Load() && errors.Is(err, context.Canceled):
			rec.Status = StatusCancelled
			rec.StatusMessage = "cancelled at client request"
		case err != nil:
			rec.Status = StatusFailed
			rec.StatusMessage = err.Error()
			rec.Error = jsonrpcErrorJSON(err)
		case res == nil:
			rec.Status = StatusFailed
			rec.StatusMessage = "executor returned no result"
			rec.Error = jsonrpcErrorJSON(errors.New("executor returned no result"))
		default:
			b, jErr := json.Marshal(res)
			if jErr != nil {
				rec.Status = StatusFailed
				rec.StatusMessage = "marshaling result: " + jErr.Error()
				rec.Error = jsonrpcErrorJSON(jErr)
				return nil
			}
			rec.Status = StatusCompleted
			rec.Result = b
		}
		return nil
	})
	if mErr != nil {
		e.logger.Warn("tasks: recording task outcome failed", "taskId", h.id, "err", mErr)
	}
}

// cancelTask delivers the cooperative cancellation signal. Without a
// live handle (work already finished, or held by another instance of
// a shared store) it is a no-op: the SEP requires only the
// acknowledgement, never that the work stops.
func (e *Extension) cancelTask(id string) {
	e.mu.Lock()
	h := e.live[id]
	e.mu.Unlock()
	if h != nil {
		h.cancelRequested.Store(true)
		h.cancel()
	}
}

// deliver routes tasks/update inputResponses to the live handle.
// Responses for keys that are unknown, already answered, or
// superseded are ignored, as are responses for tasks with no live
// handle — the SEP makes the acknowledgement independent of effect.
func (e *Extension) deliver(ctx context.Context, id string, responses mcp.InputResponseMap) {
	e.mu.Lock()
	h := e.live[id]
	e.mu.Unlock()
	if h == nil {
		return
	}

	h.mu.Lock()
	accepted := 0
	for k, v := range responses {
		if h.outstanding[k] {
			h.responses[k] = v
			delete(h.outstanding, k)
			accepted++
		}
	}
	var done chan struct{}
	if accepted > 0 && h.doneCh != nil && len(h.outstanding) == 0 {
		done = h.doneCh
		h.doneCh = nil
	}
	h.mu.Unlock()

	if done != nil {
		close(done)
		return // RequestInput flips the record back to working
	}
	if accepted == 0 {
		return
	}
	// Partial set accepted: shrink the visible outstanding requests.
	if _, err := e.store.Mutate(ctx, id, func(rec *Record) error {
		if rec.Status != StatusInputRequired {
			return nil
		}
		remaining := make(mcp.InputRequestMap, len(rec.InputRequests))
		for k, v := range rec.InputRequests {
			if h.isOutstanding(k) {
				remaining[k] = v
			}
		}
		rec.InputRequests = remaining
		rec.LastUpdatedAt = time.Now().UTC()
		return nil
	}); err != nil {
		e.logger.Warn("tasks: recording partial input responses failed", "taskId", id, "err", err)
	}
}

// principalOf applies the configured principal function.
func (e *Extension) principalOf(hdr http.Header) string {
	if e.principal == nil {
		return ""
	}
	return e.principal(hdr)
}

// Handle is the executor's view of its task.
type Handle struct {
	ext             *Extension
	id              string
	cancel          context.CancelFunc
	cancelRequested atomic.Bool

	mu          sync.Mutex
	used        map[string]bool // keys used over the task lifetime
	outstanding map[string]bool // keys of the current generation still unanswered
	responses   mcp.InputResponseMap
	doneCh      chan struct{}
}

// TaskID returns the task's identifier.
func (h *Handle) TaskID() string { return h.id }

// isOutstanding reports whether key is still awaiting a response.
func (h *Handle) isOutstanding(key string) bool {
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.outstanding[key]
}

// SetStatusMessage updates the task's human-readable statusMessage.
// It is a no-op once the task is terminal.
func (h *Handle) SetStatusMessage(ctx context.Context, msg string) error {
	_, err := h.ext.store.Mutate(ctx, h.id, func(rec *Record) error {
		if rec.Status.terminal() {
			return nil
		}
		rec.StatusMessage = msg
		rec.LastUpdatedAt = time.Now().UTC()
		return nil
	})
	return err
}

// RequestInput moves the task to input_required with the given
// requests and blocks until the client has answered every key via
// tasks/update (partial updates accumulate) or ctx is cancelled.
// Keys must be unique over the task's lifetime — the SEP forbids
// reuse — and RequestInput enforces that by failing on a reused key.
// On success the task returns to working and the collected responses
// are returned keyed exactly like the requests.
func (h *Handle) RequestInput(ctx context.Context, requests mcp.InputRequestMap) (mcp.InputResponseMap, error) {
	if len(requests) == 0 {
		return nil, errors.New("tasks: RequestInput requires at least one request")
	}
	h.mu.Lock()
	for k := range requests {
		if h.used[k] {
			h.mu.Unlock()
			return nil, fmt.Errorf("tasks: inputRequests key %q reused within task lifetime", k)
		}
	}
	outstanding := make(map[string]bool, len(requests))
	reqCopy := make(mcp.InputRequestMap, len(requests))
	for k, v := range requests {
		h.used[k] = true
		outstanding[k] = true
		reqCopy[k] = v
	}
	h.outstanding = outstanding
	h.responses = make(mcp.InputResponseMap, len(requests))
	done := make(chan struct{})
	h.doneCh = done
	h.mu.Unlock()

	if _, err := h.ext.store.Mutate(ctx, h.id, func(rec *Record) error {
		if rec.Status.terminal() {
			return fmt.Errorf("tasks: task %s already terminal", h.id)
		}
		rec.Status = StatusInputRequired
		rec.InputRequests = reqCopy
		rec.LastUpdatedAt = time.Now().UTC()
		return nil
	}); err != nil {
		return nil, err
	}

	select {
	case <-ctx.Done():
		return nil, ctx.Err()
	case <-done:
	}

	h.mu.Lock()
	responses := h.responses
	h.responses = nil
	h.outstanding = nil
	h.mu.Unlock()

	if _, err := h.ext.store.Mutate(ctx, h.id, func(rec *Record) error {
		if rec.Status.terminal() {
			return nil
		}
		rec.Status = StatusWorking
		rec.InputRequests = nil
		rec.LastUpdatedAt = time.Now().UTC()
		return nil
	}); err != nil {
		return nil, err
	}
	return responses, nil
}

// jsonrpcErrorJSON renders err as a JSON-RPC error object: a
// *jsonrpc.Error verbatim, anything else as -32603 Internal error.
func jsonrpcErrorJSON(err error) json.RawMessage {
	var je *jsonrpc.Error
	if !errors.As(err, &je) {
		je = &jsonrpc.Error{Code: jsonrpc.CodeInternalError, Message: err.Error()}
	}
	b, mErr := json.Marshal(je)
	if mErr != nil {
		return json.RawMessage(`{"code":-32603,"message":"internal error"}`)
	}
	return b
}
