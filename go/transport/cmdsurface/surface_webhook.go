package cmdsurface

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"text/template"
	"time"

	"hop.top/kit/go/transport/api"
)

// WebhookMapping declares how an inbound HTTP webhook routes to a
// bridge leaf. Mountable mappings are pre-validated against the
// bridge at MountWebhooks time: unknown paths, surfaces not enabled,
// destructive leaves without policy opt-in, auth-required leaves
// paired with AuthNone, and confirmation-required leaves without
// opt-in all surface as mount-time errors.
type WebhookMapping struct {
	// Name is the URL slug under the configured prefix
	// ("/hooks/{Name}"). Must be non-empty.
	Name string
	// Path is the leaf command path (e.g. ["widget","add"]).
	Path []string
	// FlagMap maps flag name → text/template source. Each template
	// runs against a root with these keys:
	//
	//   .body     map[string]any     (decoded JSON body, nil otherwise)
	//   .headers  map[string]string  (canonical → first value)
	//   .query    map[string]string  (first value per query key)
	//   .path     map[string]string  (mux path values; empty here)
	//
	// An empty rendered value omits the flag from the Invocation.
	FlagMap map[string]string
	// ArgsTemplate is an optional template whose rendered value is
	// split on whitespace and used as positional args.
	ArgsTemplate string
	// Auth selects the verification scheme for the request. Required.
	Auth WebhookAuth
}

// WebhookOption configures MountWebhooks.
type WebhookOption func(*webhookConfig)

type webhookConfig struct {
	prefix            string
	maxBody           int64
	resultLog         func(WebhookMapping, Result, error)
	allowConfirmation bool
}

func defaultWebhookConfig() webhookConfig {
	return webhookConfig{
		prefix:  "/hooks",
		maxBody: 1 << 20, // 1 MiB
	}
}

// WithWebhookPrefix sets the URL prefix every mapping is mounted
// under. Default "/hooks". A trailing "/" is stripped to avoid
// producing "/hooks//name".
func WithWebhookPrefix(prefix string) WebhookOption {
	return func(c *webhookConfig) {
		for len(prefix) > 1 && strings.HasSuffix(prefix, "/") {
			prefix = prefix[:len(prefix)-1]
		}
		c.prefix = prefix
	}
}

// WithWebhookMaxBody caps the inbound request body. Requests
// exceeding the cap are rejected with 413 payload_too_large. Default
// is 1 MiB.
func WithWebhookMaxBody(n int64) WebhookOption {
	return func(c *webhookConfig) {
		if n > 0 {
			c.maxBody = n
		}
	}
}

// WithWebhookResultLog installs a callback invoked after every
// successful Bridge.Invoke (and after Invoke errors that did not
// short-circuit earlier). It runs synchronously before the HTTP
// response is written.
func WithWebhookResultLog(fn func(WebhookMapping, Result, error)) WebhookOption {
	return func(c *webhookConfig) { c.resultLog = fn }
}

// WithWebhookAllowConfirmation lets MountWebhooks accept mappings
// whose leaf has Class.RequiresConfirmation. Webhooks have no human
// in the loop to mint a confirm token; opting in is an explicit
// acknowledgement that the Auth scheme is the security gate.
func WithWebhookAllowConfirmation() WebhookOption {
	return func(c *webhookConfig) { c.allowConfirmation = true }
}

