package observability

import (
	"bytes"
	"cmp"
	"io"
	"maps"
	"math"
	"net/http"
	"slices"
	"strconv"
	"strings"

	"go.opentelemetry.io/otel/attribute"
	sdkmetric "go.opentelemetry.io/otel/sdk/metric"
	"go.opentelemetry.io/otel/sdk/metric/metricdata"
)

// ScrapeContentType is the media type of the scrape endpoint's body:
// the Prometheus text exposition format, version 0.0.4, which every
// Prometheus-compatible scraper reads.
const ScrapeContentType = "text/plain; version=0.0.4; charset=utf-8"

// scrapeHandler answers a scrape with the text exposition of what
// reader has aggregated since the Provider started.
//
// It is a small encoder over the SDK's own reader rather than the
// OpenTelemetry Prometheus exporter, which would link the Prometheus
// client library (its registry, protobuf model and HTTP handler) into
// every tool that links this package, for a format this file writes
// in a few hundred lines. Names follow the OpenTelemetry-to-Prometheus
// compatibility rules the exporter applies, so dashboards written
// against either read the same series: dots become underscores, a
// unit becomes a suffix (s → _seconds, By → _bytes), a monotonic sum
// gains _total, and each series carries otel_scope_name.
type scrapeHandler struct {
	reader *sdkmetric.ManualReader
}

func (h scrapeHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	var rm metricdata.ResourceMetrics
	if err := h.reader.Collect(r.Context(), &rm); err != nil {
		http.Error(w, http.StatusText(http.StatusInternalServerError), http.StatusInternalServerError)
		return
	}
	var buf bytes.Buffer
	writeExposition(&buf, &rm)
	w.Header().Set("Content-Type", ScrapeContentType)
	w.Header().Set("Content-Length", strconv.Itoa(buf.Len()))
	w.WriteHeader(http.StatusOK)
	if r.Method != http.MethodHead {
		_, _ = buf.WriteTo(w)
	}
}

// family is one metric family of the exposition: every series that
// shares a name, whichever instrumentation scope recorded it.
type family struct {
	name, help, typ string
	series          []series
}

// series is one data point, rendered: its sorting key and its lines.
type series struct {
	key   string
	lines string
}

// writeExposition renders rm in the text exposition format: families
// sorted by name, series within one sorted by their labels, so two
// scrapes of the same state are byte-identical.
func writeExposition(w io.Writer, rm *metricdata.ResourceMetrics) {
	families := map[string]*family{}
	add := func(name, help, typ string, s series) {
		f, ok := families[name]
		if !ok {
			f = &family{name: name, help: help, typ: typ}
			families[name] = f
		}
		if f.typ != typ {
			// Two instruments whose names collide once translated,
			// with different kinds: a family has one type, so the
			// later one is left out rather than corrupt the scrape.
			return
		}
		if f.help == "" {
			f.help = help
		}
		f.series = append(f.series, s)
	}

	for _, sm := range rm.ScopeMetrics {
		scope := []label{{"otel_scope_name", sm.Scope.Name}}
		if sm.Scope.Version != "" {
			scope = append(scope, label{"otel_scope_version", sm.Scope.Version})
		}
		for _, m := range sm.Metrics {
			addMetric(add, m, scope)
		}
	}

	var b strings.Builder
	if rm.Resource != nil && rm.Resource.Len() > 0 {
		b.WriteString("# HELP target_info Target metadata\n# TYPE target_info gauge\ntarget_info")
		writeLabels(&b, labelsOf(*rm.Resource.Set(), nil), nil)
		b.WriteString(" 1\n")
	}
	for _, name := range slices.Sorted(maps.Keys(families)) {
		f := families[name]
		slices.SortStableFunc(f.series, func(a, b series) int { return cmp.Compare(a.key, b.key) })
		if f.help != "" {
			b.WriteString("# HELP " + f.name + " " + escapeHelp(f.help) + "\n")
		}
		b.WriteString("# TYPE " + f.name + " " + f.typ + "\n")
		for _, s := range f.series {
			b.WriteString(s.lines)
		}
	}
	_, _ = io.WriteString(w, b.String())
}

