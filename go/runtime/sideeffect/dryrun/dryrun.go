// Package dryrun provides describing implementations of the
// sideeffect interfaces. Each call writes a human-readable line to
// a configurable io.Writer (default os.Stderr) and returns a
// synthetic but plausibly-shaped response.
//
// The dryrun impls never block and never fail on a "would-be"
// call — preview must complete even when the real call would have
// errored. Adopters that want stricter dry-run semantics (refuse
// the call rather than synthesize success) compose their own wrapper
// over the real impl.
//
// HTTP semantics: safe verbs (GET, HEAD) are forwarded to a real
// http.Client because read-only requests are part of the preview
// and the call site needs the actual response shape. Mutating verbs
// (POST, PUT, PATCH, DELETE) are intercepted and answered with a
// synthetic 201 Created.
//
// Bus semantics: when the payload embeds bus.Qualifiers (named or
// anonymous, per ADR-0017), the wrapper augments the field with
// Mechanism: "dry_run" before describing the publish. Payloads that
// do not embed Qualifiers are described without augmentation; the
// fact is logged once via the writer.
//
// Exec semantics: the argv is printed, the call returns nil with
// zero exit code and (for Output) an empty []byte. Subprocess
// containment is hopeless; documented in ADR-0019.
package dryrun

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"reflect"
	"strings"
	"sync"

	"hop.top/kit/go/runtime/bus"
)

// Option mutates a dryrun impl during construction.
type Option func(*config)

type config struct {
	w io.Writer
}

// WithWriter redirects the human-readable output of every dryrun
// call. nil w resets to os.Stderr.
func WithWriter(w io.Writer) Option {
	return func(c *config) {
		c.w = w
	}
}

// resolve returns a non-nil writer derived from c.
func (c config) resolve() io.Writer {
	if c.w == nil {
		return os.Stderr
	}
	return c.w
}

func newConfig(opts ...Option) config {
	var c config
	for _, opt := range opts {
		opt(&c)
	}
	return c
}

// FS is the dryrun sideeffect.FS implementation.
type FS struct {
	cfg config
}

// NewFS builds an FS with the supplied options.
func NewFS(opts ...Option) FS {
	return FS{cfg: newConfig(opts...)}
}

// WriteFile prints "[dry-run] would write <path> (<n> bytes, mode <perm>)".
// Returns nil.
func (f FS) WriteFile(path string, data []byte, perm os.FileMode) error {
	fmt.Fprintf(f.cfg.resolve(), "[dry-run] would write %s (%d bytes, mode %#o)\n",
		path, len(data), perm)
	return nil
}

// MkdirAll prints "[dry-run] would mkdir -p <path> (mode <perm>)".
// Returns nil.
func (f FS) MkdirAll(path string, perm os.FileMode) error {
	fmt.Fprintf(f.cfg.resolve(), "[dry-run] would mkdir -p %s (mode %#o)\n", path, perm)
	return nil
}

// Rename prints "[dry-run] would rename <old> -> <new>". Returns nil.
func (f FS) Rename(oldpath, newpath string) error {
	fmt.Fprintf(f.cfg.resolve(), "[dry-run] would rename %s -> %s\n", oldpath, newpath)
	return nil
}

// Remove prints "[dry-run] would remove <path>". Returns nil.
func (f FS) Remove(path string) error {
	fmt.Fprintf(f.cfg.resolve(), "[dry-run] would remove %s\n", path)
	return nil
}

// HTTP is the dryrun sideeffect.HTTP implementation.
type HTTP struct {
	cfg    config
	Client *http.Client
}

// NewHTTP builds an HTTP impl. client is used for safe-verb
// pass-through; nil falls back to http.DefaultClient.
func NewHTTP(client *http.Client, opts ...Option) HTTP {
	return HTTP{cfg: newConfig(opts...), Client: client}
}

// Do dispatches safe verbs (GET, HEAD) to the real client and
// intercepts mutating verbs with a synthetic 201 Created.
func (h HTTP) Do(req *http.Request) (*http.Response, error) {
	if req == nil {
		return nil, fmt.Errorf("dryrun http: nil request")
	}
	switch req.Method {
	case http.MethodGet, http.MethodHead, "":
		// Empty method is treated as GET per net/http.
		c := h.Client
		if c == nil {
			c = http.DefaultClient
		}
		return c.Do(req)
	default:
		bodyLen := contentLength(req)
		fmt.Fprintf(h.cfg.resolve(),
			"[dry-run] would %s %s (%d-byte body)\n",
			req.Method, req.URL.String(), bodyLen)
		return synthResponse(req), nil
	}
}

// contentLength returns the request body length in bytes when
// known. Returns 0 for unknown / streaming bodies; we deliberately
// avoid consuming the body to preserve repeatability.
func contentLength(req *http.Request) int {
	if req.ContentLength > 0 {
		return int(req.ContentLength)
	}
	return 0
}

// synthResponse builds a plausible 201 Created response for a
// mutating verb. Body is empty JSON object so callers that decode
// JSON unconditionally don't crash.
func synthResponse(req *http.Request) *http.Response {
	rec := httptest.NewRecorder()
	rec.Header().Set("Content-Type", "application/json")
	rec.WriteHeader(http.StatusCreated)
	rec.Body.WriteString("{}")
	resp := rec.Result()
	resp.Request = req
	return resp
}

