package provenance

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"reflect"
	"sort"
	"strconv"
	"strings"
	"sync/atomic"

	"gopkg.in/yaml.v3"
)

// Render serializes v to w in the requested format ("json" or "yaml"),
// with strict-mode checks applied per CurrentModeFromContext(ctx).
//
//   - ModeOff: plain json.Marshal / yaml.Marshal. No envelope.
//   - ModeWarn: emit envelope; log warnings to stderr on missing entries.
//   - ModeStrict: Verify first; on violation return the *output.Error
//     (caller emits via output.RenderError) and write nothing to w.
//
// The envelope shape:
//
//	{
//	  "data": <v>,
//	  "provenance": {
//	    "/users/0/email":   { "source": "authoritative", "url": "...", ... },
//	    "/users/0/cohort":  { "source": "cached",        "url": "...", ... }
//	  }
//	}
//
// The envelope key is "data" by default; configurable via
// SetEnvelopeKey. The Provenance block aggregates entries from the
// Tracker AND from wrapper-internal Provenance (NewSynthesized /
// NewCached populate the wrapper directly without a separate
// Tracker.Synthesize call — both are honored).
func Render(ctx context.Context, w io.Writer, format string, v any) error {
	mode := CurrentModeFromContext(ctx)
	if mode == ModeOff {
		return renderPlain(w, format, v)
	}
	tr := Track(ctx)
	provBlock, missing, invalid := buildEnvelope(v, tr)
	if mode == ModeStrict && (len(missing) > 0 || len(invalid) > 0) {
		return buildError(missing, invalid)
	}
	if mode == ModeWarn && (len(missing) > 0 || len(invalid) > 0) {
		emitWarning(missing, invalid)
	}
	env := envelope{
		Data:       v,
		Provenance: provBlock,
		key:        EnvelopeKey(),
	}
	return renderEnvelope(w, format, env)
}

// renderPlain bypasses the envelope. Used in ModeOff.
func renderPlain(w io.Writer, format string, v any) error {
	switch strings.ToLower(format) {
	case "yaml", "yml":
		return yaml.NewEncoder(w).Encode(v)
	default:
		enc := json.NewEncoder(w)
		enc.SetIndent("", "  ")
		return enc.Encode(v)
	}
}

// envelope is the wire shape for the strict/warn output. The key for
// the data slot is dynamic (EnvelopeKey), so we marshal via a custom
// MarshalJSON that emits the configured key.
type envelope struct {
	Data       any
	Provenance map[string]Provenance
	key        string
}

func (e envelope) MarshalJSON() ([]byte, error) {
	m := map[string]any{
		e.key:        e.Data,
		"provenance": e.Provenance,
	}
	return json.Marshal(m)
}

func (e envelope) MarshalYAML() (any, error) {
	return map[string]any{
		e.key:        e.Data,
		"provenance": e.Provenance,
	}, nil
}

func renderEnvelope(w io.Writer, format string, env envelope) error {
	switch strings.ToLower(format) {
	case "yaml", "yml":
		return yaml.NewEncoder(w).Encode(env)
	default:
		enc := json.NewEncoder(w)
		enc.SetIndent("", "  ")
		return enc.Encode(env)
	}
}

// buildEnvelope walks v collecting wrapper-internal Provenance,
// merges it with the Tracker's entries (Tracker wins on conflict),
// and returns (block, missing-paths, invalid-paths).
//
// Path matching follows the same JSON-pointer rules as Verify.
func buildEnvelope(v any, tr *Tracker) (map[string]Provenance, []string, []pathErr) {
	block := make(map[string]Provenance)
	// Seed with tracker entries first.
	for path, p := range tr.Snapshot() {
		block[path] = p
	}
	// Walk v to harvest wrapper-internal Provenance + detect missing.
	c := collector{tracker: tr, block: block}
	c.walk(reflect.ValueOf(v), "")
	sort.Strings(c.missing)
	sort.Slice(c.invalid, func(i, j int) bool { return c.invalid[i].path < c.invalid[j].path })
	return block, c.missing, c.invalid
}

