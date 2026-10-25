package bus

import (
	"context"
	"encoding/json"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/coder/websocket"
)

// recvOne subscribes to topic on b and returns a channel that yields the
// first matching event.
func recvOne(b Bus, topic string) <-chan Event {
	ch := make(chan Event, 1)
	b.Subscribe(topic, func(_ context.Context, e Event) error {
		select {
		case ch <- e:
		default:
		}
		return nil
	})
	return ch
}

func waitEvent(t *testing.T, ch <-chan Event) Event {
	t.Helper()
	select {
	case e := <-ch:
		return e
	case <-time.After(2 * time.Second):
		t.Fatal("event not delivered")
		return Event{}
	}
}

// dialRaw opens a bare WebSocket client against an adapter handler, to
// speak frames the way an older or foreign peer would.
func dialRaw(t *testing.T, n *NetworkAdapter) *websocket.Conn {
	t.Helper()
	srv := httptest.NewServer(n.Handler())
	t.Cleanup(srv.Close)
	conn, _, err := websocket.Dial(context.Background(), "ws"+srv.URL[4:], nil)
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	t.Cleanup(func() { _ = conn.Close(websocket.StatusNormalClosure, "") })
	waitConns(t, n, 1)
	return conn
}

// waitConns blocks until n has registered want peer connections; the
// accept handler registers them asynchronously after the dial returns.
func waitConns(t *testing.T, n *NetworkAdapter, want int) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for {
		n.mu.RLock()
		got := len(n.conns)
		n.mu.RUnlock()
		if got >= want {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("adapter has %d conns, want %d", got, want)
		}
		time.Sleep(time.Millisecond)
	}
}

func readFrame(t *testing.T, conn *websocket.Conn) map[string]any {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	_, data, err := conn.Read(ctx)
	if err != nil {
		t.Fatalf("read frame: %v", err)
	}
	var m map[string]any
	if err := json.Unmarshal(data, &m); err != nil {
		t.Fatalf("frame is not JSON: %v", err)
	}
	return m
}

func TestNetwork_WorkspaceIDSurvivesWire(t *testing.T) {
	busA := New()
	adapterA := NewNetworkAdapter(busA, WithOriginID("A"))
	srv := httptest.NewServer(adapterA.Handler())
	defer srv.Close()
	defer adapterA.Close()

	busB := New()
	adapterB := NewNetworkAdapter(busB, WithOriginID("B"))
	defer adapterB.Close()
	if err := adapterB.Connect(context.Background(), "ws"+srv.URL[4:]); err != nil {
		t.Fatalf("connect: %v", err)
	}
	waitConns(t, adapterA, 1)

	got := recvOne(busB, "kit.test.workspace.scoped")
	e := NewEvent("kit.test.workspace.scoped", "A", nil)
	e.WorkspaceID = "01jg000000e008000000000000"
	_ = busA.Publish(context.Background(), e)

	if w := waitEvent(t, got).WorkspaceID; w != e.WorkspaceID {
		t.Fatalf("WorkspaceID = %q, want %q", w, e.WorkspaceID)
	}
}

func TestNetwork_V1FrameEmitsWorkspaceID(t *testing.T) {
	b := New()
	n := NewNetworkAdapter(b, WithOriginID("A"))
	defer n.Close()
	conn := dialRaw(t, n)

	scoped := NewEvent("kit.test.workspace.scoped", "A", nil)
	scoped.WorkspaceID = "ws1"
	_ = b.Publish(context.Background(), scoped)
	frame := readFrame(t, conn)
	if got := frame["workspace_id"]; got != "ws1" {
		t.Fatalf("frame workspace_id = %v, want ws1", got)
	}
	if got := frame["id"]; got != scoped.ID {
		t.Fatalf("frame id = %v, want %s", got, scoped.ID)
	}

	// Global events keep the old frame shape: no workspace_id key; an
	// event without an id carries no id key.
	_ = b.Publish(context.Background(), Event{Topic: "kit.test.workspace.global", Source: "A"})
	frame = readFrame(t, conn)
	for _, k := range []string{"workspace_id", "id"} {
		if got, ok := frame[k]; ok {
			t.Fatalf("frame carries %s = %v, want key absent", k, got)
		}
	}
}

func TestNetwork_V1FrameAcceptsWorkspaceID(t *testing.T) {
	cases := []struct {
		name  string
		frame string
		want  string
		id    string
	}{
		{
			name:  "old frame without workspace_id",
			frame: `{"topic":"kit.test.frame.sent","source":"old","timestamp":"2026-01-01T00:00:00Z","payload":{"k":"v"},"origin":"old"}`,
			want:  "",
		},
		{
			name:  "frame with workspace_id",
			frame: `{"topic":"kit.test.frame.sent","source":"new","timestamp":"2026-01-01T00:00:00Z","payload":null,"origin":"new","workspace_id":"ws1","id":"evt_01jg000000e008000000000000"}`,
			want:  "ws1",
			id:    "evt_01jg000000e008000000000000",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			b := New()
			n := NewNetworkAdapter(b, WithOriginID("A"))
			defer n.Close()
			got := recvOne(b, "kit.test.frame.sent")
			conn := dialRaw(t, n)
			if err := conn.Write(context.Background(), websocket.MessageText, []byte(tc.frame)); err != nil {
				t.Fatalf("write: %v", err)
			}
			e := waitEvent(t, got)
			if e.WorkspaceID != tc.want || e.ID != tc.id {
				t.Fatalf("WorkspaceID, ID = %q, %q; want %q, %q", e.WorkspaceID, e.ID, tc.want, tc.id)
			}
			if !e.Timestamp.Equal(time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)) {
				t.Fatalf("Timestamp = %v", e.Timestamp)
			}
		})
	}
}
