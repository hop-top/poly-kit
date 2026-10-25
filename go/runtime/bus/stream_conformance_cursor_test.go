package bus

import (
	"encoding/json"
	"reflect"
	"testing"
)

type confGap struct {
	Reason string `json:"reason"`
	Stream string `json:"stream"`
	After  string `json:"after"`
	Head   string `json:"head"`
}

func confGapOf(g *gapInfo) *confGap {
	if g == nil {
		return nil
	}
	p := g.payload("")
	return &confGap{Reason: p["reason"].(string), Stream: p["stream"].(string), After: p["after"].(string), Head: p["head"].(string)}
}

type confPos struct {
	Stream string `json:"stream"`
	Offset string `json:"offset"`
}

func TestStreamConformanceCursor(t *testing.T) {
	var f struct {
		ceConfHeader
		Cases []struct {
			Name      string           `json:"name"`
			Seed      *confPos         `json:"seed"`
			Reconnect bool             `json:"reconnect"`
			Link      string           `json:"link"`
			Ack       *json.RawMessage `json:"ack"`
			Frames    []struct {
				Event    json.RawMessage `json:"event"`
				Reset    json.RawMessage `json:"reset"`
				Position json.RawMessage `json:"position"`
			} `json:"frames"`
			Expect struct {
				LinkUp   json.RawMessage   `json:"link_up"`
				Frames   []json.RawMessage `json:"frames"`
				Position *confPos          `json:"position"`
			} `json:"expect"`
		} `json:"cases"`
	}
	streamConfLoad(t, "cursor.json", &f)
	for _, tc := range f.Cases {
		t.Run(tc.Name, func(t *testing.T) {
			var c dialerCursor
			if tc.Seed != nil {
				c = dialerCursor{Stream: tc.Seed.Stream, Offset: mustOffset(t, tc.Seed.Offset), Held: true}
			}
			var ack *ackFrame
			switch tc.Link {
			case "stream":
				// The vector's ack is the reply body minus type/caps; wrap it
				// into a real extended ack and decode it.
				var body map[string]json.RawMessage
				if err := json.Unmarshal(*tc.Ack, &body); err != nil {
					t.Fatal(err)
				}
				var si map[string]any
				_ = json.Unmarshal(body["stream"], &si)
				si["heartbeat_ms"] = 1000
				frame := map[string]any{"type": "auth_ok", "caps": map[string]any{"streams": 1, "frames": []string{"kit"}}, "stream": si}
				if r, ok := body["resume"]; ok {
					frame["session"] = map[string]json.RawMessage{"resume": r}
				}
				raw, _ := json.Marshal(frame)
				a, err := parseAck(raw, StreamsVersion)
				if err != nil {
					t.Fatalf("ack: %v", err)
				}
				ack = &a
			case "legacy":
			default:
				t.Fatalf("vector link %q", tc.Link)
			}
			resume, gap, err := c.linkUp(ack, tc.Reconnect)
			var linkUp any = map[string]any{"resume": resume, "invalidated": confGapOf(gap)}
			if err != nil {
				if !isProtocolError(err) {
					t.Fatalf("link-up err = %v, want a protocol error", err)
				}
				linkUp = "protocol_error"
			}
			lu, _ := json.Marshal(linkUp)
			assertJSONEqual(t, lu, tc.Expect.LinkUp)
			if len(tc.Frames) != len(tc.Expect.Frames) {
				t.Fatal("vector: frames and expectations differ in length")
			}
			for i, fr := range tc.Frames {
				var got any
				switch {
				case len(fr.Event) > 0:
					deliver, err := c.onEventOffset(fr.Event)
					switch {
					case err != nil:
						got = "protocol_error"
					case deliver:
						got = "deliver"
					default:
						got = "drop"
					}
				case len(fr.Reset) > 0:
					members := map[string]json.RawMessage{}
					_ = json.Unmarshal(fr.Reset, &members)
					members["type"] = json.RawMessage(`"reset"`)
					dc, err := dialerControl(members)
					if err == nil {
						var g *gapInfo
						g, err = c.onReset(dc.Reset)
						if err == nil {
							got = map[string]any{"invalidated": confGapOf(g)}
						}
					}
					if err != nil {
						got = "protocol_error"
					}
				case len(fr.Position) > 0:
					members := map[string]json.RawMessage{}
					_ = json.Unmarshal(fr.Position, &members)
					members["type"] = json.RawMessage(`"position"`)
					dc, err := dialerControl(members)
					if err == nil {
						err = c.onPosition(dc.Position)
					}
					got = "ok"
					if err != nil {
						got = "protocol_error"
					}
				default:
					t.Fatal("vector: empty frame")
				}
				gotJSON, _ := json.Marshal(got)
				assertJSONEqual(t, gotJSON, tc.Expect.Frames[i])
			}
			var pos *confPos
			if c.Held {
				pos = &confPos{Stream: c.Stream, Offset: formatOffset(c.Offset)}
			}
			if !reflect.DeepEqual(pos, tc.Expect.Position) {
				t.Errorf("position\n got  %+v\n want %+v", pos, tc.Expect.Position)
			}
		})
	}
}