type collector struct {
	tracker *Tracker
	block   map[string]Provenance
	missing []string
	invalid []pathErr
}

func (c *collector) walk(rv reflect.Value, path string) {
	if !rv.IsValid() {
		return
	}
	switch rv.Kind() {
	case reflect.Pointer, reflect.Interface:
		if rv.IsNil() {
			return
		}
		c.walk(rv.Elem(), path)
	case reflect.Struct:
		if checked := c.checkWrapper(rv, path); checked {
			return
		}
		t := rv.Type()
		for i := 0; i < rv.NumField(); i++ {
			ft := t.Field(i)
			if !ft.IsExported() {
				continue
			}
			seg, omit := jsonFieldName(ft)
			if omit {
				continue
			}
			c.walk(rv.Field(i), path+"/"+escapeJSONPointerToken(seg))
		}
	case reflect.Slice, reflect.Array:
		for i := 0; i < rv.Len(); i++ {
			c.walk(rv.Index(i), path+"/"+strconv.Itoa(i))
		}
	case reflect.Map:
		if rv.Type().Key().Kind() != reflect.String {
			return
		}
		iter := rv.MapRange()
		for iter.Next() {
			k := iter.Key().String()
			c.walk(iter.Value(), path+"/"+escapeJSONPointerToken(k))
		}
	}
}

func (c *collector) checkWrapper(rv reflect.Value, path string) bool {
	t := rv.Type()
	if !isWrapperType(t) {
		return false
	}
	provMethod := rv.MethodByName("Provenance")
	isSetMethod := rv.MethodByName("IsSet")
	if !provMethod.IsValid() || !isSetMethod.IsValid() {
		return false
	}
	provOut := provMethod.Call(nil)
	setOut := isSetMethod.Call(nil)
	prov, _ := provOut[0].Interface().(Provenance)
	isSet, _ := setOut[0].Interface().(bool)
	// If the tracker recorded an invalid entry here, surface it.
	if ierr, ok := c.tracker.invalidError(path); ok {
		c.invalid = append(c.invalid, pathErr{path: path, err: ierr})
		return true
	}
	if isSet && !prov.IsZero() {
		// Only fill in if the tracker did not already record one (the
		// tracker entry is presumed authoritative when both exist).
		if _, ok := c.block[path]; !ok {
			c.block[path] = prov
		}
		return true
	}
	if _, ok := c.block[path]; ok {
		return true
	}
	c.missing = append(c.missing, path)
	return true
}

func emitWarning(missing []string, invalid []pathErr) {
	if len(missing) == 0 && len(invalid) == 0 {
		return
	}
	var b strings.Builder
	b.WriteString("provenance: warn-mode: ")
	if len(missing) > 0 {
		fmt.Fprintf(&b, "%d missing (%s)", len(missing), strings.Join(missing, ", "))
	}
	if len(invalid) > 0 {
		if len(missing) > 0 {
			b.WriteString("; ")
		}
		items := make([]string, len(invalid))
		for i, ie := range invalid {
			items[i] = fmt.Sprintf("%s: %v", ie.path, ie.err)
		}
		fmt.Fprintf(&b, "%d invalid (%s)", len(invalid), strings.Join(items, ", "))
	}
	b.WriteString("\n")
	_, _ = io.WriteString(os.Stderr, b.String())
}

// envelopeKey holds the data-slot key. atomic.Value so SetEnvelopeKey
// is safe for init() races between adopters.
var envelopeKey atomic.Value // string

// EnvelopeKey returns the data-slot key (default: "data").
func EnvelopeKey() string {
	v := envelopeKey.Load()
	if v == nil {
		return "data"
	}
	s, ok := v.(string)
	if !ok || s == "" {
		return "data"
	}
	return s
}

// SetEnvelopeKey overrides the data-slot key. Adopters with an existing
// top-level "data:" envelope can call this from init() with "result"
// or any unique string.
func SetEnvelopeKey(k string) {
	if k == "" {
		k = "data"
	}
	envelopeKey.Store(k)
}
