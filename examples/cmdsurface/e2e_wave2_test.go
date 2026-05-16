//go:build e2e

// Wave 2 e2e suite. Exercises WebSocket, SSE, Bus, Cron, Library, and
// the sink fan-out against the live exampleApp built by setup.go.
// Build-tagged so `go test ./...` ignores it by default; run with
// `go test -tags=e2e -race -count=1 ./examples/cmdsurface/...`.
package main

import (
	"bufio"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/coder/websocket"

	"hop.top/kit/go/transport/cmdsurface"
)

// wsRawFrame mirrors the on-wire envelope MountWS uses. Kept here so
// the test does not rely on the package's internal wsFrame.
type wsRawFrame struct {
	Op         string                 `json:"op"`
	ID         string                 `json:"id,omitempty"`
	Invocation *cmdsurface.Invocation `json:"invocation,omitempty"`
	Event      *cmdsurface.Event      `json:"event,omitempty"`
	Result     *cmdsurface.Result     `json:"result,omitempty"`
	Error      *wsRawError            `json:"error,omitempty"`
}

type wsRawError struct {
	Code    string `json:"code"`
	Message string `json:"message"`
}

// wsDial opens a WebSocket against the live example's /ws/cmd
// endpoint and returns the open connection. Cleanup is registered.
//
// The example exposes `report purge` (auth-required) on the WS
// surface; MountWS aggregates safety across all WS-enabled leaves and
// gates the upgrade with the strictest matrix. We always send an
// Authorization header so the upgrade succeeds; per-invoke gates
// fire on the destructive policy check inside the connection.
func wsDial(t *testing.T, le *liveExample) *websocket.Conn {
	t.Helper()
	wsURL := "ws" + strings.TrimPrefix(le.httpURL, "http") + "/ws/cmd"
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	hdr := http.Header{}
	hdr.Set("Authorization", "Bearer test")
	c, _, err := websocket.Dial(ctx, wsURL, &websocket.DialOptions{HTTPHeader: hdr})
	if err != nil {
		t.Fatalf("dial %s: %v", wsURL, err)
	}
	t.Cleanup(func() { _ = c.Close(websocket.StatusNormalClosure, "") })
	return c
}

// wsWrite sends a JSON-encoded frame on c. Errors fail the test.
func wsWrite(t *testing.T, ctx context.Context, c *websocket.Conn, f wsRawFrame) {
	t.Helper()
	data, err := json.Marshal(f)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	if err := c.Write(ctx, websocket.MessageText, data); err != nil {
		t.Fatalf("write: %v", err)
	}
}

// wsRead reads one JSON frame; nil errors fail the test.
func wsRead(t *testing.T, ctx context.Context, c *websocket.Conn) wsRawFrame {
	t.Helper()
	_, data, err := c.Read(ctx)
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	var f wsRawFrame
	if err := json.Unmarshal(data, &f); err != nil {
		t.Fatalf("unmarshal: %v (raw=%s)", err, data)
	}
	return f
}

// --- WebSocket ---

func TestE2E_Wave2_WSHappyPath(t *testing.T) {
	le := start(t)
	c := wsDial(t, le)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	wsWrite(t, ctx, c, wsRawFrame{
		Op:         "invoke",
		ID:         "1",
		Invocation: &cmdsurface.Invocation{Path: []string{"ping"}},
	})

	sawEvent := false
	for {
		f := wsRead(t, ctx, c)
		switch f.Op {
		case "event":
			sawEvent = true
		case "result":
			if f.ID != "1" {
				t.Errorf("result.id=%q want 1", f.ID)
			}
			if f.Result == nil || !strings.Contains(f.Result.Stdout, "pong") {
				t.Errorf("result=%+v want stdout containing pong", f.Result)
			}
			if !sawEvent {
				t.Error("expected at least one event frame before result")
			}
			return
		case "error":
			t.Fatalf("unexpected error frame: %+v", f.Error)
		default:
			t.Fatalf("unexpected op=%q", f.Op)
		}
	}
}

