package provenance

import (
	"encoding/json"
	"fmt"
	"time"
)

// SchemaVersion is the on-wire version of the Provenance struct. Bumped
// per Factor 12 when fields are added or semantics change. v1 always
// emits "1".
const SchemaVersion = "1"

// SourceTier is the four-tier taxonomy of where a value came from.
// String-typed so the JSON wire format is human-readable and so future
// tiers can be added without breaking integer-enum consumers.
type SourceTier string

const (
	// SourceAuthoritative is the rare explicit-record case for plain
	// values that round-trip provenance. The plain T (no wrapper) case
	// carries no Provenance; this constant exists for adopters who
	// want to be explicit.
	SourceAuthoritative SourceTier = "authoritative"

	// SourceCached marks a value served from a cache layer rather than
	// freshly fetched. Use with Cached[T].
	SourceCached SourceTier = "cached"

	// SourceInferred marks a value the tool derived from other inputs
	// (e.g., an LLM judge, a heuristic, a join of two upstream values).
	// Use with Synthesized[T].
	SourceInferred SourceTier = "inferred"

	// SourceDefaulted marks a value the tool made up from nothing
	// (e.g., a config default). Use with Synthesized[T]. FetchedAt
	// MAY be zero for defaulted records.
	SourceDefaulted SourceTier = "defaulted"
)

// Provenance is the metadata that travels with a Synthesized[T] or
// Cached[T] value. Fields named to align with OpenTelemetry semantic
// conventions where possible (Source.URL, FetchedAt) and HTTP cache
// headers where not (Age is derived from FetchedAt, not stored).
type Provenance struct {
	// SchemaVersion of this Provenance record. Always set; v1 = "1".
	SchemaVersion string `json:"schema_version" yaml:"schema_version"`

	// Source is the SourceTier of this record.
	Source SourceTier `json:"source" yaml:"source"`

	// URL is the canonical identifier of the upstream that returned
	// the value (for cached/authoritative) or the algorithm that
	// derived it (for inferred). Free-form string; "doc://..." or
	// "exec://..." pointers are fine. Empty for defaulted.
	URL string `json:"url,omitempty" yaml:"url,omitempty"`

	// FetchedAt is when the value was retrieved from URL (for
	// cached/authoritative) or computed (for inferred/defaulted).
	// Time zone is preserved; RFC 3339 with nanoseconds on the wire.
	FetchedAt time.Time `json:"fetched_at,omitempty" yaml:"fetched_at,omitempty"`

	// Version is the upstream-provided version of the value, if any.
	// Common examples: a git SHA, a content-hash, an ETag, an upstream
	// epoch integer. Empty when not known.
	Version string `json:"version,omitempty" yaml:"version,omitempty"`

	// DerivedFrom is the provenance chain when a value was synthesized
	// from other tracked values. Inferred-from-cached cases populate
	// this with the cached input's Provenance. v1 producers MAY leave
	// it nil.
	DerivedFrom []Provenance `json:"derived_from,omitempty" yaml:"derived_from,omitempty"`
}

// Validate checks the Provenance for structural sanity. Validation
// rules (per design §1):
//
//   - SchemaVersion must be set and known.
//   - Source must be one of the four constants.
//   - For non-defaulted tiers, at least one of URL or Version must
//     be set; an inferred value with neither is suspect.
//   - FetchedAt is non-zero for cached / authoritative.
func (p Provenance) Validate() error {
	if p.SchemaVersion == "" {
		return fmt.Errorf("provenance: SchemaVersion empty")
	}
	if p.SchemaVersion != SchemaVersion {
		return fmt.Errorf("provenance: unknown SchemaVersion %q (want %q)", p.SchemaVersion, SchemaVersion)
	}
	switch p.Source {
	case SourceAuthoritative, SourceCached, SourceInferred, SourceDefaulted:
	case "":
		return fmt.Errorf("provenance: Source unset")
	default:
		return fmt.Errorf("provenance: unknown Source %q", p.Source)
	}
	if p.Source != SourceDefaulted {
		if p.URL == "" && p.Version == "" {
			return fmt.Errorf("provenance: %s requires URL or Version", p.Source)
		}
	}
	switch p.Source {
	case SourceCached, SourceAuthoritative:
		if p.FetchedAt.IsZero() {
			return fmt.Errorf("provenance: %s requires non-zero FetchedAt", p.Source)
		}
	}
	return nil
}

// IsZero reports whether p is the zero Provenance{} (no fields set).
// Used by Render / Verify to flag wrapper fields whose Provenance was
// never populated.
func (p Provenance) IsZero() bool {
	return p.SchemaVersion == "" &&
		p.Source == "" &&
		p.URL == "" &&
		p.FetchedAt.IsZero() &&
		p.Version == "" &&
		len(p.DerivedFrom) == 0
}

// fillDefaults returns p with SchemaVersion set to the current package
// constant when empty. Callers can construct a Provenance with just
// Source + URL + FetchedAt and let constructors stamp the version.
func (p Provenance) fillDefaults() Provenance {
	if p.SchemaVersion == "" {
		p.SchemaVersion = SchemaVersion
	}
	return p
}

// MarshalJSON ensures empty Provenance values still emit a stable JSON
// shape (object with the required fields), not Go's "" or null.
func (p Provenance) MarshalJSON() ([]byte, error) {
	type alias Provenance
	a := alias(p.fillDefaults())
	return json.Marshal(a)
}
