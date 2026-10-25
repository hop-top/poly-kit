package bus

import (
	"context"
	"encoding/json"
	"net/http/httptest"
	"reflect"
	"testing"
	"time"

	"github.com/coder/websocket"
)

func frameEvent() Event {
	return Event{
		ID:          "evt_01jg000000e008000000000000",
		Topic:       "kit.test.frame.sent",
		Source:      "A",
		Timestamp:   time.Date(2026, 10, 25, 12, 0, 0, 0, time.UTC),
		Payload:     map[string]any{"k": "v"},
		WorkspaceID: "ws1",
		// Set explicitly: CloudEvents frames stamp it on JSON data.
		DataContentType: "application/json",
		Extensions:      map[string]any{"tenantid": "t1"},
	}
}

func TestFrameFormat_String(t *testing.T) {
	for f, want := range map[FrameFormat]string{FrameKit: "kit", FrameCloudEvents: "cloudevents", 9: "FrameFormat(9)"} {
		if got := f.String(); got != want {
			t.Errorf("%d: %q, want %q", int(f), got, want)
		}
	}
}

func TestEncodeFrame_Shapes(t *testing.T) {
	cases := []struct {
		format  FrameFormat
		present []string
		absent  []string
	}{
		{FrameKit,
			[]string{"id", "topic", "source", "timestamp", "payload", "origin", "workspace_id"},
			[]string{"specversion", "extensions", "tenantid"}},
		{FrameCloudEvents,
			[]string{"specversion", "id", "source", "type", "time", "datacontenttype", "data", "workspaceid", "tenantid", OriginExtension},
			[]string{"topic", "payload", "origin", "workspace_id"}},
	}
	for _, tc := range cases {
		t.Run(tc.format.String(), func(t *testing.T) {
			raw, err := encodeFrame(tc.format, frameEvent(), "A")
			if err != nil {
				t.Fatal(err)
			}
			var m map[string]any
			if err := json.Unmarshal(raw, &m); err != nil {
				t.Fatal(err)
			}
			for _, k := range tc.present {
				if _, ok := m[k]; !ok {
					t.Errorf("missing %q in %s", k, raw)
				}
			}
			for _, k := range tc.absent {
				if _, ok := m[k]; ok {
					t.Errorf("unexpected %q in %s", k, raw)
				}
			}
		})
	}
}

func TestEncodeFrame_RejectsUserOrigin(t *testing.T) {
	e := frameEvent()
	e.Extensions = map[string]any{OriginExtension: "spoof"}
	if _, err := encodeFrame(FrameCloudEvents, e, "A"); err == nil {
		t.Fatal("want error for user-set kitorigin")
	}
}

func TestDecodeFrame_BothFormats(t *testing.T) {
	ceFrame, err := encodeFrame(FrameCloudEvents, frameEvent(), "A")
	if err != nil {
		t.Fatal(err)
	}
	e, origin, err := decodeFrame(ceFrame)
	if err != nil {
		t.Fatal(err)
	}
	if origin != "A" {
		t.Fatalf("origin = %q", origin)
	}
	if want := frameEvent(); !reflect.DeepEqual(e, want) {
		t.Fatalf("got  %#v\nwant %#v", e, want)
	}

	kitFrame, _ := encodeFrame(FrameKit, frameEvent(), "A")
	e, origin, err = decodeFrame(kitFrame)
	if err != nil || origin != "A" || e.WorkspaceID != "ws1" || e.ID != frameEvent().ID {
		t.Fatalf("kit frame: %v %q %#v", err, origin, e)
	}

	// The origin extension is stripped even when it is the only one.
	bare := Event{ID: "x", Topic: "kit.test.frame.sent", Source: "A"}
	raw, _ := encodeFrame(FrameCloudEvents, bare, "A")
	if e, _, _ := decodeFrame(raw); e.Extensions != nil {
		t.Fatalf("extensions = %#v, want nil", e.Extensions)
	}

	// Detection is by the exact key: a case-folded key is a kit frame.
	e, _, err = decodeFrame([]byte(`{"SPECVERSION":"1.0","topic":"kit.test.frame.sent","source":"s"}`))
	if err != nil || e.Topic != "kit.test.frame.sent" {
		t.Fatalf("case-folded specversion: %v %#v", err, e)
	}

	for _, bad := range []string{
		`not json`,
		`{"specversion":"0.3","id":"x","source":"s","type":"t"}`,
		`{"specversion":null}`,
		`{"specversion":"1.0","id":"x","source":"s","type":"t","kitorigin":7}`,
	} {
		if _, _, err := decodeFrame([]byte(bad)); err == nil {
			t.Errorf("%s: want error", bad)
		}
	}
}