func TestE2E_Wave2_WSStreamTick(t *testing.T) {
	le := start(t)
	c := wsDial(t, le)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	wsWrite(t, ctx, c, wsRawFrame{
		Op: "invoke",
		ID: "t",
		Invocation: &cmdsurface.Invocation{
			Path: []string{"tick"},
			Flags: map[string]any{
				"count":    3,
				"interval": "20ms",
			},
		},
	})

	events := 0
	for {
		f := wsRead(t, ctx, c)
		switch f.Op {
		case "event":
			// Count stdout events only; the runner may emit auxiliary
			// "done" events with no Data string, but the surface
			// forwards stdout/stderr/done uniformly. Filter by Kind.
			if f.Event != nil && f.Event.Kind == "stdout" {
				events++
			}
		case "result":
			if events != 3 {
				t.Errorf("stdout events=%d want 3", events)
			}
			if f.Result == nil {
				t.Fatal("result frame missing Result body")
			}
			return
		case "error":
			t.Fatalf("unexpected error: %+v", f.Error)
		}
	}
}

func TestE2E_Wave2_WSCancellation(t *testing.T) {
	le := start(t)
	c := wsDial(t, le)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	// 50 ticks at 500ms each = 25s. Read the first event, cancel, then
	// assert the stream halts with a terminal frame: with the fix for
	// T-1281, the surface writes terminal frames under the connection
	// ctx (not the cancelled per-invocation ctx), so a cancelled
	// invocation MUST deliver a closing error or result frame.
	wsWrite(t, ctx, c, wsRawFrame{
		Op: "invoke",
		ID: "long",
		Invocation: &cmdsurface.Invocation{
			Path: []string{"tick"},
			Flags: map[string]any{
				"count":    50,
				"interval": "500ms",
			},
		},
	})

	// Drain the first event so we know the run has started.
	first := wsRead(t, ctx, c)
	if first.Op != "event" {
		t.Fatalf("first frame op=%q want event (frame=%+v)", first.Op, first)
	}

	// Cancel.
	wsWrite(t, ctx, c, wsRawFrame{Op: "cancel", ID: "long"})

	// Read frames for up to 3s — if the runner observed the
	// cancellation, we should NOT see anywhere near 50 events.
	eventsAfterCancel := 0
	terminated := false
	readUntil := time.Now().Add(3 * time.Second)
loop:
	for time.Now().Before(readUntil) {
		readCtx, readCancel := context.WithDeadline(ctx, readUntil)
		_, data, err := c.Read(readCtx)
		readCancel()
		if err != nil {
			// Read deadline expired or connection error → stream halted.
			break loop
		}
		var f wsRawFrame
		if err := json.Unmarshal(data, &f); err != nil {
			t.Fatalf("unmarshal: %v", err)
		}
		switch f.Op {
		case "event":
			eventsAfterCancel++
		case "result", "error":
			terminated = true
			break loop
		}
	}

	// 50 events would arrive only if the runner ignored the cancel.
	// Assert we received substantially fewer.
	if eventsAfterCancel >= 40 {
		t.Errorf("events after cancel=%d; cancel did not take effect", eventsAfterCancel)
	}
	// Terminal frame is now a hard requirement (T-1281 fix).
	if !terminated {
		t.Errorf("no terminal frame received after cancel; T-1281 regression")
	}
}

func TestE2E_Wave2_WSDestructiveBlocked(t *testing.T) {
	le := start(t)
	c := wsDial(t, le)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	// `report purge` is destructive AND auth-required. wsDial already
	// passes the aggregate auth gate by sending an Authorization
	// header; the destructive policy gate then refuses the per-invoke
	// frame and returns an error envelope.
	wsWrite(t, ctx, c, wsRawFrame{
		Op: "invoke",
		ID: "p",
		Invocation: &cmdsurface.Invocation{
			Path:  []string{"report", "purge"},
			Flags: map[string]any{"before": "yesterday"},
		},
	})

	f := wsRead(t, ctx, c)
	if f.Op != "error" {
		t.Fatalf("op=%q want error (frame=%+v)", f.Op, f)
	}
	if f.Error == nil || f.Error.Code != "destructive_blocked" {
		t.Errorf("error=%+v want code=destructive_blocked", f.Error)
	}
}

// --- SSE ---

// sseFrame is one parsed Server-Sent-Events frame.
type sseFrame struct {
	Event   string
	Data    string
	Comment string
}

// sseReader reads SSE frames from a streaming response body.
type sseReader struct{ sc *bufio.Scanner }