// MountWebhooks registers one POST endpoint per mapping at
// {prefix}/{Name}. Mappings are validated against the bridge before
// any route is mounted: returning an error from MountWebhooks
// guarantees no routes were attached.
//
// Per-request behavior:
//
//   - Body is read with an io.LimitReader at WithWebhookMaxBody.
//     413 payload_too_large when the cap is exceeded.
//   - mapping.Auth.Verify(r, body) is called. 401 unauthorized on error.
//   - Body is JSON-decoded into .body when Content-Type indicates
//     JSON; otherwise .body is nil.
//   - FlagMap and ArgsTemplate are executed against the root keys.
//   - Bridge.Invoke is called. Sentinel errors map per the package
//     table; other errors flow through api.MapError.
//   - On success the response is 202 Accepted with an empty body.
func MountWebhooks(b *Bridge, r *api.Router, mappings []WebhookMapping, opts ...WebhookOption) error {
	if b == nil {
		return errors.New("cmdsurface: nil Bridge")
	}
	if r == nil {
		return errors.New("cmdsurface: nil api.Router")
	}
	cfg := defaultWebhookConfig()
	for _, o := range opts {
		o(&cfg)
	}

	// Resolve leaves and pre-parse templates for every mapping
	// before mounting anything.
	type resolved struct {
		mapping   WebhookMapping
		leaf      *Leaf
		flagTmpls map[string]*template.Template
		argsTmpl  *template.Template
	}
	prepared := make([]resolved, 0, len(mappings))
	seen := make(map[string]bool, len(mappings))

	for i, m := range mappings {
		if m.Name == "" {
			return fmt.Errorf("cmdsurface: mapping[%d] missing Name", i)
		}
		if seen[m.Name] {
			return fmt.Errorf("cmdsurface: duplicate mapping name %q", m.Name)
		}
		seen[m.Name] = true
		if m.Auth == nil {
			return fmt.Errorf("cmdsurface: mapping %q missing Auth", m.Name)
		}

		leaf, err := b.resolveLeaf(m.Path)
		if err != nil {
			return fmt.Errorf("cmdsurface: mapping %q: %w", m.Name, err)
		}
		if !leaf.Enabled[SurfaceWebhook] {
			return fmt.Errorf("cmdsurface: mapping %q: %w: %s on %s",
				m.Name, ErrSurfaceNotEnabled, leaf.PathKey(), SurfaceWebhook)
		}
		if leaf.Class.Destructive && !b.cfg.policy.Allowed(leaf.Class, SurfaceWebhook) {
			return fmt.Errorf("cmdsurface: mapping %q: %w: %s on %s",
				m.Name, ErrDestructiveBlocked, leaf.PathKey(), SurfaceWebhook)
		}
		if leaf.Class.AuthRequired {
			if _, isNone := m.Auth.(AuthNone); isNone {
				return fmt.Errorf("cmdsurface: mapping %q targets auth-required leaf with AuthNone", m.Name)
			}
		}
		if leaf.Class.RequiresConfirmation && !cfg.allowConfirmation {
			return fmt.Errorf("cmdsurface: mapping %q targets confirmation-required leaf without WithWebhookAllowConfirmation", m.Name)
		}

		flagTmpls := make(map[string]*template.Template, len(m.FlagMap))
		for k, src := range m.FlagMap {
			t, err := parseWebhookTemplate(m.Name+":flag:"+k, src)
			if err != nil {
				return fmt.Errorf("cmdsurface: mapping %q: %w", m.Name, err)
			}
			flagTmpls[k] = t
		}
		var argsTmpl *template.Template
		if m.ArgsTemplate != "" {
			t, err := parseWebhookTemplate(m.Name+":args", m.ArgsTemplate)
			if err != nil {
				return fmt.Errorf("cmdsurface: mapping %q: %w", m.Name, err)
			}
			argsTmpl = t
		}

		prepared = append(prepared, resolved{
			mapping:   m,
			leaf:      leaf,
			flagTmpls: flagTmpls,
			argsTmpl:  argsTmpl,
		})
	}

	for _, p := range prepared {
		path := cfg.prefix + "/" + p.mapping.Name
		handler := newWebhookHandler(b, p.leaf, p.mapping, p.flagTmpls, p.argsTmpl, cfg)
		r.Handle(http.MethodPost, path, handler)
	}
	return nil
}

// newWebhookHandler returns the http.HandlerFunc that drives one
// mapping end-to-end: body read → auth → template → invoke → log.
func newWebhookHandler(
	b *Bridge,
	leaf *Leaf,
	mapping WebhookMapping,
	flagTmpls map[string]*template.Template,
	argsTmpl *template.Template,
	cfg webhookConfig,
) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		// Read body with a hard cap. We read one byte beyond the cap
		// so we can distinguish "exactly at" from "over".
		body, oversize, err := readWebhookBody(r, cfg.maxBody)
		if err != nil {
			api.Error(w, http.StatusBadRequest, &api.APIError{
				Status:  http.StatusBadRequest,
				Code:    "bad_request",
				Message: err.Error(),
			})
			return
		}
		if oversize {
			api.Error(w, http.StatusRequestEntityTooLarge, &api.APIError{
				Status:  http.StatusRequestEntityTooLarge,
				Code:    "payload_too_large",
				Message: fmt.Sprintf("body exceeds %d bytes", cfg.maxBody),
			})
			return
		}

		if err := mapping.Auth.Verify(r, body); err != nil {
			api.Error(w, http.StatusUnauthorized, &api.APIError{
				Status:  http.StatusUnauthorized,
				Code:    "unauthorized",
				Message: err.Error(),
			})
			return
		}

		root := buildWebhookRoot(r, body)

		flags := make(map[string]any, len(flagTmpls))
		for k, t := range flagTmpls {
			val, err := execWebhookTemplate(t, root)
			if err != nil {
				api.Error(w, http.StatusBadRequest, &api.APIError{
					Status:  http.StatusBadRequest,
					Code:    "template_error",
					Message: err.Error(),
				})
				return
			}
			if val == "" {
				continue
			}
			flags[k] = val
		}
		var args []string
		if argsTmpl != nil {
			val, err := execWebhookTemplate(argsTmpl, root)
			if err != nil {
				api.Error(w, http.StatusBadRequest, &api.APIError{
					Status:  http.StatusBadRequest,
					Code:    "template_error",
					Message: err.Error(),
				})
				return
			}
			if val != "" {
				args = strings.Fields(val)
			}
		}

		inv := Invocation{
			Path:  append([]string(nil), leaf.Path...),
			Args:  args,
			Flags: flags,
			Meta: Meta{
				Surface:     SurfaceWebhook,
				Caller:      mapping.Name,
				TraceID:     r.Header.Get("X-Request-ID"),
				RequestedAt: time.Now(),
			},
		}

		res, err := b.Invoke(r.Context(), inv)
		if cfg.resultLog != nil {
			cfg.resultLog(mapping, res, err)
		}
		if err != nil {
			writeWebhookBridgeError(w, err)
			return
		}
		w.WriteHeader(http.StatusAccepted)
	}
}