// TestNetwork_MixedFrameFormats checks that a CloudEvents peer and a
// default kit peer interoperate in both directions.
func TestNetwork_MixedFrameFormats(t *testing.T) {
	busA := New()
	adapterA := NewNetworkAdapter(busA, WithOriginID("A"), WithFrameFormat(FrameCloudEvents))
	srv := httptest.NewServer(adapterA.Handler())
	defer srv.Close()
	defer adapterA.Close()

	busB := New()
	adapterB := NewNetworkAdapter(busB, WithOriginID("B"))
	defer adapterB.Close()
	if err := adapterB.Connect(context.Background(), "ws"+srv.URL[4:]); err != nil {
		t.Fatal(err)
	}
	waitConns(t, adapterA, 1)

	// A (CloudEvents) → B (kit default): every CloudEvents attribute
	// survives.
	gotB := recvOne(busB, "kit.test.frame.sent")
	sent := frameEvent()
	_ = busA.Publish(context.Background(), sent)
	if e := waitEvent(t, gotB); !reflect.DeepEqual(e, sent) {
		t.Fatalf("B got  %#v\nwant   %#v", e, sent)
	}

	// B (kit) → A (CloudEvents): the kit frame fields survive.
	gotA := recvOne(busA, "kit.test.frame.back")
	back := NewEvent("kit.test.frame.back", "B", "hi")
	back.WorkspaceID = "ws2"
	_ = busB.Publish(context.Background(), back)
	e := waitEvent(t, gotA)
	if e.WorkspaceID != "ws2" || e.Payload != "hi" || e.Source != "B" || e.ID != back.ID {
		t.Fatalf("A got %#v", e)
	}
}

// TestNetwork_RelayCloudEvents checks a relay hub re-forwards a
// CloudEvents frame with the publisher's origin preserved in kitorigin.
func TestNetwork_RelayCloudEvents(t *testing.T) {
	hub := New()
	hubAdapter := NewNetworkAdapter(hub, WithOriginID("H"), WithRelay(true), WithFrameFormat(FrameCloudEvents))
	defer hubAdapter.Close()
	srv := httptest.NewServer(hubAdapter.Handler())
	defer srv.Close()

	// C is a raw peer so the relayed frame itself can be inspected.
	c, _, err := websocket.Dial(context.Background(), "ws"+srv.URL[4:], nil)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = c.Close(websocket.StatusNormalClosure, "") }()
	waitConns(t, hubAdapter, 1)

	busB := New()
	adapterB := NewNetworkAdapter(busB, WithOriginID("B"), WithFrameFormat(FrameCloudEvents))
	defer adapterB.Close()
	if err := adapterB.Connect(context.Background(), "ws"+srv.URL[4:]); err != nil {
		t.Fatal(err)
	}
	waitConns(t, hubAdapter, 2)

	sent := frameEvent()
	_ = busB.Publish(context.Background(), sent)
	frame := readFrame(t, c)
	if frame[OriginExtension] != "B" || frame["id"] != sent.ID || frame["tenantid"] != "t1" || frame["workspaceid"] != "ws1" {
		t.Fatalf("relayed frame = %v", frame)
	}
}

func TestNetwork_CloudEventsFrameFromForeignPeer(t *testing.T) {
	b := New()
	n := NewNetworkAdapter(b, WithOriginID("A"))
	defer n.Close()
	got := recvOne(b, "acme.core.stream.reset")
	conn := dialRaw(t, n)

	write := func(s string) {
		if err := conn.Write(context.Background(), websocket.MessageText, []byte(s)); err != nil {
			t.Fatal(err)
		}
	}
	// Invalid CloudEvents frames are dropped; the connection survives.
	write(`{"specversion":"1.0","id":"x","source":"s","type":"acme.core.stream.reset","bad_name":1}`)
	write(`{"specversion":"1.0","id":"x","source":"s","type":"acme.core.stream.reset","kitorigin":1}`)
	// Our own origin is dropped (loop prevention).
	write(`{"specversion":"1.0","id":"x","source":"s","type":"acme.core.stream.reset","kitorigin":"A"}`)
	write(`{"specversion":"1.0","id":"ce-1","source":"/acme/core","type":"acme.core.stream.reset","tenantid":"t1","seqoffset":42}`)

	e := waitEvent(t, got)
	want := map[string]any{"tenantid": "t1", "seqoffset": int64(42)}
	if e.ID != "ce-1" || !reflect.DeepEqual(e.Extensions, want) || e.Payload != nil {
		t.Fatalf("got %#v", e)
	}
}