// addMetric renders every data point of m into its family.
func addMetric(add func(name, help, typ string, s series), m metricdata.Metrics, scope []label) {
	base := metricName(m.Name, m.Unit)
	switch d := m.Data.(type) {
	case metricdata.Sum[int64]:
		name, typ := sumFamily(base, d.IsMonotonic)
		for _, dp := range d.DataPoints {
			add(name, m.Description, typ, point(name, dp.Attributes, scope, strconv.FormatInt(dp.Value, 10)))
		}
	case metricdata.Sum[float64]:
		name, typ := sumFamily(base, d.IsMonotonic)
		for _, dp := range d.DataPoints {
			add(name, m.Description, typ, point(name, dp.Attributes, scope, formatFloat(dp.Value)))
		}
	case metricdata.Gauge[int64]:
		for _, dp := range d.DataPoints {
			add(base, m.Description, "gauge", point(base, dp.Attributes, scope, strconv.FormatInt(dp.Value, 10)))
		}
	case metricdata.Gauge[float64]:
		for _, dp := range d.DataPoints {
			add(base, m.Description, "gauge", point(base, dp.Attributes, scope, formatFloat(dp.Value)))
		}
	case metricdata.Histogram[int64]:
		for _, dp := range d.DataPoints {
			add(base, m.Description, "histogram", histogram(base, dp.Attributes, scope,
				dp.Bounds, dp.BucketCounts, dp.Count, strconv.FormatInt(dp.Sum, 10)))
		}
	case metricdata.Histogram[float64]:
		for _, dp := range d.DataPoints {
			add(base, m.Description, "histogram", histogram(base, dp.Attributes, scope,
				dp.Bounds, dp.BucketCounts, dp.Count, formatFloat(dp.Sum)))
		}
	}
	// Exponential histograms and summaries have no text-format
	// counterpart; the SDK's default aggregations produce neither.
}

// sumFamily names and types a sum: a monotonic sum is a counter and
// ends in _total, any other sum is a gauge.
func sumFamily(base string, monotonic bool) (name, typ string) {
	if !monotonic {
		return base, "gauge"
	}
	if strings.HasSuffix(base, "_total") {
		return base, "counter"
	}
	return base + "_total", "counter"
}

// point renders one sample line.
func point(name string, attrs attribute.Set, scope []label, value string) series {
	ls := labelsOf(attrs, scope)
	var b strings.Builder
	b.WriteString(name)
	writeLabels(&b, ls, nil)
	b.WriteString(" " + value + "\n")
	return series{key: labelKey(ls), lines: b.String()}
}

// histogram renders one histogram data point: a cumulative _bucket
// line per bound and +Inf, then _sum and _count.
func histogram(name string, attrs attribute.Set, scope []label, bounds []float64, counts []uint64, count uint64, sum string) series {
	ls := labelsOf(attrs, scope)
	var b strings.Builder
	var cum uint64
	for i, bound := range bounds {
		if i < len(counts) {
			cum += counts[i]
		}
		b.WriteString(name + "_bucket")
		writeLabels(&b, ls, &label{"le", formatFloat(bound)})
		b.WriteString(" " + strconv.FormatUint(cum, 10) + "\n")
	}
	b.WriteString(name + "_bucket")
	writeLabels(&b, ls, &label{"le", "+Inf"})
	b.WriteString(" " + strconv.FormatUint(count, 10) + "\n")
	b.WriteString(name + "_sum")
	writeLabels(&b, ls, nil)
	b.WriteString(" " + sum + "\n")
	b.WriteString(name + "_count")
	writeLabels(&b, ls, nil)
	b.WriteString(" " + strconv.FormatUint(count, 10) + "\n")
	return series{key: labelKey(ls), lines: b.String()}
}

type label struct{ name, value string }