func newSSEReader(r io.Reader) *sseReader {
	sc := bufio.NewScanner(r)
	sc.Buffer(make([]byte, 0, 64*1024), 1024*1024)
	return &sseReader{sc: sc}
}

func (r *sseReader) next() (sseFrame, error) {
	var f sseFrame
	hasFields := false
	for r.sc.Scan() {
		line := r.sc.Text()
		if line == "" {
			if hasFields {
				return f, nil
			}
			continue
		}
		hasFields = true
		switch {
		case strings.HasPrefix(line, ":"):
			f.Comment = strings.TrimPrefix(line, ":")
		case strings.HasPrefix(line, "event:"):
			f.Event = strings.TrimSpace(strings.TrimPrefix(line, "event:"))
		case strings.HasPrefix(line, "data:"):
			d := strings.TrimPrefix(line, "data:")
			if strings.HasPrefix(d, " ") {
				d = d[1:]
			}
			if f.Data == "" {
				f.Data = d
			} else {
				f.Data += "\n" + d
			}
		}
	}
	if err := r.sc.Err(); err != nil {
		return f, err
	}
	if !hasFields {
		return f, io.EOF
	}
	return f, nil
}

func TestE2E_Wave2_SSEHappyPath(t *testing.T) {
	le := start(t)

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	req, err := http.NewRequestWithContext(ctx, http.MethodGet,
		le.httpURL+"/cmd/ping/stream", nil)
	if err != nil {
		t.Fatalf("NewRequest: %v", err)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("Do: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		t.Fatalf("status=%d body=%s", resp.StatusCode, body)
	}
	if ct := resp.Header.Get("Content-Type"); ct != "text/event-stream" {
		t.Errorf("Content-Type=%q want text/event-stream", ct)
	}

	rdr := newSSEReader(resp.Body)
	sawEvent, sawResult := false, false
	for {
		f, err := rdr.next()
		if err == io.EOF {
			break
		}
		if err != nil {
			t.Fatalf("read frame: %v", err)
		}
		switch f.Event {
		case "event":
			sawEvent = true
		case "result":
			sawResult = true
			var res cmdsurface.Result
			if err := json.Unmarshal([]byte(f.Data), &res); err != nil {
				t.Fatalf("decode result data %q: %v", f.Data, err)
			}
			if !strings.Contains(res.Stdout, "pong") {
				t.Errorf("Result.Stdout=%q want pong", res.Stdout)
			}
		}
		if sawResult {
			break
		}
	}
	if !sawEvent {
		t.Error("did not see any event frame")
	}
	if !sawResult {
		t.Error("did not see terminal result frame")
	}
}

func TestE2E_Wave2_SSETickStream(t *testing.T) {
	le := start(t)

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	url := le.httpURL + "/cmd/tick/stream?flag.count=3&flag.interval=20ms"
	req, _ := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("Do: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status=%d", resp.StatusCode)
	}

	rdr := newSSEReader(resp.Body)
	events, sawResult := 0, false
	for {
		f, err := rdr.next()
		if err == io.EOF {
			break
		}
		if err != nil {
			t.Fatalf("read: %v", err)
		}
		switch f.Event {
		case "event":
			// Count stdout-bearing events only.
			var ev cmdsurface.Event
			if err := json.Unmarshal([]byte(f.Data), &ev); err == nil && ev.Kind == "stdout" {
				events++
			}
		case "result":
			sawResult = true
		}
		if sawResult {
			break
		}
	}
	if events != 3 {
		t.Errorf("stdout events=%d want 3", events)
	}
	if !sawResult {
		t.Error("did not see terminal result frame")
	}
}

func TestE2E_Wave2_SSEDestructiveBlocked(t *testing.T) {
	le := start(t)

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	req, _ := http.NewRequestWithContext(ctx, http.MethodGet,
		le.httpURL+"/cmd/report/purge/stream", nil)
	// SSE auth gate requires Authorization for auth-required leaves.
	req.Header.Set("Authorization", "Bearer test")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("Do: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusForbidden {
		body, _ := io.ReadAll(resp.Body)
		t.Fatalf("status=%d body=%s; want 403", resp.StatusCode, body)
	}
	body, _ := io.ReadAll(resp.Body)
	if !strings.Contains(string(body), "destructive_blocked") {
		t.Errorf("body=%s does not contain destructive_blocked", body)
	}
}

