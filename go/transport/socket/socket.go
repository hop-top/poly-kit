package socket

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"sync"

	"hop.top/kit/go/transport/cmdsurface"
	"hop.top/kit/go/transport/transportsvc"
)

// SocketMode is the permission the socket file is created with.
// Owner-only: on a Unix domain socket the filesystem permission IS
// the access control, so anything broader would let any local user
// invoke the tool's commands.
const SocketMode os.FileMode = 0o600

// maxLineBytes bounds one request line. A client that sends a longer
// line gets an error rather than the server growing a buffer without
// limit on an unauthenticated local channel.
const maxLineBytes = 1 << 20

// Request is one line of the wire protocol. It is a
// [cmdsurface.Invocation] plus the provenance fields a caller may
// supply, kept as a distinct type so the wire shape can carry
// identity without the transport pretending to have verified it.
type Request struct {
	// Path is the cobra command path from root to leaf.
	Path []string `json:"path"`
	// Args are positional arguments after the path.
	Args []string `json:"args,omitempty"`
	// Flags is the flag set keyed by long name.
	Flags map[string]any `json:"flags,omitempty"`
	// Caller is a claimed principal identifier. It is provenance for
	// the audit trail, NOT a credential: the service grants nothing
	// on its basis, and a future authenticated transport is what
	// would make it trustworthy.
	Caller string `json:"caller,omitempty"`
	// TraceID propagates a trace identifier across surfaces.
	TraceID string `json:"trace_id,omitempty"`
}

// Response is one line of the wire protocol in the reply direction.
// Exactly one of Result and Error is set, and Ok says which, so a
// client branches on one boolean rather than on the presence of a
// key.
type Response struct {
	Ok     bool               `json:"ok"`
	Result *cmdsurface.Result `json:"result,omitempty"`
	Error  *Error             `json:"error,omitempty"`
}

// Error is the wire form of a refused or failed invocation.
type Error struct {
	// Code is a stable symbol a client can branch on:
	// NOT_FOUND, NOT_ENABLED, BLOCKED, INVALID, INTERNAL.
	Code string `json:"code"`
	// Message is the human-readable detail.
	Message string `json:"message"`
}

// Wire error codes.
const (
	// CodeNotFound is a path that resolves to no command.
	CodeNotFound = "NOT_FOUND"
	// CodeNotEnabled is a command that exists but is not exposed on
	// this surface — the distinction an agent needs to tell "no such
	// command" from "not reachable here".
	CodeNotEnabled = "NOT_ENABLED"
	// CodeBlocked is a destructive command the policy refuses on
	// this surface.
	CodeBlocked = "BLOCKED"
	// CodeInvalid is a malformed request line.
	CodeInvalid = "INVALID"
	// CodeInternal is anything else the runner returned.
	CodeInternal = "INTERNAL"
)

// Transport is the [transportsvc.Transport] serving NDJSON over a
// Unix domain socket at Path.
type Transport struct {
	// Path is the socket path. Required.
	Path string

	mu    sync.Mutex
	ln    net.Listener
	conns map[net.Conn]struct{}
	done  bool
}

// New returns a Transport listening at path.
func New(path string) *Transport {
	return &Transport{Path: path, conns: make(map[net.Conn]struct{})}
}

// Bind creates the socket file and starts listening.
//
// A stale socket left by a crashed process is removed first, but only
// when nothing is listening on it: dialing succeeds means a live
// server owns the path, and taking it would silently steal traffic
// from a running process.
func (t *Transport) Bind(context.Context) (string, error) {
	if t.Path == "" {
		return "", errors.New("socket: empty path")
	}
	if err := os.MkdirAll(filepath.Dir(t.Path), 0o700); err != nil {
		return "", fmt.Errorf("create socket directory: %w", err)
	}
	if err := t.clearStale(); err != nil {
		return "", err
	}

	ln, err := net.Listen("unix", t.Path)
	if err != nil {
		return "", err
	}
	// Narrow the socket before announcing readiness: between Listen
	// and Chmod the file exists with the process umask applied, so
	// tightening it after the bind but before Serve is the smallest
	// window available without a umask dance.
	if err := os.Chmod(t.Path, SocketMode); err != nil {
		_ = ln.Close()
		return "", fmt.Errorf("chmod socket: %w", err)
	}

	t.mu.Lock()
	t.ln = ln
	t.mu.Unlock()
	return t.Path, nil
}

// clearStale removes a socket file no process is listening on.
func (t *Transport) clearStale() error {
	info, err := os.Lstat(t.Path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return err
	}
	if info.Mode()&os.ModeSocket == 0 {
		return fmt.Errorf("socket: %s exists and is not a socket", t.Path)
	}
	// A successful dial means a live server owns this path.
	if c, err := net.Dial("unix", t.Path); err == nil {
		_ = c.Close()
		return fmt.Errorf("socket: %s is already in use", t.Path)
	}
	return os.Remove(t.Path)
}