// readWebhookBody reads up to max+1 bytes from r.Body and reports
// whether the cap was exceeded. The returned body slice is the
// first min(len, max) bytes; over=true indicates the body was
// truncated.
func readWebhookBody(r *http.Request, max int64) (body []byte, over bool, err error) {
	if r.Body == nil {
		return nil, false, nil
	}
	defer r.Body.Close()
	// Read one byte past the cap to detect overflow.
	lr := io.LimitReader(r.Body, max+1)
	buf, err := io.ReadAll(lr)
	if err != nil {
		return nil, false, err
	}
	if int64(len(buf)) > max {
		return buf[:max], true, nil
	}
	return buf, false, nil
}

// buildWebhookRoot assembles the {body, headers, query, path} root
// map a template executes against. body is JSON-decoded when the
// Content-Type indicates JSON and the decode succeeds; otherwise
// .body is nil. headers/query are flattened to first-value-per-key.
func buildWebhookRoot(r *http.Request, body []byte) map[string]any {
	var jsonBody map[string]any
	if isJSONContent(r.Header.Get("Content-Type")) && len(body) > 0 {
		_ = json.Unmarshal(body, &jsonBody) // nil on failure
	}

	headers := make(map[string]string, len(r.Header))
	for k, v := range r.Header {
		if len(v) > 0 {
			headers[k] = v[0]
		}
	}
	query := make(map[string]string)
	for k, v := range r.URL.Query() {
		if len(v) > 0 {
			query[k] = v[0]
		}
	}

	// .body is the decoded JSON object when present, otherwise an
	// empty map so templates referencing .body.<field> render the
	// stdlib "<no value>" sentinel rather than failing with a
	// nil-dereference. Semantically "no JSON body"; observably the
	// keys are all absent.
	bodyRoot := jsonBody
	if bodyRoot == nil {
		bodyRoot = map[string]any{}
	}
	return map[string]any{
		"body":    bodyRoot,
		"headers": headers,
		"query":   query,
		"path":    map[string]string{},
	}
}

// isJSONContent reports whether ct denotes a JSON body. Any media
// type whose subtype is "json" or ends in "+json" qualifies.
func isJSONContent(ct string) bool {
	// Strip parameters: "application/json; charset=utf-8" → "application/json".
	if i := strings.IndexByte(ct, ';'); i >= 0 {
		ct = ct[:i]
	}
	ct = strings.TrimSpace(strings.ToLower(ct))
	if ct == "" {
		return false
	}
	if ct == "application/json" || ct == "text/json" {
		return true
	}
	return strings.HasSuffix(ct, "+json")
}

// writeWebhookBridgeError maps bridge sentinel errors to the
// surface's HTTP contract. Anything unrecognized falls through to
// api.MapError so kit transport conventions remain uniform.
func writeWebhookBridgeError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, ErrUnknownCommand):
		api.Error(w, http.StatusInternalServerError, &api.APIError{
			Status:  http.StatusInternalServerError,
			Code:    "unknown_command",
			Message: err.Error(),
		})
	case errors.Is(err, ErrSurfaceNotEnabled):
		api.Error(w, http.StatusForbidden, &api.APIError{
			Status:  http.StatusForbidden,
			Code:    "not_enabled",
			Message: err.Error(),
		})
	case errors.Is(err, ErrDestructiveBlocked):
		api.Error(w, http.StatusForbidden, &api.APIError{
			Status:  http.StatusForbidden,
			Code:    "destructive_blocked",
			Message: err.Error(),
		})
	default:
		ae := api.MapError(err)
		api.Error(w, ae.Status, ae)
	}
}