func TestE2E_Wave2_SSEClientDisconnect(t *testing.T) {
	le := start(t)

	// Long-running stream — 50 ticks at 100ms each. We read one event,
	// then cancel the request context. The connection must close
	// cleanly within 1s; we assert by reading and observing io.EOF /
	// an error within that deadline.
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	url := le.httpURL + "/cmd/tick/stream?flag.count=50&flag.interval=100ms"
	req, _ := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("Do: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status=%d", resp.StatusCode)
	}

	rdr := newSSEReader(resp.Body)
	if _, err := rdr.next(); err != nil {
		t.Fatalf("read first frame: %v", err)
	}
	cancel() // disconnect from client side.

	// Verify the body closes within a short window.
	done := make(chan struct{})
	go func() {
		_, _ = io.Copy(io.Discard, resp.Body)
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Error("body did not close within 2s after client cancel")
	}
}

// --- Bus round-trip ---

func TestE2E_Wave2_BusRoundTrip(t *testing.T) {
	le := start(t)
	bus := le.app.Bus
	if bus == nil {
		t.Fatal("liveExample.app.Bus is nil")
	}

	// Subscribe to the response topic so we can observe the publication
	// the bus surface emits after invoking widget add.
	resp := make(chan cmdsurface.BusMessage, 1)
	cancel, err := bus.Subscribe(context.Background(), "widgets.add.resp",
		func(msg cmdsurface.BusMessage) error {
			select {
			case resp <- msg:
			default:
			}
			return nil
		},
	)
	if err != nil {
		t.Fatalf("Subscribe widgets.add.resp: %v", err)
	}
	defer cancel()

	// Publish a synthetic request to widgets.add.req. The bus surface
	// subscribed at MountBus time; it will decode this payload, invoke
	// `widget add`, and publish the Result on widgets.add.resp.
	if err := bus.Publish(context.Background(), "widgets.add.req", "test",
		map[string]any{
			"flags": map[string]any{"name": "bus-foo"},
		},
	); err != nil {
		t.Fatalf("Publish widgets.add.req: %v", err)
	}

	select {
	case msg := <-resp:
		// Payload is JSON-encoded Result.
		var res cmdsurface.Result
		if err := json.Unmarshal(msg.Payload, &res); err != nil {
			t.Fatalf("decode response payload: %v (raw=%s)", err, msg.Payload)
		}
		if !strings.Contains(res.Stdout, "widget add: name=bus-foo") {
			t.Errorf("Result.Stdout=%q want contains widget add: name=bus-foo", res.Stdout)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for widgets.add.resp")
	}
}

// --- Library (in-process) ---

func TestE2E_Wave2_LibInvokeArgs(t *testing.T) {
	le := start(t)

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	res, err := cmdsurface.InvokeArgs(ctx, le.app.Bridge, []string{"ping"})
	if err != nil {
		t.Fatalf("InvokeArgs: %v", err)
	}
	if !strings.Contains(res.Stdout, "pong") {
		t.Errorf("Result.Stdout=%q want contains pong", res.Stdout)
	}
}

// --- Sinks ---

func TestE2E_Wave2_SinkFanOut(t *testing.T) {
	le := start(t)

	// Drive at least one invocation via REST so the sinkRunner fires.
	// REST is sufficient — every surface goes through the same Runner.
	body := strings.NewReader(`{"flags":{"name":"sink-test"}}`)
	req, err := http.NewRequest(http.MethodPost, le.httpURL+"/cmd/widget/add", body)
	if err != nil {
		t.Fatalf("NewRequest: %v", err)
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("Do: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status=%d", resp.StatusCode)
	}

	// FileSink writes to le.app.SinkBuf. After the request returns
	// the Runner has emitted its JSON-Lines record (sinks run inline
	// inside sinkRunner.Run, BEFORE Run returns to the caller).
	if le.app.SinkBuf.Len() == 0 {
		t.Fatal("SinkBuf is empty; expected FileSink to have written a record")
	}
	body2 := le.app.SinkBuf.String()
	if !strings.Contains(body2, `"path":"widget add"`) {
		t.Errorf("SinkBuf=%q does not contain path=widget add", body2)
	}
	if !strings.Contains(body2, `"surface":"rest"`) {
		t.Errorf("SinkBuf=%q does not contain surface=rest", body2)
	}
}
