package cli

import (
	"errors"
	"fmt"
	"net/http"
	"slices"
	"strings"
	"time"

	"github.com/spf13/cast"

	"hop.top/kit/go/console/cli/svcconfig"
	"hop.top/kit/go/transport/api"
)

// corsBlock is an HTTP listener's CORS block, HTTP-plane slot 9:
// services.<svc>.cors, with shared defaults under services.all.cors.
// Its keys are registered in svcconfig.
const corsBlock = "cors"

// ServeCORS is what a listener's protocol needs from CORS: the methods
// its browser clients send, the request headers they set and the
// response headers they read. The cors block's allow_methods,
// allow_headers and expose_headers default to these, added to what
// every kit listener's clients need (credentials, trace context,
// request id, idempotency, confirmation); each key set replaces its
// default.
type ServeCORS struct {
	// Methods are the protocol's methods. Empty is GET, HEAD, POST.
	Methods []string
	// AllowHeaders are the request headers the protocol's clients set.
	AllowHeaders []string
	// ExposeHeaders are the response headers they read.
	ExposeHeaders []string
}

// Headers every kit listener reads from a browser client, and writes
// for one to read, whatever its protocol.
var (
	corsBaseAllowHeaders = []string{
		"Authorization", "Content-Type",
		api.HeaderTraceparent, api.HeaderTracestate, api.HeaderTraceID, "X-Request-ID",
		api.HeaderIdempotencyKey, "X-Confirm-Token",
	}
	corsBaseExposeHeaders = []string{
		"X-Request-ID", api.HeaderIdempotentReplayed, "Retry-After", "WWW-Authenticate",
	}
)

// apiServeCORS is the api service's protocol: REST verbs, conditional
// reads, created resources.
var apiServeCORS = ServeCORS{
	Methods: []string{
		http.MethodGet, http.MethodHead, http.MethodPost,
		http.MethodPut, http.MethodPatch, http.MethodDelete,
	},
	AllowHeaders:  []string{"If-None-Match"},
	ExposeHeaders: []string{"ETag", "Location"},
}

// corsSettings is the resolved cors block: whether slot 9 runs, the
// grant, and the key each value came from, for messages.
type corsSettings struct {
	enabled bool
	cfg     api.CORSConfig
	keys    map[string]string
}

// corsSettings resolves the listener's cors block. It is on when
// cors.enabled says so, or, unset, when allow_origins lists an origin.
// A value that does not parse is an error naming its key.
func (p httpPlane) corsSettings() (corsSettings, error) {
	s := corsSettings{keys: map[string]string{}}
	cfg := svcconfig.New(p.viper())
	raw := func(key string) (any, bool) {
		v, from, ok := cfg.Lookup(p.l.Service, corsBlock, key)
		if ok {
			s.keys[key] = from
		}
		return v, ok
	}
	list := func(key string, def []string) ([]string, error) {
		v, ok := raw(key)
		if !ok {
			return def, nil
		}
		entries, err := cast.ToStringSliceE(v)
		if err != nil {
			return nil, fmt.Errorf("%s: must be a list of strings", s.keys[key])
		}
		var out []string
		for _, e := range entries {
			out = append(out, strings.FieldsFunc(e, func(r rune) bool { return r == ',' || r == ' ' })...)
		}
		return out, nil
	}
	flag := func(key string) (value, set bool, err error) {
		v, ok := raw(key)
		if !ok {
			return false, false, nil
		}
		b, err := cast.ToBoolE(v)
		if err != nil {
			return false, true, fmt.Errorf("%s: %v is not true or false", s.keys[key], v)
		}
		return b, true, nil
	}

	var errs []error
	collect := func(err error) {
		if err != nil {
			errs = append(errs, err)
		}
	}
	proto := p.l.CORS
	methods := proto.Methods
	if len(methods) == 0 {
		methods = []string{http.MethodGet, http.MethodHead, http.MethodPost}
	}
	var err error
	s.cfg.AllowOrigins, err = list("allow_origins", nil)
	collect(err)
	s.cfg.AllowMethods, err = list("allow_methods", methods)
	collect(err)
	s.cfg.AllowHeaders, err = list("allow_headers", union(corsBaseAllowHeaders, proto.AllowHeaders))
	collect(err)
	s.cfg.ExposeHeaders, err = list("expose_headers", union(corsBaseExposeHeaders, proto.ExposeHeaders))
	collect(err)
	s.cfg.AllowCredentials, _, err = flag("allow_credentials")
	collect(err)
	if v, ok := raw("max_age"); ok {
		d, err := durationValue(v)
		switch {
		case err != nil:
			collect(fmt.Errorf("%s: %w", s.keys["max_age"], err))
		case d < 0:
			collect(fmt.Errorf("%s: %s is negative; use 0 to send no max age", s.keys["max_age"], d))
		case d > 0 && d < time.Second:
			collect(fmt.Errorf("%s: %s is under a second, the unit browsers cache preflights in", s.keys["max_age"], d))
		default:
			s.cfg.MaxAge = int(d / time.Second)
		}
	}
	enabled, set, err := flag("enabled")
	collect(err)
	s.enabled = enabled || (!set && len(s.cfg.AllowOrigins) > 0)
	return s, errors.Join(errs...)
}

