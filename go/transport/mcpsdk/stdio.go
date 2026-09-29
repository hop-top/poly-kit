package mcpsdk

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"sync"

	"github.com/modelcontextprotocol/go-sdk/jsonrpc"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// StdioTransport carries one MCP session over a pair of byte streams
// as newline-delimited JSON-RPC — the SDK's IOTransport framing — for
// a server a host spawns and talks to on its standard input and
// output. It differs from the SDK's stdio transports in one respect:
// end of input does not abandon the calls already read.
//
// A host may write its requests and close its end at once
// (`printf ... | tool serve mcp --stdio`). The SDK (go-sdk v1.8.0,
// internal/jsonrpc2) treats end of input as a broken connection: it
// cancels every call in flight and never writes their responses, so
// such a host gets no answer at all (v1.7.0 also ended the session
// with "server is closing: EOF"). StdioTransport holds end of input back from
// the SDK until every call read before it has been answered, so the
// session ends only once those responses are written.
//
// The hold is released early when the server is itself waiting on the
// peer — a server-initiated request such as an elicitation or a ping
// is outstanding — because a peer that closed its input can never
// answer; the SDK then fails that call as before. Closing the session
// releases the hold too.
//
// TestSDKIOTransportAbandonsCallsAtEndOfInput pins the SDK behavior
// this works around.
type StdioTransport struct {
	in  io.Reader
	out io.Writer
	st  *drainState

	mu        sync.Mutex
	connected bool
}

// NewStdioTransport returns a transport reading the peer's messages
// from in and writing the server's to out. in is closed with the
// session when it is an io.Closer; out never is, so a process's
// standard output keeps its descriptor.
func NewStdioTransport(in io.Reader, out io.Writer) *StdioTransport {
	return &StdioTransport{in: in, out: out, st: newDrainState()}
}

// Connect implements [mcp.Transport]. A StdioTransport carries one
// session; a second Connect fails.
func (t *StdioTransport) Connect(ctx context.Context) (mcp.Connection, error) {
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.connected {
		return nil, errors.New("mcpsdk: stdio transport already connected")
	}
	t.connected = true
	iot := &mcp.IOTransport{Reader: t.st.reader(t.in), Writer: t.st.writer(t.out)}
	return iot.Connect(ctx)
}

// SessionEnd maps the error a session over t ended with
// ([mcp.ServerSession.Wait]) onto what its server should report. Once
// the peer has ended its input, the SDK's report of a call it could no
// longer answer is the peer leaving, not a failure: SessionEnd returns
// nil for it. Every other error, and every error before end of input,
// is returned as is.
func (t *StdioTransport) SessionEnd(err error) error {
	if err == nil || !t.st.ended() {
		return err
	}
	if errors.Is(err, io.EOF) || isServerClosing(err) {
		return nil
	}
	return err
}

// codeServerClosing is the JSON-RPC code of the SDK's "server is
// closing" error (internal/jsonrpc2.ErrServerClosing), which the SDK
// does not export.
const codeServerClosing = -32004

func isServerClosing(err error) bool {
	var we *jsonrpc.Error
	return errors.As(err, &we) && we.Code == codeServerClosing
}

// drainState tracks the calls in flight in each direction, as seen on
// the wire, and decides when end of input may reach the SDK.
type drainState struct {
	mu   sync.Mutex
	cond *sync.Cond

	// calls are the peer's calls read and not yet answered.
	calls map[jsonrpc.ID]struct{}
	// awaiting are the server's calls written and not yet answered.
	awaiting map[jsonrpc.ID]struct{}

	in, out splitter

	eof    bool // the input reported its end
	closed bool // the session closed its input
	blind  bool // a value on the wire could not be decoded
}

func newDrainState() *drainState {
	s := &drainState{calls: map[jsonrpc.ID]struct{}{}, awaiting: map[jsonrpc.ID]struct{}{}}
	s.cond = sync.NewCond(&s.mu)
	return s
}

func (s *drainState) reader(r io.Reader) *drainReader { return &drainReader{r: r, st: s} }
func (s *drainState) writer(w io.Writer) *drainWriter { return &drainWriter{w: w, st: s} }

func (s *drainState) end() {
	s.mu.Lock()
	s.eof = true
	s.mu.Unlock()
}

func (s *drainState) ended() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.eof
}

