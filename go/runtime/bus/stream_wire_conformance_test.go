package bus

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
)

// streamWireFiles lists the transcripts under streams/wire with a runner.
var streamWireFiles = map[string]bool{
	"plain_ack.jsonl": true,
	"replay.jsonl":    true,
	"reset_ack.jsonl": true,
	"subscribe.jsonl": true,
}

// wireStep is one transcript line after the header: a frame the dialer
// sends, a frame it must receive next (compared as a JSON value, plus
// exact bytes when given; a missing "message" is not compared), or an
// event the acceptor publishes.
type wireStep struct {
	Send    json.RawMessage `json:"send"`
	Recv    json.RawMessage `json:"recv"`
	Bytes   *string         `json:"bytes"`
	Publish *ceConfEvent    `json:"publish"`
}

// TestStreamConformanceWire replays each transcript against a Go
// acceptor (stream id "stm_wire", origin "hub", OpenAuth) over a real
// WebSocket.
func TestStreamConformanceWire(t *testing.T) {
	for name := range streamWireFiles {
		t.Run(name, func(t *testing.T) {
			f, err := os.Open(filepath.Join(filepath.FromSlash(streamConfDir), "wire", name))
			if err != nil {
				t.Fatal(err)
			}
			defer f.Close()
			sc := bufio.NewScanner(f)
			sc.Buffer(make([]byte, 1<<20), 1<<20)
			if !sc.Scan() {
				t.Fatal("empty transcript")
			}
			var head struct {
				Version     string `json:"version"`
				Description string `json:"description"`
				Setup       struct {
					LogSize int           `json:"log_size"`
					Publish []ceConfEvent `json:"publish"`
				} `json:"setup"`
			}
			strictJSON(t, sc.Bytes(), &head)
			if head.Version != "v1" || head.Description == "" {
				t.Fatal("header needs version v1 and a description")
			}
			b := New()
			n := NewNetworkAdapter(b, WithAuth(OpenAuth()), WithOriginID("hub"),
				WithLog(NewRingLog("stm_wire", head.Setup.LogSize)))
			srv := httptest.NewServer(n.Handler())
			defer srv.Close()
			defer n.Close()
			for _, e := range head.Setup.Publish {
				if err := b.Publish(context.Background(), e.event(t)); err != nil {
					t.Fatal(err)
				}
			}
			c := rawDial(t, "ws"+srv.URL[4:], "")
			for line := 2; sc.Scan(); line++ {
				var st wireStep
				strictJSON(t, sc.Bytes(), &st)
				switch {
				case st.Send != nil:
					rawSend(t, c, string(st.Send))
				case st.Recv != nil:
					got := rawRead(t, c)
					if st.Bytes != nil && string(got) != *st.Bytes {
						t.Fatalf("line %d: bytes %s, want %s", line, got, *st.Bytes)
					}
					var want, have map[string]any
					_ = json.Unmarshal(st.Recv, &want)
					_ = json.Unmarshal(got, &have)
					if _, ok := want["message"]; !ok {
						delete(have, "message")
					}
					g, _ := json.Marshal(have)
					assertJSONEqual(t, g, st.Recv)
				case st.Publish != nil:
					if err := b.Publish(context.Background(), st.Publish.event(t)); err != nil {
						t.Fatal(err)
					}
				default:
					t.Fatalf("line %d: empty step", line)
				}
			}
			ctx, cancel := context.WithTimeout(context.Background(), failAfter/10)
			defer cancel()
			if _, extra, err := c.Read(ctx); err == nil {
				t.Fatalf("unexpected trailing frame %s", extra)
			} else if !errors.Is(err, context.DeadlineExceeded) {
				t.Fatalf("link closed: %v", err)
			}
		})
	}
}

func strictJSON(t *testing.T, data []byte, v any) {
	t.Helper()
	dec := json.NewDecoder(bytes.NewReader(data))
	dec.DisallowUnknownFields()
	if err := dec.Decode(v); err != nil {
		t.Fatalf("transcript line %s: %v", data, err)
	}
}
