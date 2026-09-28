package mcpsdk

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"hop.top/kit/go/transport/cmdsurface"
)

// endSignal wraps a peer's input and closes eof once that input has
// reported its end, so a tool can act at the moment the peer has
// nothing more to say.
type endSignal struct {
	r    io.Reader
	once sync.Once
	eof  chan struct{}
}

func newEndSignal(script ...string) *endSignal {
	return &endSignal{r: strings.NewReader(strings.Join(script, "\n") + "\n"), eof: make(chan struct{})}
}

func (e *endSignal) Read(p []byte) (int, error) {
	n, err := e.r.Read(p)
	if errors.Is(err, io.EOF) {
		e.once.Do(func() { close(e.eof) })
	}
	return n, err
}

// wire collects everything the server writes.
type wire struct {
	mu  sync.Mutex
	buf bytes.Buffer
}

func (w *wire) Write(p []byte) (int, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.buf.Write(p)
}

func (w *wire) Close() error { return nil }

// responses decodes every response on the wire, batches included,
// keyed by request id.
func (w *wire) responses(t *testing.T) map[float64]map[string]any {
	t.Helper()
	w.mu.Lock()
	defer w.mu.Unlock()
	got := map[float64]map[string]any{}
	for _, line := range strings.Split(strings.TrimSpace(w.buf.String()), "\n") {
		if line == "" {
			continue
		}
		var msgs []map[string]any
		if strings.HasPrefix(line, "[") {
			require.NoError(t, json.Unmarshal([]byte(line), &msgs), line)
		} else {
			var m map[string]any
			require.NoError(t, json.Unmarshal([]byte(line), &m), line)
			msgs = append(msgs, m)
		}
		for _, m := range msgs {
			if id, ok := m["id"].(float64); ok && m["method"] == nil {
				got[id] = m
			}
		}
	}
	return got
}

const (
	initLine        = `{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2025-06-18","capabilities":{},"clientInfo":{"name":"host","version":"0"}}}`
	initializedLine = `{"jsonrpc":"2.0","method":"notifications/initialized"}`
	callLine        = `{"jsonrpc":"2.0","id":2,"method":"tools/call","params":{"name":"after-eof","arguments":{}}}`
	listLine        = `{"jsonrpc":"2.0","id":3,"method":"tools/list"}`
)

// endServer is a raw SDK server with one tool, after-eof, which runs
// only once the peer's input has ended. A transport that abandons
// calls at end of input cancels it first.
func endServer(eof <-chan struct{}, work func(context.Context, *mcp.CallToolRequest) (string, error)) *mcp.Server {
	srv := mcp.NewServer(&mcp.Implementation{Name: "end", Version: "0"}, nil)
	mcp.AddTool(srv, &mcp.Tool{Name: "after-eof"}, func(ctx context.Context, req *mcp.CallToolRequest, _ struct{}) (*mcp.CallToolResult, any, error) {
		select {
		case <-eof:
		case <-ctx.Done():
			return nil, nil, ctx.Err()
		}
		text, err := work(ctx, req)
		if err != nil {
			return nil, nil, err
		}
		return &mcp.CallToolResult{Content: []mcp.Content{&mcp.TextContent{Text: text}}}, nil, nil
	})
	return srv
}

// outlive answers "done" unless the call is canceled first: the SDK
// cancels a call the moment it gives up on the connection, so the
// grace period tells a finished call from an abandoned one.
func outlive(ctx context.Context, _ *mcp.CallToolRequest) (string, error) {
	select {
	case <-ctx.Done():
		return "", ctx.Err()
	case <-time.After(200 * time.Millisecond):
		return "done", nil
	}
}

// waitSession waits for the session to end on its own.
func waitSession(t *testing.T, ss *mcp.ServerSession) error {
	t.Helper()
	done := make(chan error, 1)
	go func() { done <- ss.Wait() }()
	select {
	case err := <-done:
		return err
	case <-time.After(5 * time.Second):
		// Close waits for the calls in flight, which may never end;
		// the test fails either way.
		go func() { _ = ss.Close() }()
		t.Fatal("the session did not end after end of input")
		return nil
	}
}

// TestSDKIOTransportAbandonsCallsAtEndOfInput pins the SDK behavior
// StdioTransport works around: over the SDK's own IOTransport, end of
// input cancels the calls in flight and their responses are never
// written. If this fails, the SDK now answers such calls and
// StdioTransport's hold on end of input may be redundant.
func TestSDKIOTransportAbandonsCallsAtEndOfInput(t *testing.T) {
	in := newEndSignal(initLine, initializedLine, callLine)
	out := &wire{}
	srv := endServer(in.eof, func(ctx context.Context, _ *mcp.CallToolRequest) (string, error) {
		select {
		case <-ctx.Done():
			return "", ctx.Err()
		case <-time.After(3 * time.Second):
			return "done", nil
		}
	})

	ss, err := srv.Connect(t.Context(), &mcp.IOTransport{Reader: io.NopCloser(in), Writer: out}, nil)
	require.NoError(t, err)
	err = waitSession(t, ss)

	assert.NotContains(t, out.responses(t), float64(2),
		"the SDK answered a call in flight at end of input; StdioTransport's hold may be redundant")
	require.Error(t, err, "the SDK reports the abandoned response as a session error")
	assert.Contains(t, err.Error(), "server is closing")
}

