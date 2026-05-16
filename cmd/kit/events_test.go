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

// recordAll subscribes to "#" so the test sees any topic the engine
// emits — useful when overriding the default kit.engine.document.*
// namespace via WithTopicPrefix / WithTopics.
func recordAll(b bus.Bus) func() []captured {
	var (
		mu     sync.Mutex
		events []captured
	)
	b.SubscribeAsync("#", func(_ context.Context, e bus.Event) {
		var p DocumentEventPayload
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

// newEngineWithOpts builds a router wired through registerDocumentRoutes
// with the supplied EventOptions. Returns the router, the bus, and a
// snapshot accessor backed by a "#" subscription so callers can inspect
// any emitted topic.
func newEngineWithOpts(t *testing.T, opts ...EventOption) (*api.Router, bus.Bus, func() []captured) {
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
	snapshot := recordAll(b)
	router := api.NewRouter()
	registerDocumentRoutes(router, vds, b, opts...)
	return router, b, snapshot
}

func TestDefaultDocumentTopics_Valid(t *testing.T) {
	for name, top := range map[string]bus.Topic{
		"Created": DefaultDocumentTopics.Created,
		"Updated": DefaultDocumentTopics.Updated,
		"Deleted": DefaultDocumentTopics.Deleted,
	} {
		if err := bus.ValidateTopic(top); err != nil {
			t.Errorf("DefaultDocumentTopics.%s = %q is invalid: %v", name, top, err)
		}
	}
}

func TestWithTopicPrefix_OverridesAllThree(t *testing.T) {
	router, b, events := newEngineWithOpts(t,
		WithTopicPrefix("myapp.engine.document"),
	)

	srv := httptest.NewServer(router)
	defer srv.Close()

	resp, err := http.Post(srv.URL+"/notes/", "application/json",
		bytes.NewReader([]byte(`{"id":"p1","title":"hello"}`)))
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()

	if err := b.Close(context.Background()); err != nil {
		t.Fatalf("bus close: %v", err)
	}
	got := events()
	if len(got) != 1 {
		t.Fatalf("got %d events, want 1: %+v", len(got), got)
	}
	wantTopic := bus.Topic("myapp.engine.document.created")
	if got[0].topic != wantTopic {
		t.Errorf("topic = %q, want %q", got[0].topic, wantTopic)
	}
	// Source defaults to first 2 prefix segments.
	if got[0].source != "myapp.engine" {
		t.Errorf("source = %q, want %q", got[0].source, "myapp.engine")
	}
}

func TestWithTopicPrefix_AppliesToUpdateAndDelete(t *testing.T) {
	router, b, events := newEngineWithOpts(t,
		WithTopicPrefix("myapp.engine.document"),
	)

	srv := httptest.NewServer(router)
	defer srv.Close()

	// Seed.
	resp, _ := http.Post(srv.URL+"/notes/", "application/json",
		bytes.NewReader([]byte(`{"id":"u1","title":"v1"}`)))
	resp.Body.Close()

	// Update.
	req, _ := http.NewRequest("PUT", srv.URL+"/notes/u1",
		bytes.NewReader([]byte(`{"title":"v2"}`)))
	req.Header.Set("Content-Type", "application/json")
	r2, _ := http.DefaultClient.Do(req)
	r2.Body.Close()

	// Delete.
	req3, _ := http.NewRequest("DELETE", srv.URL+"/notes/u1", nil)
	r3, _ := http.DefaultClient.Do(req3)
	r3.Body.Close()

	if err := b.Close(context.Background()); err != nil {
		t.Fatalf("bus close: %v", err)
	}

	seen := map[bus.Topic]bool{}
	for _, e := range events() {
		seen[e.topic] = true
	}
	for _, want := range []bus.Topic{
		"myapp.engine.document.created",
		"myapp.engine.document.updated",
		"myapp.engine.document.deleted",
	} {
		if !seen[want] {
			t.Errorf("missing topic %q in %+v", want, seen)
		}
	}
}

func TestWithTopicPrefix_InvalidPanics(t *testing.T) {
	cases := []string{
		"",                       // empty
		"only.two",               // 2 segments
		"too.many.segments.here", // 4 segments
		"MyApp.engine.document",  // uppercase
		"myapp.engine.",          // trailing dot
	}
	for _, p := range cases {
		t.Run(p, func(t *testing.T) {
			defer func() {
				if r := recover(); r == nil {
					t.Errorf("WithTopicPrefix(%q) did not panic", p)
				}
			}()
			_ = WithTopicPrefix(p)
		})
	}
}

func TestWithTopics_PartialOverride(t *testing.T) {
	router, b, events := newEngineWithOpts(t,
		WithTopics(DocumentTopics{
			Created: "myapp.engine.document.created",
			// Updated + Deleted left empty → keep defaults
		}),
	)

	srv := httptest.NewServer(router)
	defer srv.Close()

	// Seed → Created override.
	resp, _ := http.Post(srv.URL+"/notes/", "application/json",
		bytes.NewReader([]byte(`{"id":"po1"}`)))
	resp.Body.Close()

	// Update → still default topic.
	req, _ := http.NewRequest("PUT", srv.URL+"/notes/po1",
		bytes.NewReader([]byte(`{"v":2}`)))
	req.Header.Set("Content-Type", "application/json")
	r2, _ := http.DefaultClient.Do(req)
	r2.Body.Close()

	if err := b.Close(context.Background()); err != nil {
		t.Fatalf("bus close: %v", err)
	}

	seen := map[bus.Topic]bool{}
	for _, e := range events() {
		seen[e.topic] = true
	}
	if !seen["myapp.engine.document.created"] {
		t.Errorf("missing overridden created topic; saw %+v", seen)
	}
	if !seen[TopicDocumentUpdated] {
		t.Errorf("missing default updated topic; saw %+v", seen)
	}
}

func TestWithTopics_AllOverridden(t *testing.T) {
	router, b, events := newEngineWithOpts(t,
		WithTopics(DocumentTopics{
			Created: "myapp.engine.document.created",
			Updated: "myapp.engine.document.updated",
			Deleted: "myapp.engine.document.deleted",
		}),
	)
	srv := httptest.NewServer(router)
	defer srv.Close()

	resp, _ := http.Post(srv.URL+"/notes/", "application/json",
		bytes.NewReader([]byte(`{"id":"ao1"}`)))
	resp.Body.Close()

	req, _ := http.NewRequest("DELETE", srv.URL+"/notes/ao1", nil)
	r2, _ := http.DefaultClient.Do(req)
	r2.Body.Close()

	if err := b.Close(context.Background()); err != nil {
		t.Fatalf("bus close: %v", err)
	}
	seen := map[bus.Topic]bool{}
	for _, e := range events() {
		seen[e.topic] = true
	}
	if !seen["myapp.engine.document.created"] {
		t.Errorf("missing overridden created topic; saw %+v", seen)
	}
	if !seen["myapp.engine.document.deleted"] {
		t.Errorf("missing overridden deleted topic; saw %+v", seen)
	}
}

func TestWithTopics_InvalidPanics(t *testing.T) {
	cases := []struct {
		name string
		t    DocumentTopics
	}{
		{"created_not_past_tense", DocumentTopics{Created: "kit.engine.document.create"}},
		{"updated_too_few_segments", DocumentTopics{Updated: "kit.engine.updated"}},
		{"deleted_uppercase", DocumentTopics{Deleted: "Kit.engine.document.deleted"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			defer func() {
				if r := recover(); r == nil {
					t.Errorf("WithTopics(%+v) did not panic", tc.t)
				}
			}()
			opt := WithTopics(tc.t)
			cfg := &EventConfig{topics: DefaultDocumentTopics, source: EventSource}
			opt(cfg)
		})
	}
}

func TestWithEventSource_OverridesSource(t *testing.T) {
	router, b, events := newEngineWithOpts(t,
		WithEventSource("myapp.custom"),
	)
	srv := httptest.NewServer(router)
	defer srv.Close()

	resp, _ := http.Post(srv.URL+"/notes/", "application/json",
		bytes.NewReader([]byte(`{"id":"src1"}`)))
	resp.Body.Close()

	if err := b.Close(context.Background()); err != nil {
		t.Fatalf("bus close: %v", err)
	}
	got := events()
	if len(got) != 1 {
		t.Fatalf("got %d events, want 1: %+v", len(got), got)
	}
	if got[0].source != "myapp.custom" {
		t.Errorf("source = %q, want %q", got[0].source, "myapp.custom")
	}
	// Topic unchanged (defaults).
	if got[0].topic != TopicDocumentCreated {
		t.Errorf("topic = %q, want default %q", got[0].topic, TopicDocumentCreated)
	}
}

func TestNoOpts_PreservesDefaults(t *testing.T) {
	// Backward-compat: registerDocumentRoutes(...) with no opts must
	// emit the documented default topics and source.
	router, b, events := newEngineWithOpts(t /* no opts */)
	srv := httptest.NewServer(router)
	defer srv.Close()

	resp, _ := http.Post(srv.URL+"/notes/", "application/json",
		bytes.NewReader([]byte(`{"id":"def1"}`)))
	resp.Body.Close()

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
}
