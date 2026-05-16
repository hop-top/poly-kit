package main

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"sync"
	"testing"

	"hop.top/kit/engine/store"
	"hop.top/kit/go/runtime/bus"
	"hop.top/kit/go/transport/api"
)

// captureEvents subscribes a recorder to the document.# pattern and
// returns a snapshot accessor. Subscribers are SubscribeAsync so the
// test mirrors the production fire-and-forget convention; the bus is
// drained via Close before reading captured events.
type captured struct {
	topic   bus.Topic
	source  string
	payload DocumentEventPayload
}

func newRecorder(b bus.Bus) func() []captured {
	var (
		mu     sync.Mutex
		events []captured
	)
	b.SubscribeAsync("kit.engine.document.#", func(_ context.Context, e bus.Event) {
		var p DocumentEventPayload
		// Round-trip through JSON to mimic what wire consumers will see.
		raw, err := json.Marshal(e.Payload)
		if err == nil {
			_ = json.Unmarshal(raw, &p)
		}
		mu.Lock()
		events = append(events, captured{topic: e.Topic, source: e.Source, payload: p})
		mu.Unlock()
	})
	return func() []captured {
		mu.Lock()
		defer mu.Unlock()
		out := make([]captured, len(events))
		copy(out, events)
		return out
	}
}

func newTestEngine(t *testing.T) (*api.Router, *store.VersionedDocumentStore, bus.Bus, func() []captured) {
	t.Helper()

	ds, err := store.NewDocumentStore(filepath.Join(t.TempDir(), "documents.db"))
	if err != nil {
		t.Fatalf("new store: %v", err)
	}
	t.Cleanup(func() { _ = ds.Close() })

	vstore := store.NewInMemoryVersionStore()
	vds := store.NewVersionedDocumentStore(ds, vstore)

	b := bus.New()
	t.Cleanup(func() { _ = b.Close(context.Background()) })

	snapshot := newRecorder(b)

	router := api.NewRouter()
	registerDocumentRoutes(router, vds, b)
	return router, vds, b, snapshot
}

func TestRegisterDocumentRoutes_EmitsCreated(t *testing.T) {
	router, _, b, events := newTestEngine(t)

	srv := httptest.NewServer(router)
	defer srv.Close()

	resp, err := http.Post(srv.URL+"/notes/", "application/json",
		bytes.NewReader([]byte(`{"id":"n1","title":"hello"}`)))
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("status = %d, want 201", resp.StatusCode)
	}

	// Drain async subscribers.
	if err := b.Close(context.Background()); err != nil {
		t.Fatalf("bus close: %v", err)
	}

	got := events()
	if len(got) != 1 {
		t.Fatalf("got %d events, want 1: %+v", len(got), got)
	}
	if got[0].topic != TopicDocumentCreated {
		t.Errorf("topic = %q, want %q", got[0].topic, TopicDocumentCreated)
	}
	if got[0].source != EventSource {
		t.Errorf("source = %q, want %q", got[0].source, EventSource)
	}
	if got[0].payload.Type != "notes" || got[0].payload.ID != "n1" {
		t.Errorf("payload = %+v, want type=notes id=n1", got[0].payload)
	}
	if got[0].payload.CreatedAt == "" || got[0].payload.UpdatedAt == "" {
		t.Errorf("payload missing timestamps: %+v", got[0].payload)
	}
	if got[0].payload.VersionID == "" {
		t.Errorf("payload missing version_id: %+v", got[0].payload)
	}
	if got[0].payload.Seq != 1 {
		t.Errorf("payload seq = %d, want 1: %+v", got[0].payload.Seq, got[0].payload)
	}
}

func TestRegisterDocumentRoutes_EmitsUpdated(t *testing.T) {
	router, _, b, events := newTestEngine(t)

	srv := httptest.NewServer(router)
	defer srv.Close()

	// Seed a doc.
	resp, err := http.Post(srv.URL+"/notes/", "application/json",
		bytes.NewReader([]byte(`{"id":"n2","title":"v1"}`)))
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()

	// Update.
	req, _ := http.NewRequest("PUT", srv.URL+"/notes/n2",
		bytes.NewReader([]byte(`{"title":"v2"}`)))
	req.Header.Set("Content-Type", "application/json")
	resp2, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp2.Body.Close()
	if resp2.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", resp2.StatusCode)
	}

	if err := b.Close(context.Background()); err != nil {
		t.Fatalf("bus close: %v", err)
	}

	topics := []bus.Topic{}
	for _, e := range events() {
		topics = append(topics, e.topic)
	}
	wantSeen := map[bus.Topic]bool{
		TopicDocumentCreated: false,
		TopicDocumentUpdated: false,
	}
	for _, top := range topics {
		if _, ok := wantSeen[top]; ok {
			wantSeen[top] = true
		}
	}
	if !wantSeen[TopicDocumentCreated] {
		t.Errorf("missing %s event in %v", TopicDocumentCreated, topics)
	}
	if !wantSeen[TopicDocumentUpdated] {
		t.Errorf("missing %s event in %v", TopicDocumentUpdated, topics)
	}

	// Verify the updated payload carries id/type.
	for _, e := range events() {
		if e.topic == TopicDocumentUpdated {
			if e.payload.Type != "notes" || e.payload.ID != "n2" {
				t.Errorf("updated payload = %+v, want type=notes id=n2", e.payload)
			}
		}
	}
}

