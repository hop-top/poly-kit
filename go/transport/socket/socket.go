package socket

import (
	"bufio"
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"sync"
	"time"

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

// pipelineDepth is how many request lines a connection may have read
// ahead of the one being served. Reading ahead is what lets the
// transport notice a peer that has gone away while a long command is
// still running; the bound keeps a client that floods requests from
// growing memory without limit.
const pipelineDepth = 16

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
	// Caller is a claimed principal identifier. Without an
	// [Authenticator] on the transport it is provenance for the
	// audit trail, NOT a credential: the service grants nothing on
	// its basis. With one, the authenticator's verdict replaces it.
	Caller string `json:"caller,omitempty"`
	// Tenant is a claimed tenant identifier, carried under the same
	// terms as Caller.
	Tenant string `json:"tenant,omitempty"`
	// RequestID identifies this request in the audit trail. The
	// transport issues one when the caller sends none.
	RequestID string `json:"request_id,omitempty"`
	// TraceID propagates a trace identifier across surfaces.
	TraceID string `json:"trace_id,omitempty"`
	// IdempotencyKey is forwarded to the command's --idempotency-key
	// flag when it registers one.
	IdempotencyKey string `json:"idempotency_key,omitempty"`
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
	// NOT_FOUND, NOT_ENABLED, BLOCKED, DENIED, UNAUTHENTICATED,
	// INVALID, INTERNAL.
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
	// CodeDenied is a command the permission gate refuses for this
	// caller. The message carries the gate's stable reason.
	CodeDenied = "DENIED"
	// CodeUnauthenticated is a request the transport's
	// [Authenticator] refused. It is only ever sent when the
	// transport has one.
	CodeUnauthenticated = "UNAUTHENTICATED"
	// CodeInvalid is a malformed request line.
	CodeInvalid = "INVALID"
	// CodeInternal is anything else the runner returned.
	CodeInternal = "INTERNAL"
)

// Identity is what an [Authenticator] establishes for a request.
type Identity struct {
	// Principal is the verified caller identifier.
	Principal string
	// Tenant is the verified tenant, empty for single-tenant tools.
	Tenant string
}

// Authenticator verifies who is on the other end of a socket
// request. It sees the connection, so an implementation may read
// peer credentials from the kernel, and the request, so it may
// verify a token the caller sent in a flag. A non-nil error refuses
// the request with [CodeUnauthenticated]; the returned Identity
// replaces the request's claimed Caller and Tenant.
//
// The transport ships without one: the socket is owner-only by
// construction, and for the common case the file permission is the
// authentication. An authenticator is for the case where the socket
// is shared deliberately and the tool must know which local caller
// is speaking.
type Authenticator func(ctx context.Context, conn net.Conn, req Request) (Identity, error)

