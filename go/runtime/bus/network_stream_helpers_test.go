package bus

import (
	"context"
	"encoding/json"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	"github.com/coder/websocket"
)

// failAfter bounds every wait below. Tests synchronize on events,
// gates and injected ticks; the bound only turns a hang into a failure.
const failAfter = 5 * time.Second

// got is one event a collector saw, with its delivery metadata.
type got struct {
	Event
	pos    StreamPosition
	hasPos bool
}

// collector records every event published on a bus, in delivery order.
type collector struct {
	ch chan got
}

func collect(b Bus, pattern string) *collector {
	c := &collector{ch: make(chan got, 4096)}
	b.Subscribe(pattern, func(ctx context.Context, e Event) error {
		p, ok := StreamPositionFrom(ctx)
		c.ch <- got{e, p, ok}
		return nil
	})
	return c
}

func (c *collector) next(t *testing.T) got {
	t.Helper()
	select {
	case g := <-c.ch:
		return g
	case <-time.After(failAfter):
		t.Fatal("no event")
		return got{}
	}
}

// nextTopic returns the next event, failing unless its topic is want.
func (c *collector) nextTopic(t *testing.T, want Topic) got {
	t.Helper()
	g := c.next(t)
	if g.Topic != want {
		t.Fatalf("event %s, want %s (payload %v)", g.Topic, want, g.Payload)
	}
	return g
}

func (c *collector) none(t *testing.T) {
	t.Helper()
	select {
	case g := <-c.ch:
		t.Fatalf("unexpected event %s %v", g.Topic, g.Payload)
	default:
	}
}

func field(g got, k string) any { return g.Payload.(map[string]any)[k] }

// gateAuth accepts any token; armed, Verify blocks until released, so a
// test can hold a reconnect while it publishes. reject fails Verify.
type gateAuth struct {
	mu      sync.Mutex
	gate    chan struct{}
	reject  bool
	entered chan struct{} // optional: signaled when a gated Verify starts waiting
}

func (g *gateAuth) Token() (string, error) { return "open", nil }

func (g *gateAuth) Verify(string) error {
	g.mu.Lock()
	gate, rej := g.gate, g.reject
	g.mu.Unlock()
	if rej {
		return ErrAuthFailed
	}
	if gate != nil {
		if g.entered != nil {
			g.entered <- struct{}{}
		}
		<-gate
	}
	return nil
}

func (g *gateAuth) arm() {
	g.mu.Lock()
	g.gate = make(chan struct{})
	g.mu.Unlock()
}

// hold arms a new gate and returns it; later Verify calls wait on it
// until open(gate). Earlier waiters keep waiting on their own gate.
func (g *gateAuth) hold() chan struct{} {
	g.mu.Lock()
	defer g.mu.Unlock()
	g.gate = make(chan struct{})
	return g.gate
}

// open releases the waiters of one gate; if it is the current gate,
// later Verify calls pass.
func (g *gateAuth) open(gate chan struct{}) {
	g.mu.Lock()
	if g.gate == gate {
		g.gate = nil
	}
	g.mu.Unlock()
	close(gate)
}

// pass lets later Verify calls through without releasing earlier waiters.
func (g *gateAuth) pass() {
	g.mu.Lock()
	g.gate = nil
	g.mu.Unlock()
}

func (g *gateAuth) release() {
	g.mu.Lock()
	if g.gate != nil {
		close(g.gate)
		g.gate = nil
	}
	g.reject = false
	g.mu.Unlock()
}

func (g *gateAuth) setReject(v bool) {
	g.mu.Lock()
	g.reject = v
	g.mu.Unlock()
}

// manualClock replaces the adapter's tickers; tick delivers one tick to
// every live ticker and returns once each was received.
type manualClock struct {
	mu    sync.Mutex
	chans map[chan time.Time]struct{}
}

func newManualClock() *manualClock { return &manualClock{chans: map[chan time.Time]struct{}{}} }

func (c *manualClock) newTicker(time.Duration) (<-chan time.Time, func()) {
	ch := make(chan time.Time)
	c.mu.Lock()
	c.chans[ch] = struct{}{}
	c.mu.Unlock()
	return ch, func() {
		c.mu.Lock()
		delete(c.chans, ch)
		c.mu.Unlock()
	}
}

func (c *manualClock) tick(t *testing.T) {
	t.Helper()
	c.mu.Lock()
	chans := make([]chan time.Time, 0, len(c.chans))
	for ch := range c.chans {
		chans = append(chans, ch)
	}
	c.mu.Unlock()
	if len(chans) == 0 {
		t.Fatal("no ticker to tick")
	}
	for _, ch := range chans {
		select {
		case ch <- time.Now():
		case <-time.After(failAfter):
			t.Fatal("ticker not drained")
		}
	}
}

func withClock(c *manualClock) NetworkOption {
	return func(n *NetworkAdapter) { n.newTicker = c.newTicker }
}

func withHook(ch chan string) NetworkOption {
	return func(n *NetworkAdapter) {
		n.testHook = func(ev string) {
			select {
			case ch <- ev:
			default:
			}
		}
	}
}

type node struct {
	bus Bus
	n   *NetworkAdapter
	url string
}

// newHub serves an adapter over httptest.
func newHub(t *testing.T, opts ...NetworkOption) node {
	t.Helper()
	b := New()
	n := NewNetworkAdapter(b, opts...)
	srv := httptest.NewServer(n.Handler())
	t.Cleanup(func() {
		_ = n.Close()
		srv.Close()
	})
	return node{bus: b, n: n, url: "ws" + srv.URL[4:]}
}

func newClient(t *testing.T, opts ...NetworkOption) node {
	t.Helper()
	b := New()
	n := NewNetworkAdapter(b, opts...)
	t.Cleanup(func() { _ = n.Close() })
	return node{bus: b, n: n}
}

func connect(t *testing.T, c node, url string) {
	t.Helper()
	if err := c.n.Connect(context.Background(), url); err != nil {
		t.Fatalf("connect: %v", err)
	}
}

// dropLink closes addr's live link as a network failure would.
func dropLink(t *testing.T, n *NetworkAdapter, addr string) {
	t.Helper()
	n.mu.RLock()
	l := n.conns[addr]
	n.mu.RUnlock()
	if l == nil {
		t.Fatalf("no link to %s", addr)
	}
	l.close(statusGoingAway, "test drop", "")
}

func publish(t *testing.T, b Bus, topic Topic, ws string) {
	t.Helper()
	e := NewEvent(topic, "test", nil)
	e.WorkspaceID = ws
	if err := b.Publish(context.Background(), e); err != nil {
		t.Fatal(err)
	}
}

// rawDial opens a bare WebSocket to url and sends first as its first
// frame, for wire-level tests.
func rawDial(t *testing.T, url string, first string) *websocket.Conn {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), failAfter)
	defer cancel()
	c, _, err := websocket.Dial(ctx, url, nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = c.CloseNow() })
	if first != "" {
		rawSend(t, c, first)
	}
	return c
}

func rawSend(t *testing.T, c *websocket.Conn, frame string) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), failAfter)
	defer cancel()
	if err := c.Write(ctx, websocket.MessageText, []byte(frame)); err != nil {
		t.Fatal(err)
	}
}

func rawRead(t *testing.T, c *websocket.Conn) []byte {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), failAfter)
	defer cancel()
	_, data, err := c.Read(ctx)
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	return data
}

func rawJSON(t *testing.T, c *websocket.Conn) map[string]any {
	t.Helper()
	var m map[string]any
	if err := json.Unmarshal(rawRead(t, c), &m); err != nil {
		t.Fatal(err)
	}
	return m
}