// TestRegisterDocumentRoutes_UpdateSeqMonotonic verifies that two
// successive PUTs against the same doc emit document.updated events
// whose payload.seq increments monotonically (2, then 3) and whose
// version_id is non-empty per event. The created event itself uses
// seq=1; updates pick up from there.
func TestRegisterDocumentRoutes_UpdateSeqMonotonic(t *testing.T) {
	router, _, b, events := newTestEngine(t)

	srv := httptest.NewServer(router)
	defer srv.Close()

	// Seed a doc — produces seq=1 created event.
	resp, err := http.Post(srv.URL+"/notes/", "application/json",
		bytes.NewReader([]byte(`{"id":"n-mono","title":"v1"}`)))
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()

	// Two successive updates → seq=2, seq=3.
	for _, body := range []string{`{"title":"v2"}`, `{"title":"v3"}`} {
		req, _ := http.NewRequest("PUT", srv.URL+"/notes/n-mono", bytes.NewReader([]byte(body)))
		req.Header.Set("Content-Type", "application/json")
		r, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		r.Body.Close()
		if r.StatusCode != http.StatusOK {
			t.Fatalf("update status = %d, want 200", r.StatusCode)
		}
	}

	if err := b.Close(context.Background()); err != nil {
		t.Fatalf("bus close: %v", err)
	}

	var updates []captured
	for _, e := range events() {
		if e.topic == TopicDocumentUpdated {
			updates = append(updates, e)
		}
	}
	if len(updates) != 2 {
		t.Fatalf("got %d updated events, want 2: %+v", len(updates), updates)
	}
	wantSeq := []int{2, 3}
	for i, u := range updates {
		if u.payload.Seq != wantSeq[i] {
			t.Errorf("updates[%d].seq = %d, want %d", i, u.payload.Seq, wantSeq[i])
		}
		if u.payload.VersionID == "" {
			t.Errorf("updates[%d] missing version_id: %+v", i, u.payload)
		}
	}
}

func TestRegisterDocumentRoutes_EmitsDeleted(t *testing.T) {
	router, _, b, events := newTestEngine(t)

	srv := httptest.NewServer(router)
	defer srv.Close()

	// Seed.
	resp, err := http.Post(srv.URL+"/notes/", "application/json",
		bytes.NewReader([]byte(`{"id":"n3"}`)))
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()

	req, _ := http.NewRequest("DELETE", srv.URL+"/notes/n3", nil)
	resp2, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp2.Body.Close()
	if resp2.StatusCode != http.StatusNoContent {
		t.Fatalf("status = %d, want 204", resp2.StatusCode)
	}

	if err := b.Close(context.Background()); err != nil {
		t.Fatalf("bus close: %v", err)
	}

	var seenDelete bool
	for _, e := range events() {
		if e.topic == TopicDocumentDeleted {
			seenDelete = true
			if e.payload.Type != "notes" || e.payload.ID != "n3" {
				t.Errorf("delete payload = %+v, want type=notes id=n3", e.payload)
			}
		}
	}
	if !seenDelete {
		t.Errorf("missing %s event", TopicDocumentDeleted)
	}
}

func TestRegisterDocumentRoutes_NoEventOnValidationFailure(t *testing.T) {
	router, _, b, events := newTestEngine(t)

	srv := httptest.NewServer(router)
	defer srv.Close()

	// Invalid type triggers the 400 path before the store is touched.
	resp, err := http.Post(srv.URL+"/INVALID-TYPE/", "application/json",
		bytes.NewReader([]byte(`{}`)))
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", resp.StatusCode)
	}

	// Invalid JSON also short-circuits before store + event.
	resp2, err := http.Post(srv.URL+"/notes/", "application/json",
		bytes.NewReader([]byte(`not-json`)))
	if err != nil {
		t.Fatal(err)
	}
	resp2.Body.Close()
	if resp2.StatusCode != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", resp2.StatusCode)
	}

	// Update non-existent doc → 404, no event.
	req, _ := http.NewRequest("PUT", srv.URL+"/notes/missing",
		bytes.NewReader([]byte(`{}`)))
	req.Header.Set("Content-Type", "application/json")
	resp3, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	resp3.Body.Close()
	if resp3.StatusCode != http.StatusNotFound {
		t.Fatalf("status = %d, want 404", resp3.StatusCode)
	}

	// Delete non-existent → 404, no event.
	req2, _ := http.NewRequest("DELETE", srv.URL+"/notes/missing", nil)
	resp4, err := http.DefaultClient.Do(req2)
	if err != nil {
		t.Fatal(err)
	}
	resp4.Body.Close()
	if resp4.StatusCode != http.StatusNotFound {
		t.Fatalf("status = %d, want 404", resp4.StatusCode)
	}

	if err := b.Close(context.Background()); err != nil {
		t.Fatalf("bus close: %v", err)
	}

	if got := events(); len(got) != 0 {
		t.Errorf("expected zero events on failed writes, got %d: %+v", len(got), got)
	}
}

func TestRegisterDocumentRoutes_NilBusIsSafe(t *testing.T) {
	ds, err := store.NewDocumentStore(filepath.Join(t.TempDir(), "documents.db"))
	if err != nil {
		t.Fatalf("new store: %v", err)
	}
	defer ds.Close()

	vstore := store.NewInMemoryVersionStore()
	vds := store.NewVersionedDocumentStore(ds, vstore)

	router := api.NewRouter()
	registerDocumentRoutes(router, vds, nil)

	srv := httptest.NewServer(router)
	defer srv.Close()

	resp, err := http.Post(srv.URL+"/notes/", "application/json",
		bytes.NewReader([]byte(`{"id":"safe"}`)))
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("status = %d, want 201", resp.StatusCode)
	}
}
