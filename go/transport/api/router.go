package api

import (
	"net/http"

	"github.com/danielgtaylor/huma/v2"
)

// RouterOption configures a Router.
type RouterOption func(*Router)

// Router is an HTTP router built on Go 1.22+ ServeMux.
// It supports method+path patterns, groups, middleware, and mounting.
type Router struct {
	mux        *http.ServeMux
	prefix     string
	middleware []Middleware
	parent     *Router
	humaAPI    huma.API
	capCfg     *capConfig
	routes     []routeEntry
}

// NewRouter creates a Router with the given options.
func NewRouter(opts ...RouterOption) *Router {
	r := &Router{
		mux: http.NewServeMux(),
	}
	for _, o := range opts {
		o(r)
	}
	if r.capCfg != nil {
		r.registerCapabilitiesEndpoint()
	}
	return r
}

// Handle registers a handler for the given method and path.
// Method and path are combined into a Go 1.22+ ServeMux pattern
// (e.g. "GET /api/items/{id}").
func (r *Router) Handle(method, path string, handler http.HandlerFunc) {
	full := r.fullPath(path)
	pattern := method + " " + full
	r.mux.Handle(pattern, r.applyMiddleware(handler))
	r.trackRoute(method, full, "endpoint")
}

// Group creates a sub-router with an additional prefix and optional
// middleware. The group shares the parent's underlying ServeMux.
func (r *Router) Group(prefix string, mw ...Middleware) *Router {
	return &Router{
		mux:        r.mux,
		prefix:     r.fullPath(prefix),
		middleware: mw,
		parent:     r,
	}
}

// Mount attaches an http.Handler under the given prefix.
// A trailing slash is appended so the ServeMux treats it as a subtree.
func (r *Router) Mount(prefix string, h http.Handler) {
	full := r.fullPath(prefix)
	if full == "" {
		full = "/"
	}
	// Ensure trailing slash for subtree matching.
	if full[len(full)-1] != '/' {
		full += "/"
	}
	wrapped := r.applyMiddleware(h.ServeHTTP)
	r.mux.Handle(full, http.StripPrefix(full[:len(full)-1], wrapped))
}

// MountResource attaches a ResourceRouter and auto-registers its CRUD
// capabilities when WithCapabilities is enabled on this Router.
func (r *Router) MountResource(prefix string, h http.Handler, ops ...string) {
	r.Mount(prefix, h)
	if r.capCfg != nil && len(ops) > 0 {
		full := r.fullPath(prefix)
		r.trackResource(full, ops)
	}
}

// ServeHTTP implements http.Handler.
func (r *Router) ServeHTTP(w http.ResponseWriter, req *http.Request) {
	r.mux.ServeHTTP(w, req)
}

// PathParam extracts a path parameter from the request using
// Go 1.22+ r.PathValue.
func PathParam(r *http.Request, name string) string {
	return r.PathValue(name)
}

// trackRoute records a route for capabilities introspection.
// Routes on sub-routers propagate up the full parent chain to root.
func (r *Router) trackRoute(method, path, capType string) {
	entry := routeEntry{method: method, path: path, capType: capType}
	for cur := r; cur != nil; cur = cur.parent {
		if cur.routes != nil || cur.parent == nil {
			cur.routes = append(cur.routes, entry)
		}
	}
}

// trackResource registers resource CRUD capabilities on the router.
func (r *Router) trackResource(prefix string, ops []string) {
	methodMap := map[string]struct {
		method string
		suffix string
	}{
		"create": {"POST", "/"},
		"list":   {"GET", "/"},
		"get":    {"GET", "/{id}"},
		"update": {"PUT", "/{id}"},
		"delete": {"DELETE", "/{id}"},
	}
	for _, op := range ops {
		if info, ok := methodMap[op]; ok {
			r.trackRoute(info.method, prefix+info.suffix, "resource")
		}
	}
}

// fullPath returns the prefix-qualified path.
func (r *Router) fullPath(path string) string {
	return r.prefix + path
}

// allMiddleware returns the full middleware chain: parent → self.
func (r *Router) allMiddleware() []Middleware {
	if r.parent == nil {
		return r.middleware
	}
	parent := r.parent.allMiddleware()
	all := make([]Middleware, 0, len(parent)+len(r.middleware))
	all = append(all, parent...)
	all = append(all, r.middleware...)
	return all
}

// applyMiddleware wraps a handler with the full middleware chain.
func (r *Router) applyMiddleware(h http.HandlerFunc) http.Handler {
	mws := r.allMiddleware()
	if len(mws) == 0 {
		return h
	}
	return Chain(mws...)(h)
}