// Transport is the [transportsvc.Transport] serving NDJSON over a
// Unix domain socket at Path.
type Transport struct {
	// Path is the socket path. Required.
	Path string

	// Auth, when set, verifies every request before it is invoked.
	// See [Authenticator].
	Auth Authenticator

	// OnRefused is called for a request the transport refused before
	// reaching the invoker — today, a failed authentication — with
	// the invocation as it would have been dispatched and an error
	// wrapping [cmdsurface.ErrAuthRefused]. The service that owns
	// the transport wires it to [cmdsurface.Bridge.Audit] so the
	// refusal lands in the same audit stream as everything the
	// bridge decides.
	OnRefused func(ctx context.Context, inv cmdsurface.Invocation, err error)

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
//
// Requests are answered strictly in order, but they are READ ahead
// of being served: a reader goroutine keeps consuming lines while a
// command runs, so a peer that hangs up mid-command is noticed at
// once — the read returns, and the connection's context is canceled,
// which cancels the invocation in flight. A transport that only read
// between requests would run a command to completion for a caller
// who is no longer there.
func (t *Transport) handle(ctx context.Context, conn net.Conn, inv transportsvc.Invoker) {
	defer func() { _ = conn.Close() }()

	connCtx, cancel := context.WithCancel(ctx)
	defer cancel()

	lines := make(chan []byte, pipelineDepth)
	go func() {
		defer close(lines)
		// The peer closing, a read error, or the listener tearing the
		// connection down all end the scan; each means no caller is
		// left to answer, so the connection's work is canceled.
		defer cancel()
		sc := bufio.NewScanner(conn)
		sc.Buffer(make([]byte, 0, 64*1024), maxLineBytes)
		for sc.Scan() {
			line := append([]byte(nil), sc.Bytes()...)
			select {
			case lines <- line:
			case <-connCtx.Done():
				return
			}
		}
	}()

	enc := json.NewEncoder(conn)
	for line := range lines {
		if connCtx.Err() != nil {
			return
		}
		if len(line) == 0 {
			continue
		}
		if err := enc.Encode(t.dispatch(connCtx, conn, line, inv)); err != nil {
			// The peer is gone or the socket is torn down; there is
			// nowhere left to report this.
			return
		}
	}
}

// dispatch decodes one request line and invokes it, mapping every
// outcome onto a Response.
func (t *Transport) dispatch(ctx context.Context, conn net.Conn, line []byte, inv transportsvc.Invoker) Response {
	var req Request
	if err := json.Unmarshal(line, &req); err != nil {
		return errResponse(CodeInvalid, err.Error())
	}
	if len(req.Path) == 0 {
		return errResponse(CodeInvalid, "path is required")
	}
	if req.RequestID == "" {
		req.RequestID = newRequestID()
	}

	invocation := cmdsurface.Invocation{
		Path:  req.Path,
		Args:  req.Args,
		Flags: req.Flags,
		Meta: cmdsurface.Meta{
			// Surface is pinned by the seam; setting it here would
			// be overwritten. Without an authenticator, Caller and
			// Tenant are the caller's claim, carried for audit only.
			Caller:         req.Caller,
			Tenant:         req.Tenant,
			RequestID:      req.RequestID,
			TraceID:        req.TraceID,
			IdempotencyKey: req.IdempotencyKey,
			RequestedAt:    time.Now(),
		},
	}

	if t.Auth != nil {
		id, err := t.Auth(ctx, conn, req)
		if err != nil {
			if t.OnRefused != nil {
				t.OnRefused(ctx, invocation,
					fmt.Errorf("%w: %v", cmdsurface.ErrAuthRefused, err))
			}
			return errResponse(CodeUnauthenticated, err.Error())
		}
		// The verified identity replaces the claim. A caller who
		// sent a different name is recorded as who they proved to
		// be, not who they said they were.
		invocation.Meta.Caller = id.Principal
		invocation.Meta.Tenant = id.Tenant
	}

	res, err := inv(ctx, invocation)
	if err != nil {
		return errResponse(codeFor(err), err.Error())
	}
	return Response{Ok: true, Result: &res}
}

// codeFor maps a bridge error onto a wire code. The four the bridge
// documents are distinguished; anything else is internal.
func codeFor(err error) string {
	switch {
	case errors.Is(err, cmdsurface.ErrUnknownCommand):
		return CodeNotFound
	case errors.Is(err, cmdsurface.ErrSurfaceNotEnabled):
		return CodeNotEnabled
	case errors.Is(err, cmdsurface.ErrDestructiveBlocked):
		return CodeBlocked
	case errors.Is(err, cmdsurface.ErrPermissionDenied):
		return CodeDenied
	default:
		return CodeInternal
	}
}

func errResponse(code, msg string) Response {
	return Response{Ok: false, Error: &Error{Code: code, Message: msg}}
}

// newRequestID issues an id for a request that arrived without one,
// so every audit record has a handle even when the caller is a
// one-line shell pipe. Random rather than sequential: ids from two
// server instances must not collide in a shared audit stream.
func newRequestID() string {
	b := make([]byte, 16)
	if _, err := rand.Read(b); err != nil {
		return fmt.Sprintf("%x", time.Now().UnixNano())
	}
	return hex.EncodeToString(b)
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
