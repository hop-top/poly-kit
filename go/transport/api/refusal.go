package api

import (
	"context"
	"net/http"
	"sync"
)

// refusalSlotKey is the context key of the slot a refusal code is
// recorded into.
type refusalSlotKey struct{}

// refusalSlot holds the one refusal code a request ended with. It is
// written by the middleware that refused and read by the middleware
// that observes, which run on the same goroutine in the common case;
// the mutex covers handlers that hand the request to another one.
type refusalSlot struct {
	mu   sync.Mutex
	code string
}

// ObserveRefusal returns r with a slot for a refusal code in its
// context, and a function that reads the slot once the handler has
// returned.
//
// It is how the tracing and metrics middleware counts HTTP-plane
// refusals by code: it sits outside the middleware that refuse (the
// body limit, the Host and Origin checks), installs the slot, and
// reads it back when the response is done. An empty read means no
// middleware recorded a refusal. See [RecordRefusal].
func ObserveRefusal(r *http.Request) (*http.Request, func() string) {
	slot := &refusalSlot{}
	r = r.WithContext(context.WithValue(r.Context(), refusalSlotKey{}, slot))
	return r, func() string {
		slot.mu.Lock()
		defer slot.mu.Unlock()
		return slot.code
	}
}

// RecordRefusal notes that the response to r is a refusal with the
// stable code (body_too_large, host_rejected, …), so an observer
// installed by [ObserveRefusal] counts it by code. It is a no-op when
// nothing observes, and the first code recorded for a request wins.
//
// HTTP-plane middleware that refuses a request calls it before
// writing the response. A refusal the bridge decides is counted on
// the invocation plane instead, through the audit sinks, so the
// projection does not record one here.
func RecordRefusal(r *http.Request, code string) {
	if r == nil || code == "" {
		return
	}
	slot, ok := r.Context().Value(refusalSlotKey{}).(*refusalSlot)
	if !ok {
		return
	}
	slot.mu.Lock()
	defer slot.mu.Unlock()
	if slot.code == "" {
		slot.code = code
	}
}

// refusalObserved reports whether an observer installed by
// [ObserveRefusal] is waiting on r, so a middleware can skip work that
// only serves recording.
func refusalObserved(r *http.Request) bool {
	_, ok := r.Context().Value(refusalSlotKey{}).(*refusalSlot)
	return ok
}
