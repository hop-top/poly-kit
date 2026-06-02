// Package badge writes the shields.io endpoint-badge JSON for a
// 12-Factor AI-CLI conformance report, and derives the verdict
// (label / message / color) from a per-factor matrix.
//
// The badge JSON committed at repo root as `.12fcc.json` drives the
// shields endpoint badge that the kit-scaffolded READMEs embed:
//
//	https://img.shields.io/endpoint?url=https://raw.githubusercontent.com/<owner>/<repo>/main/.12fcc.json
//
// Adopter test wiring (typically the same test that regenerates the
// human-readable `docs/12-factor-conformance.md`):
//
//	import "hop.top/kit/go/conformance/badge"
//
//	func TestGenerateBadge(t *testing.T) {
//	    rep := badge.Report{
//	        Factors: []badge.Factor{
//	            {N: 1, Name: "Capability Introspection", Tier: badge.Must, Status: badge.Pass},
//	            // ... 11 more
//	        },
//	    }
//	    f, _ := os.Create(".12fcc.json")
//	    defer f.Close()
//	    if err := badge.WriteJSON(f, rep); err != nil { t.Fatal(err) }
//	}
//
// The schema is shields.io endpoint-badge v1; fields not relevant to
// the conformance signal (style, logoColor, isError) are omitted.
package badge

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
)

// Tier classifies a factor's normative weight, mirroring the spec's
// RFC-2119 vocabulary. MUST failures gate the verdict to red; SHOULD
// failures degrade it without forcing red.
type Tier int

const (
	// May is the default zero value and signals an advisory factor.
	// SHOULD / MUST should be set explicitly.
	May Tier = iota
	// Should is an advisory factor whose failure degrades the verdict
	// but never forces red.
	Should
	// Must is a normative factor whose failure forces a red verdict.
	Must
)

// Status reports a single factor's outcome. Skip is reserved for
// factors that legitimately do not apply to a given CLI (e.g. a
// read-only tool with no destructive ops can skip a destructive-token
// gate); skipped factors are excluded from pass/total counts.
type Status int

const (
	// Pass is the default zero value: factor satisfied.
	Pass Status = iota
	// Fail signals the factor's test asserted a violation.
	Fail
	// Skip signals the factor does not apply; excluded from counts.
	Skip
)

// Factor is one row of the conformance matrix. N is the factor number
// from the spec (1..12), Name is the spec's title, Tier is the
// normative weight, Status is the test outcome, Evidence is a short
// pointer at the implementation that proves the factor (e.g. a file
// path + symbol). Evidence is informational; the badge JSON does not
// embed it. Adopters typically render it in the human-readable
// `docs/12-factor-conformance.md` matrix.
type Factor struct {
	N        int
	Name     string
	Tier     Tier
	Status   Status
	Evidence string
}

// Report is a full conformance matrix. SchemaVersion is the shields
// endpoint-badge schema version; it defaults to 1 when zero.
type Report struct {
	SchemaVersion int
	Factors       []Factor
}

// shieldsEndpoint mirrors the shields.io endpoint-badge JSON schema
// v1. Fields are encoded with shields' camelCase names; omitempty is
// applied to optional fields so we never emit empty strings shields
// would treat as overrides.
type shieldsEndpoint struct {
	SchemaVersion int    `json:"schemaVersion"`
	Label         string `json:"label"`
	Message       string `json:"message"`
	Color         string `json:"color"`
	LabelColor    string `json:"labelColor,omitempty"`
	NamedLogo     string `json:"namedLogo,omitempty"`
	CacheSeconds  int    `json:"cacheSeconds,omitempty"`
}

// Validate enforces the spec's invariants on a Report. Returns nil
// when the report is well-formed.
func Validate(rep Report) error {
	if len(rep.Factors) != 12 {
		return fmt.Errorf("badge: report must list 12 factors, got %d", len(rep.Factors))
	}
	seen := make(map[int]bool, 12)
	for i, f := range rep.Factors {
		if f.N < 1 || f.N > 12 {
			return fmt.Errorf("badge: factor[%d] has out-of-range N=%d", i, f.N)
		}
		if seen[f.N] {
			return fmt.Errorf("badge: factor N=%d appears more than once", f.N)
		}
		seen[f.N] = true
		if f.Name == "" {
			return fmt.Errorf("badge: factor N=%d has empty Name", f.N)
		}
	}
	return nil
}

// Verdict reduces a Report to the three shields.io fields. The rules
// are:
//
//   - any MUST factor failing => red, "<pass>/<total> pass, MUST fail"
//   - all 12 pass            => brightgreen, "12/12 pass"
//   - >=10 passing, no MUST  => green, "<pass>/<total> pass"
//     failing
//   - >=8 passing            => yellow, "<pass>/<total> pass"
//   - otherwise              => red,    "<pass>/<total> pass"
//
// Skipped factors are excluded from both pass and total counts.
// Verdict never returns an error; an invalid Report yields
// ("12-factor AI-CLI", "ungradable", "lightgrey") so that callers
// (and the badge) degrade gracefully when the matrix is malformed or
// not yet populated.
func Verdict(rep Report) (label, message, color string) {
	const label12fcc = "12-factor AI-CLI"
	if err := Validate(rep); err != nil {
		return label12fcc, "ungradable", "lightgrey"
	}
	var pass, total, mustFail int
	for _, f := range rep.Factors {
		if f.Status == Skip {
			continue
		}
		total++
		switch f.Status {
		case Pass:
			pass++
		case Fail:
			if f.Tier == Must {
				mustFail++
			}
		}
	}
	switch {
	case mustFail > 0:
		return label12fcc, fmt.Sprintf("%d/%d pass, MUST fail", pass, total), "red"
	case total > 0 && pass == total && total == 12:
		return label12fcc, "12/12 pass", "brightgreen"
	case pass >= 10:
		return label12fcc, fmt.Sprintf("%d/%d pass", pass, total), "green"
	case pass >= 8:
		return label12fcc, fmt.Sprintf("%d/%d pass", pass, total), "yellow"
	default:
		return label12fcc, fmt.Sprintf("%d/%d pass", pass, total), "red"
	}
}

// WriteJSON encodes the shields.io endpoint-badge payload for rep to
// w. Output is deterministic (stable field order, two-space indent,
// trailing newline) so the file diffs cleanly under version control.
// Returns an error only when w.Write fails or rep cannot be encoded;
// an invalid Report is encoded as the lightgrey "ungradable" badge
// via Verdict's fallback.
func WriteJSON(w io.Writer, rep Report) error {
	if w == nil {
		return errors.New("badge: nil writer")
	}
	label, message, color := Verdict(rep)
	schema := rep.SchemaVersion
	if schema == 0 {
		schema = 1
	}
	payload := shieldsEndpoint{
		SchemaVersion: schema,
		Label:         label,
		Message:       message,
		Color:         color,
		LabelColor:    "555",
		NamedLogo:     "robotframework",
		CacheSeconds:  300,
	}
	buf, err := json.MarshalIndent(payload, "", "  ")
	if err != nil {
		return fmt.Errorf("badge: marshal: %w", err)
	}
	buf = append(buf, '\n')
	if _, err := w.Write(buf); err != nil {
		return fmt.Errorf("badge: write: %w", err)
	}
	return nil
}
