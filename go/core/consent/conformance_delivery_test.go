package consent

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"testing"

	"hop.top/kit/go/runtime/bus"
)

// Runner for contracts/consent-v1/delivery.json: the audit log as an
// outbox, delivered onto an in-process bus with a sync subscriber.

type deliveryStep struct {
	Op     string          `json:"op"`
	Record *deliveryRecord `json:"record"`
	// FailSeq makes the subscriber error on that seq (0: never).
	FailSeq *uint64         `json:"fail_seq"`
	Expect  *deliveryExpect `json:"expect"`
}

type deliveryRecord struct {
	Event          string `json:"event"`
	Capability     string `json:"capability"`
	Subject        string `json:"subject"`
	Decision       string `json:"decision"`
	Duration       string `json:"duration"`
	DecisionSource string `json:"decision_source"`
	PromptVersion  int    `json:"prompt_version"`
	Reason         string `json:"reason"`
}

type deliveryExpect struct {
	Seq       uint64   `json:"seq"`
	Delivered []uint64 `json:"delivered"`
	Cursor    *uint64  `json:"cursor"`
	Pending   *int     `json:"pending"`
	Error     bool     `json:"error"`
}

type published struct {
	Seq   uint64 `json:"seq"`
	Topic string `json:"topic"`
}

func TestConformanceConsentDelivery(t *testing.T) {
	var doc struct {
		Version     string `json:"version"`
		Description string `json:"description"`
		Scenarios   []struct {
			Name       string         `json:"name"`
			Now        string         `json:"now"`
			CursorJSON *string        `json:"cursor_json"`
			Steps      []deliveryStep `json:"steps"`
			Published  []published    `json:"published"`
			CursorFile *struct {
				Seq uint64 `json:"seq"`
			} `json:"cursor_file"`
		} `json:"scenarios"`
	}
	loadCorpus(t, "delivery.json", &doc)
	checkVersion(t, "delivery.json", doc.Version)
	for _, sc := range doc.Scenarios {
		t.Run(sc.Name, func(t *testing.T) {
			ctx := context.Background()
			t.Setenv("XDG_STATE_HOME", t.TempDir())
			at := mustTime(t, sc.Now)

			b := bus.New()
			defer b.Close(ctx)
			var failSeq uint64
			var got []published
			var keys []string
			b.Subscribe("kit.core.consent.#", func(_ context.Context, e bus.Event) error {
				rec := e.Payload.(AuditEvent)
				got = append(got, published{Seq: rec.Seq, Topic: string(e.Topic)})
				keys = append(keys, e.ID)
				if e.ID != rec.Key() || e.Source != "core.consent" {
					t.Errorf("event id %q source %q, want key %q", e.ID, e.Source, rec.Key())
				}
				if rec.Seq == failSeq {
					return fmt.Errorf("subscriber refuses seq %d", rec.Seq)
				}
				return nil
			})

			var a *FileAuditor
			var r *Relay
			crash := false
			open := func() {
				var err error
				if a, err = NewFileAuditorFor(corpusTool); err != nil {
					t.Fatal(err)
				}
				r = NewRelay(a, b)
				r.writeCursor = func(p string, c relayCursor) error {
					if crash {
						crash = false
						return errors.New("crash before ack")
					}
					return writeCursorFile(p, c)
				}
			}
			open()
			if sc.CursorJSON != nil {
				if err := os.MkdirAll(filepath.Dir(r.CursorPath()), 0o700); err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(r.CursorPath(), []byte(*sc.CursorJSON), 0o600); err != nil {
					t.Fatal(err)
				}
			}
			for i, st := range sc.Steps {
				where := fmt.Sprintf("step %d %s", i, st.Op)
				switch st.Op {
				case "record":
					rc := st.Record
					e := AuditEvent{
						Event: AuditKind(rc.Event), At: at, Capability: Capability(rc.Capability),
						Subject: rc.Subject, Decision: Effect(rc.Decision), Duration: Duration(rc.Duration),
						DecisionSource: DecisionSource(rc.DecisionSource), PromptVersion: rc.PromptVersion,
						Cause: RefusalCause(rc.Reason),
					}
					if err := a.Record(ctx, e); err != nil {
						t.Fatalf("%s: %v", where, err)
					}
					recs, _ := a.ReadAfter(st.Expect.Seq - 1)
					if len(recs) != 1 || recs[0].Seq != st.Expect.Seq {
						t.Fatalf("%s: want seq %d, log has %+v", where, st.Expect.Seq, recs)
					}
				case "subscriber":
					failSeq = *st.FailSeq
				case "crash_after_publish":
					crash = true
				case "restart":
					open()
				case "flush":
					res, err := r.Flush(ctx)
					ex := st.Expect
					if (err != nil) != ex.Error {
						t.Fatalf("%s: err=%v, want error=%v", where, err, ex.Error)
					}
					if res.Delivered == nil {
						res.Delivered = []uint64{}
					}
					if !reflect.DeepEqual(res.Delivered, ex.Delivered) || res.Cursor != *ex.Cursor || res.Pending != *ex.Pending {
						t.Fatalf("%s: got %+v, want delivered=%v cursor=%d pending=%d", where, res, ex.Delivered, *ex.Cursor, *ex.Pending)
					}
				default:
					t.Fatalf("unknown op %q", st.Op)
				}
			}
			if got == nil {
				got = []published{}
			}
			if !reflect.DeepEqual(got, sc.Published) {
				t.Fatalf("published %+v, want %+v", got, sc.Published)
			}
			if sc.CursorFile != nil {
				c, err := readCursorFile(r.CursorPath())
				if err != nil {
					t.Fatal(err)
				}
				logID, _ := a.LogID()
				if c.Seq != sc.CursorFile.Seq || c.Log != logID {
					t.Fatalf("cursor %+v, want seq %d log %s", c, sc.CursorFile.Seq, logID)
				}
				fi, _ := os.Stat(r.CursorPath())
				if fi.Mode().Perm() != 0o600 {
					t.Fatalf("cursor perm %o", fi.Mode().Perm())
				}
			}
		})
	}
}