// hold blocks until end of input may reach the SDK: nothing the peer
// asked is unanswered, the server is waiting on the peer, the session
// closed, or the wire stopped making sense.
func (s *drainState) hold() {
	s.mu.Lock()
	defer s.mu.Unlock()
	for !s.closed && !s.blind && len(s.calls) > 0 && len(s.awaiting) == 0 {
		s.cond.Wait()
	}
}

func (s *drainState) close() {
	s.mu.Lock()
	s.closed = true
	s.mu.Unlock()
	s.cond.Broadcast()
}

// observe records the messages in b, read from the peer (in) or
// written to it (out).
func (s *drainState) observe(b []byte, in bool) {
	if len(b) == 0 {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	sp := &s.out
	if in {
		sp = &s.in
	}
	ok := sp.feed(b, func(v []byte) {
		msgs, err := decodeValue(v)
		if err != nil {
			s.blind = true
			return
		}
		for _, m := range msgs {
			s.record(m, in)
		}
	})
	if !ok {
		s.blind = true
	}
	s.cond.Broadcast()
}

func (s *drainState) record(m jsonrpc.Message, in bool) {
	asked, answered := s.awaiting, s.calls
	if in {
		asked, answered = s.calls, s.awaiting
	}
	switch m := m.(type) {
	case *jsonrpc.Request:
		if m.IsCall() {
			asked[m.ID] = struct{}{}
		}
	case *jsonrpc.Response:
		delete(answered, m.ID)
	}
}

// decodeValue decodes one JSON-RPC message or batch.
func decodeValue(v []byte) ([]jsonrpc.Message, error) {
	if v[0] != '[' {
		m, err := jsonrpc.DecodeMessage(v)
		if err != nil {
			return nil, err
		}
		return []jsonrpc.Message{m}, nil
	}
	var raws []json.RawMessage
	if err := json.Unmarshal(v, &raws); err != nil {
		return nil, err
	}
	msgs := make([]jsonrpc.Message, 0, len(raws))
	for _, raw := range raws {
		m, err := jsonrpc.DecodeMessage(raw)
		if err != nil {
			return nil, err
		}
		msgs = append(msgs, m)
	}
	return msgs, nil
}

// drainReader is the peer's input as the SDK reads it: every message
// passes through unchanged, and end of input is held by hold.
type drainReader struct {
	r  io.Reader
	st *drainState
}

func (d *drainReader) Read(p []byte) (int, error) {
	if !d.st.ended() {
		n, err := d.r.Read(p)
		d.st.observe(p[:n], true)
		if !errors.Is(err, io.EOF) {
			return n, err
		}
		d.st.end()
		if n > 0 {
			return n, nil
		}
	}
	d.st.hold()
	return 0, io.EOF
}

// Close releases a held end of input and closes the input when it can
// be closed, so that ending the session unblocks a pending read.
func (d *drainReader) Close() error {
	d.st.close()
	if c, ok := d.r.(io.Closer); ok {
		return c.Close()
	}
	return nil
}

// drainWriter is the server's output: every write passes through
// unchanged and is recorded once written.
type drainWriter struct {
	w  io.Writer
	st *drainState
}

func (d *drainWriter) Write(p []byte) (int, error) {
	n, err := d.w.Write(p)
	d.st.observe(p[:n], false)
	return n, err
}

// Close leaves the output open: closing a process's standard output
// would let the next file it opens take its descriptor.
func (d *drainWriter) Close() error { return nil }

// splitter cuts a byte stream into top-level JSON values: objects and
// arrays, separated by whitespace, as the SDK's decoder reads them.
type splitter struct {
	buf   []byte
	depth int
	str   bool
	esc   bool
}

// feed consumes b, calling emit with each value it completes. It
// reports false when the stream holds something other than an object
// or an array at the top level.
func (s *splitter) feed(b []byte, emit func([]byte)) bool {
	for _, c := range b {
		if s.depth == 0 {
			switch c {
			case ' ', '\t', '\r', '\n':
				continue
			case '{', '[':
				s.buf = append(s.buf[:0], c)
				s.depth = 1
				continue
			default:
				return false
			}
		}
		s.buf = append(s.buf, c)
		switch {
		case s.esc:
			s.esc = false
		case s.str:
			switch c {
			case '\\':
				s.esc = true
			case '"':
				s.str = false
			}
		case c == '"':
			s.str = true
		case c == '{' || c == '[':
			s.depth++
		case c == '}' || c == ']':
			s.depth--
			if s.depth == 0 {
				emit(s.buf)
			}
		}
	}
	return true
}
