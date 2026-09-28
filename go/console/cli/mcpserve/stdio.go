package mcpserve

import (
	"context"
	"errors"
	"io"
	"os"
	"strconv"
	"sync"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"hop.top/kit/go/transport/cmdsurface"
	"hop.top/kit/go/transport/mcpsdk"
)

// stdioStreams overrides the streams the stdio transport speaks on.
// nil means the process's own standard input and output; tests set it
// to a pipe pair.
type stdioStreams struct {
	in  io.Reader
	out io.Writer
}

// stdioDrain bounds how long a stop waits for the session to wind
// down after it is closed. Closing the session closes its input, but a
// read blocked on a terminal is not always interruptible; the process
// is exiting, so the reader is abandoned rather than holding the stop.
const stdioDrain = 2 * time.Second

// stdioServing serves the surface on standard input and output.
//
// Standard output IS the protocol here, so for as long as the service
// serves, every other writer in the process is pointed away from it:
// the transport keeps the original streams, os.Stdout resolves to
// standard error, and os.Stdin to an empty reader. A stray print in a
// command, a hook, or a library then lands in the operator's log
// rather than in the middle of a frame, and nothing but the SDK
// consumes a request byte.
type stdioServing struct {
	streams *stdioStreams

	mu      sync.Mutex
	in      io.ReadCloser
	out     io.WriteCloser
	restore func()
	ss      *mcp.ServerSession
}

func newStdio(streams *stdioStreams) *stdioServing { return &stdioServing{streams: streams} }

// bind acquires the streams and redirects the process's own. There is
// no address: the peer is whoever spawned the process.
func (s *stdioServing) bind(context.Context) (string, error) {
	var in io.Reader = os.Stdin
	var out io.Writer = os.Stdout
	if st := s.streams; st != nil {
		if st.in != nil {
			in = st.in
		}
		if st.out != nil {
			out = st.out
		}
	}

	empty, err := os.Open(os.DevNull)
	if err != nil {
		return "", err
	}
	origIn, origOut := os.Stdin, os.Stdout
	os.Stdout = os.Stderr
	os.Stdin = empty

	s.mu.Lock()
	s.in = readCloser(in)
	s.out = nopWriteCloser{out}
	s.restore = sync.OnceFunc(func() {
		os.Stdin, os.Stdout = origIn, origOut
		_ = empty.Close()
	})
	s.mu.Unlock()
	return "", nil
}

// serve runs one session until the peer ends it or ctx is canceled.
// The peer closing its end — end of input — is how a host ends a
// stdio server, so it is a clean stop.
func (s *stdioServing) serve(ctx context.Context, surf *mcpsdk.Surface) error {
	s.mu.Lock()
	in, out := s.in, s.out
	s.mu.Unlock()
	if in == nil {
		return errors.New("mcp: serve called before bind")
	}

	ss, err := surf.Server().Connect(ctx, &mcp.IOTransport{Reader: in, Writer: out}, nil)
	if err != nil {
		return err
	}
	s.mu.Lock()
	s.ss = ss
	s.mu.Unlock()

	done := make(chan error, 1)
	go func() { done <- ss.Wait() }()
	select {
	case err := <-done:
		return sessionEnd(err)
	case <-ctx.Done():
		_ = ss.Close()
		select {
		case <-done:
		case <-time.After(stdioDrain):
		}
		return nil
	}
}

// sessionEnd maps how a session ended onto the service's result: the
// peer closing its input, or the session being closed, is a clean
// stop; anything else is a failure.
func sessionEnd(err error) error {
	switch {
	case err == nil,
		errors.Is(err, io.EOF),
		errors.Is(err, io.ErrClosedPipe),
		errors.Is(err, os.ErrClosed),
		errors.Is(err, context.Canceled),
		errors.Is(err, mcp.ErrConnectionClosed):
		return nil
	}
	return err
}

// close ends the session and gives the process its streams back.
func (s *stdioServing) close(context.Context) error {
	s.mu.Lock()
	ss, restore := s.ss, s.restore
	s.mu.Unlock()
	if ss != nil {
		_ = ss.Close()
	}
	if restore != nil {
		restore()
	}
	return nil
}

// callMeta is the provenance of one tool call over stdio: the
// transport and the peer's process id. No principal is invented.
func (s *stdioServing) callMeta(_ context.Context, req *mcp.CallToolRequest) cmdsurface.Meta {
	extra := map[string]string{
		"mcp_transport": TransportStdio,
		"peer_pid":      strconv.Itoa(os.Getppid()),
	}
	if name := clientName(req); name != "" {
		extra["mcp_client"] = name
	}
	return cmdsurface.Meta{
		RequestID:   newRequestID(),
		RequestedAt: time.Now(),
		Extra:       extra,
	}
}

// authenticated admits kit/auth-required leaves on the spawn's trust.
// The peer spawned this process and holds the only ends of its
// standard streams, and the process runs with the peer's user, so the
// peer already holds every credential the command would use — the
// socket's owner-only argument, applied to a pair of pipes.
func (s *stdioServing) authenticated(context.Context, *mcp.CallToolRequest) bool { return true }

// readCloser returns r as an io.ReadCloser, closing it when it can be
// closed so that ending the session unblocks a pending read.
func readCloser(r io.Reader) io.ReadCloser {
	if rc, ok := r.(io.ReadCloser); ok {
		return rc
	}
	return io.NopCloser(r)
}

// nopWriteCloser keeps the protocol stream open when the session
// closes: closing the process's standard output would let the next
// file the process opens take its descriptor.
type nopWriteCloser struct{ io.Writer }

func (nopWriteCloser) Close() error { return nil }