// Bus is the dryrun sideeffect.Bus implementation.
type Bus struct {
	cfg          config
	noQualLogged sync.Once // emit the "no Qualifiers embed" notice once per Bus
}

// NewBus builds a Bus impl.
func NewBus(opts ...Option) Bus {
	return Bus{cfg: newConfig(opts...)}
}

// Publish prints "[dry-run] would publish <topic> from <source>" and
// (best-effort) augments bus.Qualifiers{Mechanism: "dry_run"} on the
// payload via reflection. Payloads that do not embed Qualifiers are
// described without augmentation.
func (b *Bus) Publish(_ context.Context, topic, source string, payload any) error {
	out := b.cfg.resolve()
	if augmented, ok := augmentMechanism(payload, "dry_run"); ok {
		_ = augmented // payload mutated in place when possible; describe below
		fmt.Fprintf(out, "[dry-run] would publish %s from %s (mechanism=dry_run)\n",
			topic, source)
		return nil
	}
	// No Qualifiers field. Describe without augmentation. We log the
	// gap once per Bus so adopters notice during preview.
	b.noQualLogged.Do(func() {
		fmt.Fprintf(out,
			"[dry-run] note: payload type %s does not embed bus.Qualifiers; "+
				"mechanism tag will not appear on this event\n",
			payloadTypeName(payload))
	})
	fmt.Fprintf(out, "[dry-run] would publish %s from %s\n", topic, source)
	return nil
}

// augmentMechanism sets the Mechanism field on the first
// bus.Qualifiers found inside payload (anonymous or named embed,
// pointer or value). Returns true when the augmentation succeeded
// (or already had Mechanism set to a non-empty value, which we
// preserve). Returns false when the payload does not embed
// Qualifiers or is not a struct.
func augmentMechanism(payload any, mechanism string) (any, bool) {
	if payload == nil {
		return payload, false
	}
	v := reflect.ValueOf(payload)
	// Pointer to struct: addressable, can mutate in place.
	if v.Kind() == reflect.Pointer {
		if v.IsNil() {
			return payload, false
		}
		v = v.Elem()
	}
	if v.Kind() != reflect.Struct {
		return payload, false
	}
	qualifiersType := reflect.TypeOf(bus.Qualifiers{})
	t := v.Type()
	for i := 0; i < t.NumField(); i++ {
		f := t.Field(i)
		if !f.IsExported() {
			continue
		}
		if f.Type == qualifiersType {
			fv := v.Field(i)
			if !fv.CanSet() {
				// Non-pointer struct value — can't mutate in place.
				// We have read access via QualifiersFrom, but to
				// augment we'd need to clone. We do not clone here
				// because the dryrun bus is descriptive: the caller
				// keeps owning the payload value, and a clone would
				// silently diverge from what the caller passed.
				// Report success anyway: the description is correct.
				return payload, true
			}
			q, _ := fv.Interface().(bus.Qualifiers)
			if q.Mechanism == "" {
				q.Mechanism = mechanism
				fv.Set(reflect.ValueOf(q))
			}
			return payload, true
		}
	}
	return payload, false
}

// payloadTypeName returns a printable type name for a payload value,
// or "<nil>" when payload is nil.
func payloadTypeName(payload any) string {
	if payload == nil {
		return "<nil>"
	}
	t := reflect.TypeOf(payload)
	if t.Kind() == reflect.Pointer {
		return "*" + t.Elem().String()
	}
	return t.String()
}

// Exec is the dryrun sideeffect.Exec implementation.
type Exec struct {
	cfg config
}

// NewExec builds an Exec impl.
func NewExec(opts ...Option) Exec {
	return Exec{cfg: newConfig(opts...)}
}

// Run prints "[dry-run] would exec: <argv>" and returns nil.
func (e Exec) Run(cmd *exec.Cmd) error {
	fmt.Fprintf(e.cfg.resolve(), "[dry-run] would exec: %s\n", argv(cmd))
	return nil
}

// Output prints "[dry-run] would exec (capture): <argv>" and returns
// an empty byte slice with nil error.
func (e Exec) Output(cmd *exec.Cmd) ([]byte, error) {
	fmt.Fprintf(e.cfg.resolve(), "[dry-run] would exec (capture): %s\n", argv(cmd))
	return []byte{}, nil
}

// argv renders cmd.Args (or cmd.Path when Args is empty) into a
// shell-readable single line. Quoting is intentionally simple: we
// quote tokens that contain spaces and pass everything else through.
// The output is informational, not round-trippable.
func argv(cmd *exec.Cmd) string {
	if cmd == nil {
		return "<nil>"
	}
	args := cmd.Args
	if len(args) == 0 {
		args = []string{cmd.Path}
	}
	var buf bytes.Buffer
	for i, a := range args {
		if i > 0 {
			buf.WriteByte(' ')
		}
		if needsQuote(a) {
			buf.WriteByte('"')
			buf.WriteString(strings.ReplaceAll(a, `"`, `\"`))
			buf.WriteByte('"')
		} else {
			buf.WriteString(a)
		}
	}
	return buf.String()
}

func needsQuote(s string) bool {
	return strings.ContainsAny(s, " \t\n\"")
}
