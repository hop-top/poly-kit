package provenance

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"sort"
	"strings"

	"hop.top/kit/go/console/output"
)

// TB is the minimal subset of testing.TB used by the harness assert
// helpers. Defined here (rather than importing testing) so non-test
// adopters of the kit's conformance harness can supply their own
// fixture types without depending on the testing package.
type TB interface {
	Helper()
	Errorf(format string, args ...any)
	Fatalf(format string, args ...any)
}

// AssertProvenanceComplete is the kit-conformance harness primitive
// for Layer B integration tests. It walks v finding every Synthesized
// / Cached wrapper and asserts each has a populated Provenance
// (either from a wrapper constructor or from the Tracker on ctx).
//
// On any missing or invalid path, t.Errorf is called once per issue
// and the helper returns. Reports the full sorted path list for
// reproducibility.
//
// The implementation is intentionally split between this package and
// the harness package: harness re-exports this function under
// its own name (e.g., harness.AssertProvenanceComplete) so adopters
// import a single package; the implementation lives here because the
// kit/provenance package owns the wire contract.
func AssertProvenanceComplete(t TB, ctx context.Context, v any) {
	t.Helper()
	if err := Verify(ctx, v); err != nil {
		// Surface the Cause slot (the JSON-pointer list) directly so
		// the test reporter sees which paths are missing without
		// having to crack open the Error envelope.
		detail := err.Error()
		var oe *output.Error
		if errors.As(err, &oe) && oe.Cause != "" {
			detail = fmt.Sprintf("%s (paths: %s)", err.Error(), oe.Cause)
		}
		t.Errorf("AssertProvenanceComplete: %s", detail)
	}
}

// CassetteEntry is the minimal record the cassette cross-check
// consumes. The harness package's xrr cassette type maps onto
// this via a small adapter. Defined here so this package does not take
// a dependency on the harness package (and so harness can import this
// package, not the other way around).
type CassetteEntry struct {
	// URL is the upstream URL recorded by the xrr cassette. Compared
	// against the wrapper / Tracker URL after Normalize.
	URL string
	// Method is the HTTP method ("GET", "POST", ...) when applicable.
	// Empty for non-HTTP cassettes.
	Method string
}

// AssertProvenanceMatchesCassette cross-checks every wrapper's declared
// Provenance.URL against the closest matching CassetteEntry. The
// comparison applies Normalize to both sides.
//
// Match rules:
//   - For each wrapper with a non-empty Provenance.URL, there must be
//     at least one cassette entry whose normalised URL equals the
//     wrapper's normalised URL.
//   - Wrappers with Provenance.Source == SourceDefaulted or
//     SourceInferred without a URL are skipped (nothing to match).
//   - Cassette entries with no matching wrapper are NOT reported
//     (cassettes can carry extra recordings, e.g., follow-up requests
//     the wrapper-stamped fetch did not surface).
//
// On any unmatched wrapper, t.Errorf reports the wrapper path + URL
// and the closest cassette URLs (Levenshtein-free; we just list the
// known URLs for context).
func AssertProvenanceMatchesCassette(t TB, ctx context.Context, v any, entries []CassetteEntry) {
	t.Helper()

	normalised := make(map[string]struct{}, len(entries))
	for _, e := range entries {
		if e.URL == "" {
			continue
		}
		n, err := Normalize(e.URL)
		if err != nil {
			continue
		}
		normalised[n] = struct{}{}
	}

	var records []wrapperURL

	tr := Track(ctx)
	rv := reflect.ValueOf(v)
	collectURLs(rv, "", tr, &records)
	sort.Slice(records, func(i, j int) bool { return records[i].path < records[j].path })

	var unmatched []wrapperURL
	for _, r := range records {
		if r.url == "" {
			continue
		}
		n, err := Normalize(r.url)
		if err != nil {
			t.Errorf("AssertProvenanceMatchesCassette: invalid URL at %s: %v", r.path, err)
			continue
		}
		if _, ok := normalised[n]; !ok {
			unmatched = append(unmatched, r)
		}
	}
	if len(unmatched) == 0 {
		return
	}
	known := make([]string, 0, len(normalised))
	for k := range normalised {
		known = append(known, k)
	}
	sort.Strings(known)
	for _, r := range unmatched {
		t.Errorf("AssertProvenanceMatchesCassette: wrapper at %s declared URL %q has no cassette match (known: %s)",
			r.path, r.url, strings.Join(known, ", "))
	}
}

// collectURLs walks v gathering wrapper URLs (from either the
// wrapper's own Provenance or the tracker's matching entry).
func collectURLs(rv reflect.Value, path string, tr *Tracker, out *[]wrapperURL) {
	c := urlCollector{tracker: tr, out: out}
	c.walk(rv, path)
}

type wrapperURL = struct {
	path string
	url  string
}

type urlCollector struct {
	tracker *Tracker
	out     *[]wrapperURL
}

func (c *urlCollector) walk(rv reflect.Value, path string) {
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
		if c.checkWrapper(rv, path) {
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
			c.walk(rv.Index(i), fmt.Sprintf("%s/%d", path, i))
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

func (c *urlCollector) checkWrapper(rv reflect.Value, path string) bool {
	t := rv.Type()
	if !isWrapperType(t) {
		return false
	}
	provMethod := rv.MethodByName("Provenance")
	if !provMethod.IsValid() {
		return false
	}
	prov, ok := provMethod.Call(nil)[0].Interface().(Provenance)
	if !ok {
		return false
	}
	url := prov.URL
	if url == "" {
		if tp, ok := c.tracker.Lookup(path); ok {
			url = tp.URL
		}
	}
	*c.out = append(*c.out, wrapperURL{path: path, url: url})
	return true
}

// MarshalSnapshotJSON is a small convenience for tests that want to
// inspect a Tracker's recorded state via golden-file comparison.
func (t *Tracker) MarshalSnapshotJSON() ([]byte, error) {
	snap := t.Snapshot()
	// Sort by key for stable output.
	keys := make([]string, 0, len(snap))
	for k := range snap {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	ordered := make([]map[string]any, 0, len(keys))
	for _, k := range keys {
		ordered = append(ordered, map[string]any{"path": k, "provenance": snap[k]})
	}
	return json.Marshal(ordered)
}
