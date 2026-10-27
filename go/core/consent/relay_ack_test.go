package consent

import (
	"context"
	"errors"
	"net/http/httptest"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"hop.top/kit/go/runtime/bus"
)

const relayWait = 5 * time.Second

func flushAsync(ctx context.Context, r *Relay) <-chan error {
	ch := make(chan error, 1)
	go func() {
		_, err := r.Flush(ctx)
		ch <- err
	}()
	return ch
}

func waitFlush(t *testing.T, ch <-chan error) error {
	t.Helper()
	select {
	case err := <-ch:
		return err
	case <-time.After(relayWait):
		t.Fatal("Flush did not return")
		return nil
	}
}

func recordOne(t *testing.T, a *FileAuditor) {
	t.Helper()
	g := NewGrants(&MemoryGrantStore{}, fixedClock(), WithAuditor(a))
	if err := g.Decide(context.Background(), allowAlways("io.files.read", "")); err != nil {
		t.Fatal(err)
	}
}

func TestRelay_WaitsForAsyncSubscribers(t *testing.T) {
	t.Setenv("XDG_STATE_HOME", t.TempDir())
	b := bus.New()
	defer b.Close(context.Background())
	release := make(chan struct{})
	var once sync.Once
	open := func() { once.Do(func() { close(release) }) }
	t.Cleanup(open)
	b.SubscribeAsync("kit.core.consent.#", func(context.Context, bus.Event) { <-release })
	a, _ := NewFileAuditorFor("mytool")
	recordOne(t, a)
	r := NewRelay(a, b)
	ch := flushAsync(context.Background(), r)
	select {
	case err := <-ch:
		t.Fatalf("Flush returned before the async subscriber finished: %v", err)
	case <-time.After(50 * time.Millisecond):
	}
	open()
	if err := waitFlush(t, ch); err != nil {
		t.Fatal(err)
	}
	if res, _ := r.Flush(context.Background()); res.Pending != 0 || res.Cursor != 1 {
		t.Fatalf("after ack: %+v", res)
	}
}

func TestRelay_DeliveryTimeoutLeavesTheRecordPending(t *testing.T) {
	t.Setenv("XDG_STATE_HOME", t.TempDir())
	b := bus.New()
	release := make(chan struct{})
	t.Cleanup(func() { close(release); _ = b.Close(context.Background()) })
	b.SubscribeAsync("kit.core.consent.#", func(context.Context, bus.Event) { <-release })
	a, _ := NewFileAuditorFor("mytool")
	recordOne(t, a)
	r := NewRelay(a, b, WithDeliveryTimeout(20*time.Millisecond))
	err := waitFlush(t, flushAsync(context.Background(), r))
	if !errors.Is(err, bus.ErrNack) {
		t.Fatalf("err = %v, want a nack", err)
	}
	if c, _ := readCursorFile(r.CursorPath()); c.Seq != 0 {
		t.Fatalf("cursor moved to %d without an ack", c.Seq)
	}
}

// nackOnce confirms delivery, refusing the first publish.
type nackOnce struct {
	calls atomic.Int32
	keys  []string
}

func (p *nackOnce) Publish(context.Context, bus.Event) error {
	return errors.New("plain Publish used")
}

func (p *nackOnce) PublishAcked(_ context.Context, e bus.Event) (bus.Ack, error) {
	p.keys = append(p.keys, e.ID)
	if p.calls.Add(1) == 1 {
		return bus.Ack{ID: e.ID}, &bus.AckError{Peer: "ws://hub", Code: bus.AckTimeout}
	}
	return bus.Ack{ID: e.ID}, nil
}

func TestRelay_NackIsRetriedWithTheSameKey(t *testing.T) {
	t.Setenv("XDG_STATE_HOME", t.TempDir())
	a, _ := NewFileAuditorFor("mytool")
	recordOne(t, a)
	p := &nackOnce{}
	r := NewRelay(a, p)
	res, err := r.Flush(context.Background())
	if !errors.Is(err, bus.ErrNack) || res.Pending != 1 || res.Cursor != 0 {
		t.Fatalf("first flush %+v %v", res, err)
	}
	res, err = r.Flush(context.Background())
	if err != nil || res.Cursor != 1 || res.Pending != 0 {
		t.Fatalf("second flush %+v %v", res, err)
	}
	if len(p.keys) != 2 || p.keys[0] != p.keys[1] {
		t.Fatalf("keys %v, want the same key twice", p.keys)
	}
}

func TestRelay_CountsANetworkPeersAck(t *testing.T) {
	t.Setenv("XDG_STATE_HOME", t.TempDir())
	ctx := context.Background()
	hubBus := bus.New()
	hub := bus.NewNetworkAdapter(hubBus, bus.WithAuth(bus.OpenAuth()))
	srv := httptest.NewServer(hub.Handler())
	defer srv.Close()
	defer hub.Close()
	var vetoes atomic.Int32
	vetoes.Store(1)
	got := make(chan string, 4)
	hubBus.Subscribe("kit.core.consent.#", func(_ context.Context, e bus.Event) error {
		if vetoes.Add(-1) >= 0 {
			return errors.New("hub not ready")
		}
		got <- e.ID
		return nil
	})

	cl := bus.New(bus.WithNetwork("ws"+srv.URL[4:]),
		bus.WithNetworkOption(bus.WithAuth(bus.OpenAuth()), bus.WithAcks(), bus.WithBackoff(time.Millisecond, time.Millisecond)))
	defer cl.Close(ctx)
	a, _ := NewFileAuditorFor("mytool")
	recordOne(t, a)
	r := NewRelay(a, cl, WithDeliveryTimeout(relayWait))

	// The hub vetoes the first copy: the relay sees the peer's nack.
	err := waitFlush(t, flushAsync(ctx, r))
	var ae *bus.AckError
	if !errors.As(err, &ae) || ae.Code != bus.AckRejected {
		t.Fatalf("first flush err = %v, want the hub's rejection", err)
	}
	if err := waitFlush(t, flushAsync(ctx, r)); err != nil {
		t.Fatal(err)
	}
	select {
	case id := <-got:
		if c, _ := readCursorFile(r.CursorPath()); c.Seq != 1 || id == "" {
			t.Fatalf("cursor %d id %q", c.Seq, id)
		}
	case <-time.After(relayWait):
		t.Fatal("hub never accepted the record")
	}
}
