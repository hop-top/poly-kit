package output

import "time"

// Metadata is the provenance envelope attached to outputs that originate
// from upstream APIs, caches, LLMs, or derived state. It declares where
// the data came from and how it was retrieved so adopters can show
// trust signals without re-deriving them at the call site.
//
// Required fields: Source, FetchedAt, Method. Optional fields stay zero
// in the envelope when unset (Cached/CacheAge/Confidence are encoded
// per their JSON/YAML tags below).
type Metadata struct {
	Source     string        `json:"source"                 yaml:"source"`
	FetchedAt  time.Time     `json:"fetched_at"             yaml:"fetched_at"`
	Method     string        `json:"method"                 yaml:"method"`
	Cached     bool          `json:"cached"                 yaml:"cached"`
	CacheAge   time.Duration `json:"cache_age,omitempty"    yaml:"cache_age,omitempty"`
	Confidence *float64      `json:"confidence,omitempty"   yaml:"confidence,omitempty"`
}

// RenderOption mutates the per-call Render configuration. Options are
// variadic on Render so existing call sites compile unchanged.
type RenderOption func(*renderConfig)

// renderConfig is the resolved set of Render options for a single call.
type renderConfig struct {
	provenance  *Metadata
	tableStyle  *TableStyle
	rowEmphasis map[int]EmphasisKind
}

// WithProvenance attaches a Metadata envelope to the rendered output.
//
// For JSON and YAML formats, the rendered payload is wrapped in
// {"data": <v>, "_meta": <Metadata>}. For Table format, provenance is
// printed as a single trailing footer line on stderr (no column space
// is consumed in the table itself).
//
// Adopters opt in explicitly — Render never auto-attaches provenance,
// because the package can't tell which calls touched the network.
func WithProvenance(m Metadata) RenderOption {
	return func(c *renderConfig) { c.provenance = &m }
}

// CachedFromMetadata returns a copy of m with Cached set to true and
// CacheAge computed as time.Since(fetchedAt). Adopters with cache
// layers use this so they don't repeat the now()-arithmetic at every
// call site.
func CachedFromMetadata(m Metadata, fetchedAt time.Time) Metadata {
	m.Cached = true
	m.CacheAge = time.Since(fetchedAt)
	return m
}