// labelsOf translates attrs to labels, sorted by name, followed by
// the scope's. Two attribute keys that translate to the same label
// name have their values joined with ";", as the compatibility rules
// specify, so the series stays valid.
func labelsOf(attrs attribute.Set, scope []label) []label {
	var out []label
	at := map[string]int{}
	iter := attrs.Iter()
	for iter.Next() {
		kv := iter.Attribute()
		name := labelName(string(kv.Key))
		value := kv.Value.Emit()
		if i, dup := at[name]; dup {
			out[i].value += ";" + value
			continue
		}
		at[name] = len(out)
		out = append(out, label{name, value})
	}
	slices.SortStableFunc(out, func(a, b label) int { return cmp.Compare(a.name, b.name) })
	return append(out, scope...)
}

// writeLabels writes {a="1",b="2"} with extra last, or nothing when
// there are no labels at all.
func writeLabels(b *strings.Builder, ls []label, extra *label) {
	if len(ls) == 0 && extra == nil {
		return
	}
	b.WriteByte('{')
	for i, l := range ls {
		if i > 0 {
			b.WriteByte(',')
		}
		b.WriteString(l.name + `="` + escapeLabelValue(l.value) + `"`)
	}
	if extra != nil {
		if len(ls) > 0 {
			b.WriteByte(',')
		}
		b.WriteString(extra.name + `="` + escapeLabelValue(extra.value) + `"`)
	}
	b.WriteByte('}')
}

// labelKey orders series within a family.
func labelKey(ls []label) string {
	var b strings.Builder
	for _, l := range ls {
		b.WriteString(l.name + "\x00" + l.value + "\x00")
	}
	return b.String()
}

// unitSuffixes are the units whose Prometheus base-unit name is a
// metric name suffix. A unit in braces ({request}) is an annotation,
// not a unit, and adds nothing; so does any unit not listed here.
var unitSuffixes = map[string]string{
	"s":  "seconds",
	"ms": "milliseconds",
	"us": "microseconds",
	"ns": "nanoseconds",
	"By": "bytes",
}

// metricName translates an instrument name and unit to a Prometheus
// metric name: http.server.request.duration in s becomes
// http_server_request_duration_seconds.
func metricName(name, unit string) string {
	n := sanitize(name, true)
	if suffix, ok := unitSuffixes[unit]; ok && !strings.HasSuffix(n, "_"+suffix) {
		n += "_" + suffix
	}
	return n
}

// labelName translates an attribute key to a label name. A name the
// exposition reserves (a leading "__") or cannot start with (a digit)
// gains a "key_" prefix.
func labelName(key string) string {
	n := sanitize(key, false)
	if strings.HasPrefix(n, "__") || (n != "" && n[0] >= '0' && n[0] <= '9') {
		n = "key_" + strings.TrimLeft(n, "_")
	}
	return n
}

// sanitize replaces every character the exposition does not allow in
// a name with "_", collapsing runs of them. Metric names may also hold
// ":"; label names may not. A metric name may not start with a digit.
func sanitize(s string, colon bool) string {
	var b strings.Builder
	under := false
	for i := 0; i < len(s); i++ {
		c := s[i]
		ok := c == '_' || (c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z') ||
			(c >= '0' && c <= '9') || (colon && c == ':')
		if !ok || c == '_' {
			if !under {
				b.WriteByte('_')
			}
			under = true
			continue
		}
		under = false
		b.WriteByte(c)
	}
	n := b.String()
	if colon && n != "" && n[0] >= '0' && n[0] <= '9' {
		n = "_" + n
	}
	return n
}

// formatFloat writes a sample value or bound the way the exposition
// spells it.
func formatFloat(f float64) string {
	switch {
	case math.IsInf(f, 1):
		return "+Inf"
	case math.IsInf(f, -1):
		return "-Inf"
	case math.IsNaN(f):
		return "NaN"
	}
	return strconv.FormatFloat(f, 'g', -1, 64)
}

var (
	helpEscaper  = strings.NewReplacer(`\`, `\\`, "\n", `\n`)
	valueEscaper = strings.NewReplacer(`\`, `\\`, "\n", `\n`, `"`, `\"`)
)

func escapeHelp(s string) string       { return helpEscaper.Replace(s) }
func escapeLabelValue(s string) string { return valueEscaper.Replace(s) }
