package bus

import (
	"encoding/json"
	"reflect"
	"testing"
)

func TestStreamConformanceAcks(t *testing.T) {
	type resolved struct {
		Event string `json:"event"`
		Code  string `json:"code"`
	}
	var f struct {
		ceConfHeader
		Auth []struct {
			Name   string `json:"name"`
			Frame  string `json:"frame"`
			OK     bool   `json:"ok"`
			Acks   int    `json:"acks"`
			Stream string `json:"stream"`
			Error  string `json:"error"`
		} `json:"auth"`
		Negotiate []struct {
			Name string `json:"name"`
			Mine int    `json:"mine"`
			Peer *struct {
				Acks int `json:"acks"`
			} `json:"peer"`
			StreamLink bool `json:"stream_link"`
			Acks       bool `json:"acks"`
		} `json:"negotiate"`
		Puback []struct {
			Name    string `json:"name"`
			Frame   string `json:"frame"`
			OK      bool   `json:"ok"`
			Offset  string `json:"offset"`
			ID      string `json:"id"`
			Status  string `json:"status"`
			Code    string `json:"code"`
			Message string `json:"message"`
			Error   string `json:"error"`
		} `json:"puback"`
		PubackEncode []struct {
			Name    string          `json:"name"`
			Offset  string          `json:"offset"`
			ID      string          `json:"id"`
			Status  string          `json:"status"`
			Code    string          `json:"code"`
			Message string          `json:"message"`
			Frame   json.RawMessage `json:"frame"`
		} `json:"puback_encode"`
		Dialer []struct {
			Name     string `json:"name"`
			Capacity int    `json:"capacity"`
			Ops      []struct {
				Publish string `json:"publish"`
				Acked   bool   `json:"acked"`
				Connect *struct {
					Acks bool `json:"acks"`
				} `json:"connect"`
				Disconnect bool   `json:"disconnect"`
				Puback     string `json:"puback"`
				Fail       string `json:"fail"`
			} `json:"ops"`
			Sent     []string   `json:"sent"`
			Resolved []resolved `json:"resolved"`
			Error    string     `json:"error"`
		} `json:"dialer"`
	}
	streamConfLoad(t, "acks.json", &f)
	if len(f.Auth) == 0 || len(f.Negotiate) == 0 || len(f.Puback) == 0 || len(f.PubackEncode) == 0 || len(f.Dialer) == 0 {
		t.Fatal("acks.json: every section needs cases")
	}

	for _, tc := range f.Auth {
		t.Run("auth/"+tc.Name, func(t *testing.T) {
			a, err := parseAuthFrame([]byte(tc.Frame))
			if !tc.OK {
				if tc.Error == "" || err == nil || err.Code != tc.Error {
					t.Fatalf("err = %v, want %q", err, tc.Error)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			acks := 0
			if a.Caps != nil {
				acks = a.Caps.Acks
			}
			if acks != tc.Acks || a.Stream != tc.Stream {
				t.Fatalf("acks %d stream %q, want %d %q", acks, a.Stream, tc.Acks, tc.Stream)
			}
		})
	}

	for _, tc := range f.Negotiate {
		t.Run("negotiate/"+tc.Name, func(t *testing.T) {
			var peer *streamCaps
			if tc.Peer != nil {
				peer = &streamCaps{Streams: 1, Acks: tc.Peer.Acks}
			}
			if got := negotiateAcks(tc.Mine, peer, tc.StreamLink); got != tc.Acks {
				t.Fatalf("acks = %v", got)
			}
		})
	}

	for _, tc := range f.Puback {
		t.Run("puback/"+tc.Name, func(t *testing.T) {
			kind, members := classifyFrame([]byte(tc.Frame), true)
			if kind != frameKindControl {
				t.Fatalf("classified %s", kind)
			}
			p, err := parsePuback(members)
			if !tc.OK {
				if tc.Error == "" || protocolKind(err) != tc.Error {
					t.Fatalf("err = %v, want kind %q", err, tc.Error)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			want := pubackInfo{Offset: mustOffset(t, tc.Offset), ID: tc.ID, Status: tc.Status, Code: tc.Code, Message: tc.Message}
			if p != want {
				t.Fatalf("puback = %+v, want %+v", p, want)
			}
		})
	}

	for _, tc := range f.PubackEncode {
		t.Run("puback_encode/"+tc.Name, func(t *testing.T) {
			got := encodePuback(pubackInfo{Offset: mustOffset(t, tc.Offset), ID: tc.ID, Status: tc.Status, Code: tc.Code, Message: tc.Message})
			assertJSONEqual(t, got, tc.Frame)
		})
	}

	for _, tc := range f.Dialer {
		t.Run("dialer/"+tc.Name, func(t *testing.T) {
			log := NewRingLog("stm_dialer", tc.Capacity)
			cur := newSendCursor(log, true)
			tr := newAckTracker(cur.at)
			var (
				connected, acks bool
				sent            = []string{}
				got             = []resolved{}
				perr            string
			)
			record := func(rs ...ackResolution) {
				for _, r := range rs {
					if r.T == nil || r.T.resolvedFor("peer") {
						continue
					}
					r.T.resolve("peer", r.Code, r.Message)
					got = append(got, resolved{r.T.id, r.Code})
				}
			}
			send := func() {
				for {
					before := cur.at
					entries, gap := cur.next(log, writerBatch)
					if len(entries) == 0 {
						return
					}
					if gap > 0 {
						record(tr.onGap(before, entries[0].Offset)...)
					}
					for _, e := range entries {
						if r := tr.onSend(e.Offset, e.ticket, acks); r != nil {
							record(*r)
						}
						sent = append(sent, string(e.Event.Topic)+"@"+formatOffset(e.Offset))
					}
				}
			}
		ops:
			for i, op := range tc.Ops {
				switch {
				case op.Publish != "":
					var tk *ackTicket
					if op.Acked {
						tk = newAckTicket(op.Publish, true)
						tk.expect("peer", nil)
					}
					off := log.Append(LogEntry{Event: Event{ID: op.Publish, Topic: Topic(op.Publish)}, ticket: tk})
					if tk != nil {
						tr.register(off, tk)
					}
					if connected {
						send()
					}
				case op.Connect != nil:
					connected, acks = true, op.Connect.Acks
					record(tr.linkUp(acks, cur)...)
					send()
				case op.Disconnect:
					connected = false
				case op.Puback != "":
					_, members := classifyFrame([]byte(op.Puback), true)
					p, err := parsePuback(members)
					var rs []ackResolution
					if err == nil {
						rs, err = tr.onPuback(p)
					}
					if err != nil {
						perr = protocolKind(err)
						if perr == "" {
							t.Fatalf("op %d: %v", i, err)
						}
						break ops
					}
					record(rs...)
				case op.Fail != "":
					record(tr.failAll(op.Fail)...)
				default:
					t.Fatalf("op %d is empty", i)
				}
			}
			want := tc.Resolved
			if want == nil {
				want = []resolved{}
			}
			if !reflect.DeepEqual(sent, tc.Sent) || !reflect.DeepEqual(got, want) || perr != tc.Error {
				t.Fatalf("sent %v resolved %v error %q\nwant %v %v %q", sent, got, perr, tc.Sent, want, tc.Error)
			}
		})
	}
}