func TestStdioTransportAnswersCallsReadBeforeEndOfInput(t *testing.T) {
	in := newEndSignal(initLine, initializedLine, callLine, listLine)
	out := &wire{}
	tr := NewStdioTransport(in, out)

	ss, err := endServer(in.eof, outlive).Connect(t.Context(), tr, nil)
	require.NoError(t, err)
	err = waitSession(t, ss)
	require.NoError(t, err)
	require.NoError(t, tr.SessionEnd(err))

	got := out.responses(t)
	require.Contains(t, got, float64(1), "initialize answered")
	require.Contains(t, got, float64(3), "tools/list answered")
	require.Contains(t, got, float64(2), "the call in flight at end of input is answered")
	assert.Nil(t, got[2]["error"], "%v", got[2])
	assert.Contains(t, toJSON(t, got[2]["result"]), `"done"`)
}

// TestStdioTransportAnswersABatchReadBeforeEndOfInput covers the batch
// form revisions before 2025-06-18 allow: the batch's responses go out
// together, once every call in it is answered.
func TestStdioTransportAnswersABatchReadBeforeEndOfInput(t *testing.T) {
	legacyInit := strings.Replace(initLine, "2025-06-18", "2025-03-26", 1)
	batch := "[" + callLine + "," + listLine + "]"
	in := newEndSignal(legacyInit, initializedLine, batch)
	out := &wire{}
	tr := NewStdioTransport(in, out)

	ss, err := endServer(in.eof, outlive).Connect(t.Context(), tr, nil)
	require.NoError(t, err)
	require.NoError(t, tr.SessionEnd(waitSession(t, ss)))

	got := out.responses(t)
	require.Contains(t, got, float64(2))
	require.Contains(t, got, float64(3))
	assert.Contains(t, toJSON(t, got[2]["result"]), `"done"`)
}

// TestStdioTransportEndsWhenThePeerCannotAnswer: a call that is waiting
// on the peer — here a server-initiated ping — can never complete once
// the peer has closed its input, so end of input is not held for it.
// The session ends promptly and still counts as a clean stop.
func TestStdioTransportEndsWhenThePeerCannotAnswer(t *testing.T) {
	in := newEndSignal(initLine, initializedLine, callLine)
	out := &wire{}
	tr := NewStdioTransport(in, out)

	srv := endServer(in.eof, func(ctx context.Context, req *mcp.CallToolRequest) (string, error) {
		if err := req.Session.Ping(ctx, nil); err != nil {
			return "", err
		}
		return "pinged", nil
	})
	ss, err := srv.Connect(t.Context(), tr, nil)
	require.NoError(t, err)
	err = waitSession(t, ss)
	assert.NoError(t, tr.SessionEnd(err), "the peer ending its input is a clean stop: %v", err)
	assert.Contains(t, out.responses(t), float64(1))
}

func TestStdioTransportEndsAtOnceWhenNothingIsInFlight(t *testing.T) {
	for name, script := range map[string][]string{
		"empty input":     nil,
		"answered":        {initLine, initializedLine},
		"notification":    {initializedLine},
		"blank lines too": {"", initLine, ""},
	} {
		t.Run(name, func(t *testing.T) {
			in := newEndSignal(script...)
			out := &wire{}
			tr := NewStdioTransport(in, out)
			ss, err := endServer(in.eof, outlive).Connect(t.Context(), tr, nil)
			require.NoError(t, err)
			start := time.Now()
			err = waitSession(t, ss)
			require.NoError(t, tr.SessionEnd(err))
			assert.Less(t, time.Since(start), time.Second)
		})
	}
}

// TestStdioTransportSessionEndKeepsOtherFailures: only the peer ending
// its input makes a failure a clean stop.
func TestStdioTransportSessionEndKeepsOtherFailures(t *testing.T) {
	boom := errors.New("boom")
	tr := NewStdioTransport(strings.NewReader(""), &wire{})
	assert.NoError(t, tr.SessionEnd(nil))
	assert.ErrorIs(t, tr.SessionEnd(boom), boom, "input has not ended")

	tr.st.end()
	assert.ErrorIs(t, tr.SessionEnd(boom), boom, "an unrelated failure stays a failure after end of input")
}

