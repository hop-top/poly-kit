package bus_test

import (
	"context"
	"encoding/json"
	"sync"
	"testing"

	"hop.top/kit/go/runtime/bus"
)

// payload with an anonymous embed of bus.Qualifiers
type anonEmbedPayload struct {
	bus.Qualifiers
	SnapshotID string `json:"snapshot_id"`
}

// payload with a named field of type bus.Qualifiers
type namedEmbedPayload struct {
	Q          bus.Qualifiers `json:"qualifiers"`
	SnapshotID string         `json:"snapshot_id"`
}

// payload without any qualifiers field
type plainPayload struct {
	SnapshotID string `json:"snapshot_id"`
}

func TestQualifiers_IsZero(t *testing.T) {
	var q bus.Qualifiers
	if !q.IsZero() {
		t.Errorf("zero Qualifiers should report IsZero=true")
	}
	q.Reason = "sighup"
	if q.IsZero() {
		t.Errorf("Qualifiers with Reason set should report IsZero=false")
	}
}

func TestQualifiers_EmptyMarshalsToEmptyObject(t *testing.T) {
	var q bus.Qualifiers
	raw, err := json.Marshal(q)
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}
	if string(raw) != "{}" {
		t.Errorf("empty Qualifiers JSON: got %s want {}", string(raw))
	}
}

func TestQualifiers_PopulatedMarshalsAllFields(t *testing.T) {
	q := bus.Qualifiers{
		Reason:       "sighup",
		Mechanism:    "signal",
		Property:     "snapshot_path",
		Circumstance: "during_reload",
	}
	raw, err := json.Marshal(q)
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}
	var back bus.Qualifiers
	if err := json.Unmarshal(raw, &back); err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}
	if back != q {
		t.Errorf("round-trip: got %+v want %+v", back, q)
	}
}

func TestQualifiersFrom_AnonymousEmbed(t *testing.T) {
	p := anonEmbedPayload{
		Qualifiers: bus.Qualifiers{Reason: "sighup", Mechanism: "signal"},
		SnapshotID: "abc",
	}
	q, ok := bus.QualifiersFrom(p)
	if !ok {
		t.Fatalf("QualifiersFrom anonymous: ok=false")
	}
	if q.Reason != "sighup" || q.Mechanism != "signal" {
		t.Errorf("got %+v", q)
	}
}

func TestQualifiersFrom_NamedField(t *testing.T) {
	p := namedEmbedPayload{
		Q:          bus.Qualifiers{Reason: "manual"},
		SnapshotID: "abc",
	}
	q, ok := bus.QualifiersFrom(p)
	if !ok {
		t.Fatalf("QualifiersFrom named: ok=false")
	}
	if q.Reason != "manual" {
		t.Errorf("got %+v", q)
	}
}

func TestQualifiersFrom_PointerPayload(t *testing.T) {
	p := &anonEmbedPayload{
		Qualifiers: bus.Qualifiers{Reason: "ptr"},
		SnapshotID: "x",
	}
	q, ok := bus.QualifiersFrom(p)
	if !ok {
		t.Fatalf("QualifiersFrom pointer: ok=false")
	}
	if q.Reason != "ptr" {
		t.Errorf("got %+v", q)
	}
}

func TestQualifiersFrom_NoEmbedReturnsFalse(t *testing.T) {
	p := plainPayload{SnapshotID: "abc"}
	q, ok := bus.QualifiersFrom(p)
	if ok {
		t.Fatalf("QualifiersFrom no-embed: ok=true (want false), got %+v", q)
	}
}

func TestQualifiersFrom_NilPayloadReturnsFalse(t *testing.T) {
	q, ok := bus.QualifiersFrom(nil)
	if ok {
		t.Fatalf("QualifiersFrom nil: ok=true (want false), got %+v", q)
	}
}

func TestQualifiersFrom_NilPointerReturnsFalse(t *testing.T) {
	var p *anonEmbedPayload
	q, ok := bus.QualifiersFrom(p)
	if ok {
		t.Fatalf("QualifiersFrom typed-nil ptr: ok=true (want false), got %+v", q)
	}
}

func TestQualifiersFrom_NonStructReturnsFalse(t *testing.T) {
	for _, in := range []any{
		"string",
		42,
		[]string{"a"},
		map[string]string{"k": "v"},
	} {
		q, ok := bus.QualifiersFrom(in)
		if ok {
			t.Errorf("QualifiersFrom(%T): ok=true (want false), got %+v", in, q)
		}
	}
}

func TestQualifiers_PublishSubscribeRoundTrip(t *testing.T) {
	// In-process round-trip via the in-memory adapter. The
	// publisher passes a struct value with Qualifiers embedded;
	// the subscriber gets the same Go value (no JSON hop) and
	// extracts via QualifiersFrom.
	b := bus.New()
	defer b.Close(context.Background())

	topic := bus.TopicOf("kit", "config", "snapshot").Mod("reload").Action("failed")

	var (
		gotQ  bus.Qualifiers
		gotOK bool
		mu    sync.Mutex
		wg    sync.WaitGroup
	)
	wg.Add(1)
	b.Subscribe(string(topic), func(ctx context.Context, e bus.Event) error {
		mu.Lock()
		defer mu.Unlock()
		gotQ, gotOK = bus.QualifiersFrom(e.Payload)
		wg.Done()
		return nil
	})

	payload := anonEmbedPayload{
		Qualifiers: bus.Qualifiers{
			Reason:    "sighup",
			Mechanism: "signal",
		},
		SnapshotID: "snap-1",
	}
	if err := b.Publish(context.Background(), bus.NewEvent(topic, "test", payload)); err != nil {
		t.Fatalf("Publish: %v", err)
	}
	wg.Wait()
	mu.Lock()
	defer mu.Unlock()
	if !gotOK {
		t.Fatalf("subscriber failed to extract Qualifiers")
	}
	if gotQ.Reason != "sighup" || gotQ.Mechanism != "signal" {
		t.Errorf("Qualifiers round-trip: got %+v", gotQ)
	}
}
