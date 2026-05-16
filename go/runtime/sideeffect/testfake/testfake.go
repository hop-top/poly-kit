// Package testfake provides recording implementations of the
// sideeffect interfaces, optimized for tests.
//
// Every call is appended to a per-impl []Call slice. Helpers
// (AssertCalled, AssertNotCalled, Calls) keep test code free of
// reflection. An optional AllowList rejects unexpected calls
// loudly via t.Fatalf — the inverse of the dryrun impl, which
// tolerates anything.
//
// Canned responses are configured per-call via the Expect/Return
// fluent chain on each impl. Calls beyond a queued expectation
// fall back to the impl's zero-value default (nil error,
// 200 OK with empty JSON body, empty []byte for Output).
//
// Threadsafety: every impl guards its mutable fields with a mutex.
// Test code may call methods from multiple goroutines.
//
// See sideeffect/real for production behavior and sideeffect/dryrun
// for human-readable preview behavior.
package testfake

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"strings"
	"sync"
	"testing"
)

// Call is a single recorded invocation. Method names the interface
// method (e.g. "FS.WriteFile"), and Args carries the call
// arguments in declaration order.
type Call struct {
	Method string
	Args   []any
}

// String renders the call in a stable shape for assertion errors.
func (c Call) String() string {
	if len(c.Args) == 0 {
		return c.Method + "()"
	}
	parts := make([]string, len(c.Args))
	for i, a := range c.Args {
		parts[i] = fmt.Sprintf("%v", a)
	}
	return fmt.Sprintf("%s(%s)", c.Method, strings.Join(parts, ", "))
}

// AllowFn reports whether a Call is allowed. Used by AllowList. nil
// allow-list means "everything is allowed".
type AllowFn func(Call) bool

// FS is the recording sideeffect.FS impl.
type FS struct {
	mu    sync.Mutex
	calls []Call
	t     testing.TB
	allow []AllowFn
}

// NewFS returns a recording FS bound to t. Pass nil t to record
// without t.Fatalf wiring (handy for non-test diagnostics; rare).
func NewFS(t testing.TB) *FS {
	return &FS{t: t}
}

// Allow appends predicates to the allowlist. A call is rejected
// when AT LEAST ONE predicate is registered AND none match.
func (f *FS) Allow(fns ...AllowFn) *FS {
	f.mu.Lock()
	f.allow = append(f.allow, fns...)
	f.mu.Unlock()
	return f
}

// Calls returns a snapshot of recorded calls in invocation order.
func (f *FS) Calls() []Call {
	f.mu.Lock()
	defer f.mu.Unlock()
	out := make([]Call, len(f.calls))
	copy(out, f.calls)
	return out
}

func (f *FS) record(c Call) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if !f.allows(c) {
		if f.t != nil {
			f.t.Helper()
			f.t.Fatalf("testfake/FS: unexpected call %s", c)
		}
	}
	f.calls = append(f.calls, c)
}

// allows must be called with f.mu held.
func (f *FS) allows(c Call) bool {
	if len(f.allow) == 0 {
		return true
	}
	for _, fn := range f.allow {
		if fn(c) {
			return true
		}
	}
	return false
}

// WriteFile records ("FS.WriteFile", path, data, perm) and returns nil.
func (f *FS) WriteFile(path string, data []byte, perm os.FileMode) error {
	f.record(Call{Method: "FS.WriteFile", Args: []any{path, data, perm}})
	return nil
}

// MkdirAll records ("FS.MkdirAll", path, perm) and returns nil.
func (f *FS) MkdirAll(path string, perm os.FileMode) error {
	f.record(Call{Method: "FS.MkdirAll", Args: []any{path, perm}})
	return nil
}

// Rename records ("FS.Rename", old, new) and returns nil.
func (f *FS) Rename(oldpath, newpath string) error {
	f.record(Call{Method: "FS.Rename", Args: []any{oldpath, newpath}})
	return nil
}

// Remove records ("FS.Remove", path) and returns nil.
func (f *FS) Remove(path string) error {
	f.record(Call{Method: "FS.Remove", Args: []any{path}})
	return nil
}

// HTTP is the recording sideeffect.HTTP impl.
type HTTP struct {
	mu       sync.Mutex
	calls    []Call
	t        testing.TB
	allow    []AllowFn
	queue    []*httpExpect
	defaultR *httpExpect
}

type httpExpect struct {
	resp *http.Response
	err  error
}

// NewHTTP returns a recording HTTP bound to t.
func NewHTTP(t testing.TB) *HTTP {
	return &HTTP{t: t}
}

// Allow appends predicates to the allowlist.
func (h *HTTP) Allow(fns ...AllowFn) *HTTP {
	h.mu.Lock()
	h.allow = append(h.allow, fns...)
	h.mu.Unlock()
	return h
}