// TestStdioTransportCloseReleasesTheHold: closing the session unblocks
// a read held at end of input, whatever is still in flight.
func TestStdioTransportCloseReleasesTheHold(t *testing.T) {
	st := newDrainState()
	r := st.reader(strings.NewReader(callLine + "\n"))
	buf := make([]byte, 4096)
	n, err := r.Read(buf)
	require.NoError(t, err)
	require.Positive(t, n)

	got := make(chan error, 1)
	go func() {
		_, err := r.Read(buf)
		got <- err
	}()
	select {
	case err := <-got:
		t.Fatalf("end of input released with a call unanswered: %v", err)
	case <-time.After(100 * time.Millisecond):
	}
	require.NoError(t, r.Close())
	select {
	case err := <-got:
		assert.ErrorIs(t, err, io.EOF)
	case <-time.After(time.Second):
		t.Fatal("close did not release the held read")
	}
}

func TestStdioTransportConnectsOnce(t *testing.T) {
	tr := NewStdioTransport(strings.NewReader(""), &wire{})
	_, err := tr.Connect(t.Context())
	require.NoError(t, err)
	_, err = tr.Connect(t.Context())
	assert.Error(t, err)
}

func toJSON(t *testing.T, v any) string {
	t.Helper()
	b, err := json.Marshal(v)
	require.NoError(t, err)
	return string(b)
}

// TestServeStdioAnswersCallsReadBeforeEndOfInput drives ServeStdio on
// the process's own standard streams, swapped for pipes: a host that
// writes its requests and closes its end gets every answer, and
// ServeStdio returns nil.
func TestServeStdioAnswersCallsReadBeforeEndOfInput(t *testing.T) {
	inR, inW, err := os.Pipe()
	require.NoError(t, err)
	outR, outW, err := os.Pipe()
	require.NoError(t, err)
	origIn, origOut := os.Stdin, os.Stdout
	os.Stdin, os.Stdout = inR, outW
	t.Cleanup(func() { os.Stdin, os.Stdout = origIn, origOut })

	s, err := New(cmdsurface.New(newTestTree()), WithServerConfigurator(func(srv *mcp.Server) {
		mcp.AddTool(srv, &mcp.Tool{Name: "outlive"}, func(ctx context.Context, req *mcp.CallToolRequest, _ struct{}) (*mcp.CallToolResult, any, error) {
			text, err := outlive(ctx, req)
			if err != nil {
				return nil, nil, err
			}
			return &mcp.CallToolResult{Content: []mcp.Content{&mcp.TextContent{Text: text}}}, nil, nil
		})
	}))
	require.NoError(t, err)

	out := &wire{}
	copied := make(chan struct{})
	go func() { _, _ = io.Copy(out, outR); close(copied) }()
	_, err = io.WriteString(inW, strings.Join([]string{initLine, initializedLine,
		`{"jsonrpc":"2.0","id":2,"method":"tools/call","params":{"name":"outlive","arguments":{}}}`,
		`{"jsonrpc":"2.0","id":3,"method":"tools/call","params":{"name":"ping","arguments":{}}}`,
	}, "\n")+"\n")
	require.NoError(t, err)
	require.NoError(t, inW.Close())

	served := make(chan error, 1)
	go func() { served <- s.ServeStdio(t.Context()) }()
	select {
	case err := <-served:
		require.NoError(t, err, "end of input is a clean end")
	case <-time.After(5 * time.Second):
		t.Fatal("ServeStdio did not return at end of input")
	}
	require.NoError(t, outW.Close())
	<-copied

	got := out.responses(t)
	require.Contains(t, got, float64(2), "the call in flight at end of input is answered")
	require.Contains(t, got, float64(3))
	assert.Contains(t, toJSON(t, got[2]["result"]), `"done"`)
	assert.Contains(t, toJSON(t, got[3]["result"]), "pong")
}

func TestSplitterCutsTopLevelValues(t *testing.T) {
	stream := `{"a":"}{[\"x","n":[1,{"b":"]"}]}` + "\r\n  " + `[{"c":{}},{"d":"\\"}]` + "\n" + `{"e":1}`
	for _, chunk := range []int{1, 3, len(stream)} {
		var sp splitter
		var got []string
		for i := 0; i < len(stream); i += chunk {
			end := min(i+chunk, len(stream))
			require.True(t, sp.feed([]byte(stream[i:end]), func(v []byte) { got = append(got, string(v)) }))
		}
		assert.Equal(t, []string{
			`{"a":"}{[\"x","n":[1,{"b":"]"}]}`,
			`[{"c":{}},{"d":"\\"}]`,
			`{"e":1}`,
		}, got, "chunk %d", chunk)
	}

	var sp splitter
	assert.False(t, sp.feed([]byte(`not json`), func([]byte) {}), "a top-level scalar is not a message")
}
