package api

import (
	"context"
	"net/http"
	"sync"
)

// routeSlotKey is the context key of the slot the matched route
// pattern is recorded into.
type routeSlotKey struct{}

// routeSlot holds the ServeMux pattern a Router matched for one
// request. It is written by the Router and read by the middleware
// that observes, outside it.
type routeSlot struct {
	mu      sync.Mutex
	pattern string
}

// ObserveRoute returns r with a slot for the route pattern a [Router]
// matches, and a function that reads the slot once the handler has
// returned.
//
// Middleware that wraps a Router from outside never sees
// Request.Pattern: the mux sets it on the request it dispatches, which
// is a copy made further in. The tracing and metrics middleware sits
// there (HTTP-plane slot 5) and names its span by route, so it
// installs this slot and reads the pattern back. An empty read means
// no Router matched a route: a probe answered ahead of it, a refusal
// by middleware outside it, or an unmatched path. A refusal by the
// Router's own outer middleware (see [WithOuterMiddleware]) still
// reports the route the request addressed.
func ObserveRoute(r *http.Request) (*http.Request, func() string) {
	slot := &routeSlot{}
	r = r.WithContext(context.WithValue(r.Context(), routeSlotKey{}, slot))
	return r, func() string {
		slot.mu.Lock()
		defer slot.mu.Unlock()
		return slot.pattern
	}
}

// recordRoute notes the pattern the Router's mux matched for r, for an
// observer installed by [ObserveRoute]. It is a no-op when nothing
// observes or nothing matched. A Router mounted under another records
// first, as its ServeHTTP returns first; the outer Router's pattern
// then replaces it, so the route reported is the one the full path
// matched.
func recordRoute(r *http.Request) {
	if r == nil {
		return
	}
	recordPattern(r, r.Pattern)
}

// recordPattern notes pattern for an observer installed by
// [ObserveRoute]; a no-op when nothing observes or pattern is empty.
func recordPattern(r *http.Request, pattern string) {
	if pattern == "" {
		return
	}
	slot, ok := r.Context().Value(routeSlotKey{}).(*routeSlot)
	if !ok {
		return
	}
	slot.mu.Lock()
	defer slot.mu.Unlock()
	slot.pattern = pattern
}

// routeObserved reports whether an observer installed by
// [ObserveRoute] is waiting on r.
func routeObserved(r *http.Request) bool {
	_, ok := r.Context().Value(routeSlotKey{}).(*routeSlot)
	return ok
}