// union is a followed by the entries of b it lacks, without case.
func union(a, b []string) []string {
	out := slices.Clone(a)
	for _, h := range b {
		if !slices.ContainsFunc(out, func(o string) bool { return strings.EqualFold(o, h) }) {
			out = append(out, h)
		}
	}
	return out
}

// validateCORS refuses, before anything binds, an unknown key in
// either cors block and a grant the Fetch standard or a browser would
// not honor: cors on with no origin, an origin that is not a bare
// origin, "*" beside origins or with credentials, a method or header
// that is not a token, a negative max age.
func (p httpPlane) validateCORS() error {
	if v := p.viper(); v != nil {
		if err := svcconfig.New(v).ValidateBlock(corsBlock, p.l.Service, svcconfig.Shared); err != nil {
			return err
		}
	}
	s, err := p.corsSettings()
	if err != nil || !s.enabled {
		return err
	}
	key := func(k string) string {
		if from := s.keys[k]; from != "" {
			return from
		}
		return svcconfig.Key(p.l.Service, corsBlock, k)
	}
	if len(s.cfg.AllowOrigins) == 0 {
		return fmt.Errorf("%s: cors is on and grants no origin; list origins in %s, or switch it off",
			key("enabled"), key("allow_origins"))
	}
	c := s.cfg
	var errs []error
	for k, part := range map[string]api.CORSConfig{
		"allow_origins":  {AllowOrigins: c.AllowOrigins},
		"allow_methods":  {AllowMethods: c.AllowMethods},
		"allow_headers":  {AllowHeaders: c.AllowHeaders},
		"expose_headers": {ExposeHeaders: c.ExposeHeaders, AllowCredentials: c.AllowCredentials},
	} {
		if err := part.Validate(); err != nil {
			errs = append(errs, fmt.Errorf("%s: %w", key(k), err))
		}
	}
	if slices.Contains(c.AllowOrigins, "*") && c.AllowCredentials {
		errs = append(errs, fmt.Errorf(
			"%s: credentials cannot be allowed with the \"*\" origin in %s (Fetch standard); list the origins instead",
			key("allow_credentials"), key("allow_origins")))
	}
	return joinSortedErrors(errs)
}

// joinSortedErrors joins errs in message order, so a report built from
// a map is stable.
func joinSortedErrors(errs []error) error {
	slices.SortFunc(errs, func(a, b error) int { return strings.Compare(a.Error(), b.Error()) })
	return errors.Join(errs...)
}

// corsOrigins is the origins the cors block grants by name, as a
// browser sends them, when it is on: the origins slot 8's Origin
// check admits beside its own allow list. "*" is not among them: it
// grants reading to every origin, never writing.
func (p httpPlane) corsOrigins() []string {
	s, err := p.corsSettings()
	if err != nil || !s.enabled {
		return nil
	}
	var out []string
	for _, o := range s.cfg.AllowOrigins {
		if n, err := api.ParseOrigin(o); err == nil {
			out = append(out, n)
		}
	}
	return out
}

// corsMiddleware is HTTP-plane slot 9 when the cors block is on, and
// nothing otherwise: inside the Host and Origin checks, so a
// rebinding page is refused before it; ahead of the body limit,
// compression and authentication, so a preflight, which carries no
// credentials, is answered. The OAuth protected-resource metadata
// document answers its own CORS, open to every origin (RFC 9728), so
// the slot leaves it alone.
func (p httpPlane) corsMiddleware() ([]api.Middleware, error) {
	s, err := p.corsSettings()
	if err != nil {
		return nil, err
	}
	if !s.enabled {
		return nil, nil
	}
	grant := api.CORS(s.cfg)
	return []api.Middleware{func(next http.Handler) http.Handler {
		granted := grant(next)
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if strings.HasPrefix(r.URL.Path, api.ProtectedResourceWellKnown) {
				next.ServeHTTP(w, r)
				return
			}
			granted.ServeHTTP(w, r)
		})
	}}, nil
}
