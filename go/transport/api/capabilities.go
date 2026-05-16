package api

import (
	"net/http"
	"sort"
	"sync"

	"hop.top/kit/go/ai/toolspec"
)

// capConfig holds capabilities endpoint configuration.
type capConfig struct {
	serviceName string
	version     string
	middleware  []Middleware
}

// WithCapabilities enables the GET /capabilities endpoint that returns
// a CapabilitySet built from all registered routes on the Router.
//
// Security: the endpoint is unauthenticated by default and exposes route
// topology. Pass authentication middleware to restrict access.
func WithCapabilities(svc, version string, mw ...Middleware) RouterOption {
	return func(r *Router) {
		r.capCfg = &capConfig{serviceName: svc, version: version, middleware: mw}
	}
}

// registerCapabilitiesEndpoint installs the /capabilities handler.
// Called internally after router construction when capCfg is set.
func (r *Router) registerCapabilitiesEndpoint() {
	var (
		once     sync.Once
		cached   []byte
		cacheErr error
	)

	handler := http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		once.Do(func() {
			cs := r.buildCapabilitySet()
			cached, cacheErr = cs.JSON()
		})
		if cacheErr != nil {
			Error(w, http.StatusInternalServerError, &APIError{
				Status:  http.StatusInternalServerError,
				Code:    "internal",
				Message: "failed to serialize capabilities",
			})
			return
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write(cached)
	})

	var h http.Handler = handler
	for i := len(r.capCfg.middleware) - 1; i >= 0; i-- {
		h = r.capCfg.middleware[i](h)
	}
	r.mux.Handle("GET /capabilities", h)
}

// buildCapabilitySet constructs a CapabilitySet from tracked routes.
func (r *Router) buildCapabilitySet() toolspec.CapabilitySet {
	cs := toolspec.NewCapabilitySet(r.capCfg.serviceName, r.capCfg.version)

	// Aggregate methods per path.
	type entry struct {
		methods []string
		capType string
	}
	grouped := make(map[string]*entry)

	for _, rt := range r.routes {
		e, ok := grouped[rt.path]
		if !ok {
			e = &entry{capType: rt.capType}
			grouped[rt.path] = e
		}
		if rt.method != "" {
			e.methods = append(e.methods, rt.method)
		}
	}

	paths := make([]string, 0, len(grouped))
	for path := range grouped {
		paths = append(paths, path)
	}
	sort.Strings(paths)

	for _, path := range paths {
		e := grouped[path]
		cs.Add(toolspec.Capability{
			Name:    e.capType + ":" + path,
			Type:    e.capType,
			Path:    path,
			Methods: e.methods,
		})
	}

	return cs
}

// routeEntry records a registered route for capability introspection.
type routeEntry struct {
	method  string
	path    string
	capType string // "endpoint" or "resource"
}