// Calls returns a snapshot of recorded calls.
func (h *HTTP) Calls() []Call {
	h.mu.Lock()
	defer h.mu.Unlock()
	out := make([]Call, len(h.calls))
	copy(out, h.calls)
	return out
}

// Expect queues a canned response for the next Do call. Subsequent
// calls fall back to the default (200 OK, empty JSON) when the
// queue empties.
func (h *HTTP) Expect(resp *http.Response, err error) *HTTP {
	h.mu.Lock()
	h.queue = append(h.queue, &httpExpect{resp: resp, err: err})
	h.mu.Unlock()
	return h
}

// SetDefault overrides the fallback response used after the queue
// empties. nil resp falls back to the synthesized 200.
func (h *HTTP) SetDefault(resp *http.Response, err error) *HTTP {
	h.mu.Lock()
	h.defaultR = &httpExpect{resp: resp, err: err}
	h.mu.Unlock()
	return h
}

// Do records the call and returns the next queued response (or the
// default).
func (h *HTTP) Do(req *http.Request) (*http.Response, error) {
	method := ""
	url := ""
	if req != nil {
		method = req.Method
		if req.URL != nil {
			url = req.URL.String()
		}
	}
	h.mu.Lock()
	c := Call{Method: "HTTP.Do", Args: []any{method, url}}
	if !h.allowsLocked(c) && h.t != nil {
		h.mu.Unlock()
		h.t.Helper()
		h.t.Fatalf("testfake/HTTP: unexpected call %s", c)
		return nil, nil
	}
	h.calls = append(h.calls, c)
	var exp *httpExpect
	switch {
	case len(h.queue) > 0:
		exp = h.queue[0]
		h.queue = h.queue[1:]
	case h.defaultR != nil:
		exp = h.defaultR
	}
	h.mu.Unlock()
	if exp != nil {
		return exp.resp, exp.err
	}
	return defaultHTTPResponse(req), nil
}

func (h *HTTP) allowsLocked(c Call) bool {
	if len(h.allow) == 0 {
		return true
	}
	for _, fn := range h.allow {
		if fn(c) {
			return true
		}
	}
	return false
}

func defaultHTTPResponse(req *http.Request) *http.Response {
	rec := httptest.NewRecorder()
	rec.Header().Set("Content-Type", "application/json")
	rec.WriteHeader(http.StatusOK)
	rec.Body.WriteString("{}")
	resp := rec.Result()
	resp.Request = req
	return resp
}

// Bus is the recording sideeffect.Bus impl.
type Bus struct {
	mu    sync.Mutex
	calls []Call
	t     testing.TB
	allow []AllowFn
	queue []error
	defE  *error
}

// NewBus returns a recording Bus bound to t.
func NewBus(t testing.TB) *Bus {
	return &Bus{t: t}
}

// Allow appends predicates to the allowlist.
func (b *Bus) Allow(fns ...AllowFn) *Bus {
	b.mu.Lock()
	b.allow = append(b.allow, fns...)
	b.mu.Unlock()
	return b
}

// Calls returns a snapshot of recorded calls.
func (b *Bus) Calls() []Call {
	b.mu.Lock()
	defer b.mu.Unlock()
	out := make([]Call, len(b.calls))
	copy(out, b.calls)
	return out
}

// Expect queues an error to return on the next Publish.
func (b *Bus) Expect(err error) *Bus {
	b.mu.Lock()
	b.queue = append(b.queue, err)
	b.mu.Unlock()
	return b
}

// SetDefault overrides the fallback error returned after the queue
// empties.
func (b *Bus) SetDefault(err error) *Bus {
	b.mu.Lock()
	b.defE = &err
	b.mu.Unlock()
	return b
}

// Publish records ("Bus.Publish", topic, source, payload) and
// returns the next queued error (or nil).
func (b *Bus) Publish(_ context.Context, topic, source string, payload any) error {
	c := Call{Method: "Bus.Publish", Args: []any{topic, source, payload}}
	b.mu.Lock()
	if !b.allowsLocked(c) && b.t != nil {
		b.mu.Unlock()
		b.t.Helper()
		b.t.Fatalf("testfake/Bus: unexpected call %s", c)
		return nil
	}
	b.calls = append(b.calls, c)
	var err error
	switch {
	case len(b.queue) > 0:
		err = b.queue[0]
		b.queue = b.queue[1:]
	case b.defE != nil:
		err = *b.defE
	}
	b.mu.Unlock()
	return err
}

func (b *Bus) allowsLocked(c Call) bool {
	if len(b.allow) == 0 {
		return true
	}
	for _, fn := range b.allow {
		if fn(c) {
			return true
		}
	}
	return false
}

