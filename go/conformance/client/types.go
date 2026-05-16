package client

import (
	"net/http"
	"time"
)

// Client carries the configuration for a single svc endpoint.
// Adopters construct one via New + functional options and reuse it
// across Grade/Status calls; methods are safe for concurrent use.
type Client struct {
	baseURL     string
	token       string
	http        *http.Client
	userAgent   string
	maxAttempts int
	backoff     backoffPolicy
	maxCassette int64
	now         func() time.Time
}

// GradeRequest is the input shape for Client.Grade. CassetteDir is
// required; everything else is optional and overrides what the
// manifest at <CassetteDir>/manifest.yaml declares.
type GradeRequest struct {
	// CassetteDir points to a directory containing a manifest.yaml
	// plus the per-step cassette/capture data scen consumes.
	CassetteDir string

	// ScenarioID, if non-empty, overrides the manifest's scenario_id.
	ScenarioID string

	// StoryPath, if non-empty, overrides the manifest's story_path.
	StoryPath string

	// Tier requests a grading tier (1, 2, or 3). 0 defers to
	// manifest/server default. svc may downgrade.
	Tier int

	// Captures, if non-nil, augments per-step capture data inline
	// rather than reading from disk. Keyed by step ID. Adopters whose
	// captures live in memory (not on disk) use this to avoid a temp
	// dir.
	Captures map[string]Capture
}

// Capture mirrors scen's per-step capture envelope. The fields here
// match what the harness records on disk under
// steps/<step-id>/{stdout.bin, stderr.bin, result.json}.
type Capture struct {
	ExitCode    int
	Stdout      []byte
	Stderr      []byte
	DurationMs  int64
	CassetteDir string // path relative to GradeRequest.CassetteDir
}

// Result is a structural bridge to
// hop.top/kit/go/conformance/scenario.Result. While the scen track is
// landing in a parallel worktree, this package mirrors the JSON wire
// shape locally. After scen merges, this alias should be replaced by
// a type alias / direct re-export with a compile-time assertion that
// the JSON tags still match.
type Result struct {
	ScenarioID     string         `json:"scenario_id"`
	Verdict        string         `json:"verdict"` // pass | fail | ungradable
	ExitCode       int            `json:"exit_code"`
	Reason         string         `json:"reason,omitempty"`
	Tier           int            `json:"tier,omitempty"`
	ScoredAt       string         `json:"scored_at,omitempty"`
	GraderVersion  string         `json:"grader_version,omitempty"`
	RulesVersion   string         `json:"rules_version,omitempty"`
	ServiceVersion string         `json:"service_version,omitempty"`
	Facets         []Facet        `json:"facets,omitempty"`
	Findings       []Finding      `json:"findings,omitempty"`
	Provenance     map[string]any `json:"provenance,omitempty"`
}

// Facet is one factor-coverage entry in tier-2/3 results.
type Facet struct {
	Factor      int    `json:"factor"`
	Status      string `json:"status"` // pass | fail | n/a
	Description string `json:"description,omitempty"`
}

// Finding is one failing assertion in tier-3 results.
type Finding struct {
	ID       string `json:"id"`
	Kind     string `json:"kind,omitempty"`
	Expected string `json:"expected,omitempty"`
	Observed string `json:"observed,omitempty"`
}

// Verdict constants. Match scen's wire shape.
const (
	VerdictPass       = "pass"
	VerdictFail       = "fail"
	VerdictUngradable = "ungradable"
)

// backoffPolicy controls the retry loop's per-attempt delay
// computation. The zero value is invalid; defaultBackoff returns the
// production defaults.
type backoffPolicy struct {
	InitialBackoff    time.Duration
	MaxBackoff        time.Duration
	BackoffMultiplier float64
	BackoffJitter     float64
}

// defaultBackoff returns the v1 defaults documented in design.md §5.
func defaultBackoff() backoffPolicy {
	return backoffPolicy{
		InitialBackoff:    500 * time.Millisecond,
		MaxBackoff:        10 * time.Second,
		BackoffMultiplier: 2.0,
		BackoffJitter:     0.3,
	}
}