// Serve accepts connections until ctx is canceled or Close is called.
func (t *Transport) Serve(ctx context.Context, inv transportsvc.Invoker) error {
	t.mu.Lock()
	ln := t.ln
	t.mu.Unlock()
	if ln == nil {
		return errors.New("socket: Serve called before Bind")
	}

	// Close on cancellation as well as on Stop: a supervisor that
	// cancels without calling Stop must not leave Accept blocked.
	stop := context.AfterFunc(ctx, func() { _ = t.Close(context.Background()) })
	defer stop()

	var wg sync.WaitGroup
	defer wg.Wait()

	for {
		conn, err := ln.Accept()
		if err != nil {
			if t.stopped() || errors.Is(err, net.ErrClosed) {
				// A closed listener after Stop or cancellation is a
				// clean stop, not a failure.
				return nil
			}
			return err
		}
		t.track(conn)
		wg.Add(1)
		go func() {
			defer wg.Done()
			defer t.untrack(conn)
			t.handle(ctx, conn, inv)
		}()
	}
}

// handle serves one connection: a request line in, a response line
// out, until the peer closes or the connection is torn down.
func (t *Transport) handle(ctx context.Context, conn net.Conn, inv transportsvc.Invoker) {
	defer func() { _ = conn.Close() }()

	sc := bufio.NewScanner(conn)
	sc.Buffer(make([]byte, 0, 64*1024), maxLineBytes)
	enc := json.NewEncoder(conn)

	for sc.Scan() {
		if ctx.Err() != nil {
			return
		}
		line := sc.Bytes()
		if len(line) == 0 {
			continue
		}
		if err := enc.Encode(t.dispatch(ctx, line, inv)); err != nil {
			// The peer is gone or the socket is torn down; there is
			// nowhere left to report this.
			return
		}
	}
}

// dispatch decodes one request line and invokes it, mapping every
// outcome onto a Response.
func (t *Transport) dispatch(ctx context.Context, line []byte, inv transportsvc.Invoker) Response {
	var req Request
	if err := json.Unmarshal(line, &req); err != nil {
		return errResponse(CodeInvalid, err.Error())
	}
	if len(req.Path) == 0 {
		return errResponse(CodeInvalid, "path is required")
	}

	res, err := inv(ctx, cmdsurface.Invocation{
		Path:  req.Path,
		Args:  req.Args,
		Flags: req.Flags,
		Meta: cmdsurface.Meta{
			// Surface is pinned by the seam; setting it here would
			// be overwritten. Caller and TraceID are the caller's
			// claim, carried for audit only.
			Caller:  req.Caller,
			TraceID: req.TraceID,
		},
	})
	if err != nil {
		return errResponse(codeFor(err), err.Error())
	}
	return Response{Ok: true, Result: &res}
}

// codeFor maps a bridge error onto a wire code. The three the bridge
// documents are distinguished; anything else is internal.
func codeFor(err error) string {
	switch {
	case errors.Is(err, cmdsurface.ErrUnknownCommand):
		return CodeNotFound
	case errors.Is(err, cmdsurface.ErrSurfaceNotEnabled):
		return CodeNotEnabled
	case errors.Is(err, cmdsurface.ErrDestructiveBlocked):
		return CodeBlocked
	default:
		return CodeInternal
	}
}

func errResponse(code, msg string) Response {
	return Response{Ok: false, Error: &Error{Code: code, Message: msg}}
}

// Close stops accepting, tears down live connections, and unlinks the
// socket file. It is idempotent.
//
// Unlinking is [net.UnixListener]'s own behavior for a socket it
// created, so closing the listener is what removes the file; there is
// no second unlink here, because a manual [os.Remove] after it would
// either be a no-op or delete a socket a restarted process had
// already put back in its place.
func (t *Transport) Close(context.Context) error {
	t.mu.Lock()
	if t.done {
		t.mu.Unlock()
		return nil
	}
	t.done = true
	ln := t.ln
	conns := make([]net.Conn, 0, len(t.conns))
	for c := range t.conns {
		conns = append(conns, c)
	}
	t.mu.Unlock()

	var err error
	if ln != nil {
		err = ln.Close()
	}
	// Tear down in-flight connections so Serve's wait group drains
	// inside the caller's stop budget rather than after it.
	for _, c := range conns {
		_ = c.Close()
	}
	if errors.Is(err, net.ErrClosed) {
		return nil
	}
	return err
}

func (t *Transport) stopped() bool {
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.done
}

func (t *Transport) track(c net.Conn) {
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.conns == nil {
		t.conns = make(map[net.Conn]struct{})
	}
	t.conns[c] = struct{}{}
}

func (t *Transport) untrack(c net.Conn) {
	t.mu.Lock()
	defer t.mu.Unlock()
	delete(t.conns, c)
}

// Compile-time proof the socket transport satisfies the seam.
var _ transportsvc.Transport = (*Transport)(nil)