func TestStreamConformanceInbound(t *testing.T) {
	var f struct {
		ceConfHeader
		Cases []struct {
			Name     string `json:"name"`
			After    string `json:"after"`
			Capacity int    `json:"capacity"`
			Ops      []struct {
				Recv    *string `json:"recv"`
				Deliver *int    `json:"deliver"`
			} `json:"ops"`
			Delivered   []string `json:"delivered"`
			Overflow    bool     `json:"overflow"`
			ResumeAfter string   `json:"resume_after"`
		} `json:"cases"`
	}
	streamConfLoad(t, "inbound.json", &f)
	for _, tc := range f.Cases {
		t.Run(tc.Name, func(t *testing.T) {
			q := newInboundQueue(tc.Capacity, mustOffset(t, tc.After))
			delivered := []string{}
			for _, op := range tc.Ops {
				switch {
				case op.Recv != nil:
					q.recv(mustOffset(t, *op.Recv))
				case op.Deliver != nil:
					for _, o := range q.deliver(*op.Deliver) {
						delivered = append(delivered, formatOffset(o))
					}
				}
			}
			if !reflect.DeepEqual(delivered, tc.Delivered) {
				t.Errorf("delivered = %v, want %v", delivered, tc.Delivered)
			}
			if q.overflowed != tc.Overflow {
				t.Errorf("overflow = %v, want %v", q.overflowed, tc.Overflow)
			}
			if formatOffset(q.resumeAfter()) != tc.ResumeAfter {
				t.Errorf("resume after = %d, want %s", q.resumeAfter(), tc.ResumeAfter)
			}
		})
	}
}

func TestStreamConformanceOutbox(t *testing.T) {
	var f struct {
		ceConfHeader
		Cases []struct {
			Name     string `json:"name"`
			Capacity int    `json:"capacity"`
			Outbox   bool   `json:"outbox"`
			Ops      []struct {
				Publish    *string `json:"publish"`
				Connect    bool    `json:"connect"`
				Disconnect bool    `json:"disconnect"`
			} `json:"ops"`
			Sent    []string `json:"sent"`
			Dropped uint64   `json:"dropped"`
		} `json:"cases"`
	}
	streamConfLoad(t, "outbox.json", &f)
	for _, tc := range f.Cases {
		t.Run(tc.Name, func(t *testing.T) {
			log := NewRingLog("S", tc.Capacity)
			ob := newSendCursor(log, tc.Outbox)
			sent := []string{}
			var dropped uint64
			connected := false
			drain := func() {
				entries, gap := ob.next(log, 1<<20)
				dropped += gap
				for _, e := range entries {
					sent = append(sent, string(e.Event.Topic))
				}
			}
			for _, op := range tc.Ops {
				switch {
				case op.Publish != nil:
					log.Append(LogEntry{Event: Event{Topic: Topic(*op.Publish)}})
					if connected {
						drain()
					}
				case op.Connect:
					connected = true
					ob.linkUp(log)
					drain()
				case op.Disconnect:
					connected = false
				}
			}
			if !reflect.DeepEqual(sent, tc.Sent) {
				t.Errorf("sent = %v, want %v", sent, tc.Sent)
			}
			if dropped != tc.Dropped {
				t.Errorf("dropped = %d, want %d", dropped, tc.Dropped)
			}
		})
	}
}
