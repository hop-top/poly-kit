package service

import (
	"errors"
	"fmt"
	"net/http"
	"strings"

	"hop.top/kit/go/transport/api"
)

// Surface is an additional protocol surface of the api service: a set
// of routes beside the REST projection, built from the same command
// tree and run through the same executor. It is the extension point
// for a wire contract that REST does not express (a capability
// manifest, a job model); the surface owns its routes and bodies, the
// service owns the listener, the guard chain and the executor.
//
// A surface gets the same guards as every REST route: authentication,
// host check, body limit, compression, the permission gate inside the
// executor. The one thing it may ask for is that named routes skip
// authentication; see [PublicRoute].
type Surface interface {
	// Name identifies the surface in errors. It is unique across
	// [APIConfig.Surfaces] and not empty.
	Name() string
	// Mount registers the surface's routes on router. proj is what the
	// REST projection was built from: the reflected command
	// descriptors, the executor that runs a command with the gates,
	// audit and idempotency the other surfaces have, and the tool name
	// and version. A handler reads its caller with [api.ClaimsFromContext]
	// and [api.IdentityOf].
	//
	// Mount runs once per start, after the projection and the
	// adopter's routes. A route already served at a path is a
	// collision: the mux refuses a second registration and the start
	// fails naming the surface. An error fails the start too.
	Mount(router *api.Router, proj api.ProjectionConfig) error
	// PublicRoutes lists the exact routes that skip authentication.
	// Nil (the default) makes every route of the surface guarded.
	PublicRoutes() []PublicRoute
}

// PublicRoute names one route a [Surface] serves without
// authentication, by the exact method and path it registers. There
// are no patterns: a path with a wildcard, a trailing prefix match, a
// different method or a longer path stays guarded. A route that
// another part of the service already serves (a REST projection
// route, an adopter route) cannot be declared: the start fails.
type PublicRoute struct {
	Method string
	Path   string
}

// validateSurfaces checks the declarations that need no router.
func validateSurfaces(ss []Surface) error {
	seen := map[string]bool{}
	for i, s := range ss {
		if s == nil {
			return fmt.Errorf("surfaces[%d]: nil surface", i)
		}
		name := s.Name()
		if name == "" {
			return fmt.Errorf("surfaces[%d]: empty name", i)
		}
		if seen[name] {
			return fmt.Errorf("surface %q: declared twice", name)
		}
		seen[name] = true
		for _, p := range s.PublicRoutes() {
			if err := validatePublicRoute(p); err != nil {
				return fmt.Errorf("surface %q: public route %s %s: %w", name, p.Method, p.Path, err)
			}
		}
	}
	return nil
}

func validatePublicRoute(p PublicRoute) error {
	switch {
	case p.Method == "" || p.Method != strings.ToUpper(p.Method) || strings.ContainsAny(p.Method, " \t/"):
		return errors.New("method must be an upper-case HTTP method")
	case !strings.HasPrefix(p.Path, "/") || p.Path == "/":
		return errors.New("path must be an absolute path below /")
	case strings.ContainsAny(p.Path, "{}?# \t") || strings.HasSuffix(p.Path, "/"):
		return errors.New("path must be exact: no wildcards, query or trailing slash")
	}
	return nil
}

// publicRoutes collects every declared public route, keyed by method
// and path.
func publicRoutes(ss []Surface) map[PublicRoute]string {
	out := map[PublicRoute]string{}
	for _, s := range ss {
		for _, p := range s.PublicRoutes() {
			out[p] = s.Name()
		}
	}
	return out
}

// exemptPublic lets exactly the declared routes past the
// authentication guard. Nothing else changes: a request for any other
// method or path goes through guard.
func exemptPublic(guard api.Middleware, public map[PublicRoute]string) api.Middleware {
	if len(public) == 0 {
		return guard
	}
	return func(next http.Handler) http.Handler {
		guarded := guard(next)
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if _, ok := public[PublicRoute{r.Method, r.URL.Path}]; ok {
				next.ServeHTTP(w, r)
				return
			}
			guarded.ServeHTTP(w, r)
		})
	}
}

// mountSurfaces mounts each surface and checks its public routes: a
// route must not be served before the surface mounts and must be
// served after. A panic from a registration (the mux refuses a
// duplicate pattern) is a start error naming the surface.
func mountSurfaces(router *api.Router, proj api.ProjectionConfig, ss []Surface) error {
	for _, s := range ss {
		if err := mountSurface(router, proj, s); err != nil {
			return fmt.Errorf("surface %q: %w", s.Name(), err)
		}
	}
	return nil
}

func mountSurface(router *api.Router, proj api.ProjectionConfig, s Surface) (err error) {
	defer func() {
		if r := recover(); r != nil {
			err = fmt.Errorf("mount: %v", r)
		}
	}()
	pub := s.PublicRoutes()
	for _, p := range pub {
		if router.Serves(p.Method, p.Path) {
			return fmt.Errorf("public route %s %s is already served by the service", p.Method, p.Path)
		}
	}
	if err := s.Mount(router, proj); err != nil {
		return err
	}
	for _, p := range pub {
		if !router.Serves(p.Method, p.Path) {
			return fmt.Errorf("public route %s %s is not mounted by the surface", p.Method, p.Path)
		}
	}
	return nil
}