// Exec is the recording sideeffect.Exec impl.
type Exec struct {
	mu       sync.Mutex
	calls    []Call
	t        testing.TB
	allow    []AllowFn
	runQ     []error
	outQ     []*execOutput
	defaultR *execOutput
	defaultE *error
}

type execOutput struct {
	out []byte
	err error
}

// NewExec returns a recording Exec bound to t.
func NewExec(t testing.TB) *Exec {
	return &Exec{t: t}
}

// Allow appends predicates to the allowlist.
func (e *Exec) Allow(fns ...AllowFn) *Exec {
	e.mu.Lock()
	e.allow = append(e.allow, fns...)
	e.mu.Unlock()
	return e
}

// Calls returns a snapshot of recorded calls.
func (e *Exec) Calls() []Call {
	e.mu.Lock()
	defer e.mu.Unlock()
	out := make([]Call, len(e.calls))
	copy(out, e.calls)
	return out
}

// ExpectRun queues an error for the next Run call.
func (e *Exec) ExpectRun(err error) *Exec {
	e.mu.Lock()
	e.runQ = append(e.runQ, err)
	e.mu.Unlock()
	return e
}

// ExpectOutput queues a (bytes, err) pair for the next Output call.
func (e *Exec) ExpectOutput(b []byte, err error) *Exec {
	e.mu.Lock()
	e.outQ = append(e.outQ, &execOutput{out: b, err: err})
	e.mu.Unlock()
	return e
}

// SetDefaultRun sets the default error returned by Run after the queue empties.
func (e *Exec) SetDefaultRun(err error) *Exec {
	e.mu.Lock()
	e.defaultE = &err
	e.mu.Unlock()
	return e
}

// SetDefaultOutput sets the default (bytes, err) for Output after the queue empties.
func (e *Exec) SetDefaultOutput(b []byte, err error) *Exec {
	e.mu.Lock()
	e.defaultR = &execOutput{out: b, err: err}
	e.mu.Unlock()
	return e
}

// Run records the call and returns the next queued error.
func (e *Exec) Run(cmd *exec.Cmd) error {
	c := Call{Method: "Exec.Run", Args: []any{cmdArgs(cmd)}}
	e.mu.Lock()
	if !e.allowsLocked(c) && e.t != nil {
		e.mu.Unlock()
		e.t.Helper()
		e.t.Fatalf("testfake/Exec: unexpected call %s", c)
		return nil
	}
	e.calls = append(e.calls, c)
	var err error
	switch {
	case len(e.runQ) > 0:
		err = e.runQ[0]
		e.runQ = e.runQ[1:]
	case e.defaultE != nil:
		err = *e.defaultE
	}
	e.mu.Unlock()
	return err
}

// Output records the call and returns the next queued (bytes, err).
func (e *Exec) Output(cmd *exec.Cmd) ([]byte, error) {
	c := Call{Method: "Exec.Output", Args: []any{cmdArgs(cmd)}}
	e.mu.Lock()
	if !e.allowsLocked(c) && e.t != nil {
		e.mu.Unlock()
		e.t.Helper()
		e.t.Fatalf("testfake/Exec: unexpected call %s", c)
		return nil, nil
	}
	e.calls = append(e.calls, c)
	var resp *execOutput
	switch {
	case len(e.outQ) > 0:
		resp = e.outQ[0]
		e.outQ = e.outQ[1:]
	case e.defaultR != nil:
		resp = e.defaultR
	}
	e.mu.Unlock()
	if resp != nil {
		return resp.out, resp.err
	}
	return nil, nil
}

func (e *Exec) allowsLocked(c Call) bool {
	if len(e.allow) == 0 {
		return true
	}
	for _, fn := range e.allow {
		if fn(c) {
			return true
		}
	}
	return false
}

func cmdArgs(cmd *exec.Cmd) []string {
	if cmd == nil {
		return nil
	}
	if len(cmd.Args) > 0 {
		out := make([]string, len(cmd.Args))
		copy(out, cmd.Args)
		return out
	}
	return []string{cmd.Path}
}

// AssertCalled fails t when no recorded call satisfies match. The
// match func receives every call in order; the first true short-
// circuits.
func AssertCalled(t testing.TB, calls []Call, match func(Call) bool) {
	t.Helper()
	for _, c := range calls {
		if match(c) {
			return
		}
	}
	t.Fatalf("AssertCalled: no recorded call matched\nrecorded: %v", calls)
}

// AssertNotCalled fails t when any recorded call satisfies match.
func AssertNotCalled(t testing.TB, calls []Call, match func(Call) bool) {
	t.Helper()
	for _, c := range calls {
		if match(c) {
			t.Fatalf("AssertNotCalled: unexpected call %s\nrecorded: %v", c, calls)
		}
	}
}
