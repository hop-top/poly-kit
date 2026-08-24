package client

import (
	"net/http"
	"time"

	"hop.top/kit/go/conformance/scenario"
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

// Result is the grade result envelope — a type alias to the scenario
// library's Result, which is the exact struct svc serializes under
// the "result" key of a /v1/grade response (svc.GradeResponse).
// Aliasing rather than mirroring makes wire drift impossible: every
// field the grader records — facet rollups, per-assertion traces,
// judge traces — survives the typed decode by construction.
type Result = scenario.Result

// Verdict is the top-level pass/fail/ungradable outcome.
type Verdict = scenario.Verdict

// Status is the per-assertion / per-facet outcome.
type Status = scenario.Status

// Facet is one per-factor rollup entry in tier-2/3 results.
type Facet = scenario.FactorFacet

// Assertion is one per-assertion trace entry in tier-3 results. It
// carries the observed/expected values and the evaluator message that
// explain WHY a facet failed.
type Assertion = scenario.AssertionResult

// JudgeTrace records one AI-judge invocation (tier-3 results only).
type JudgeTrace = scenario.JudgeTrace

// Verdict and Status constants, re-exported from the scenario
// library so adopters need only this import.
const (
	VerdictPass       = scenario.VerdictPass
	VerdictFail       = scenario.VerdictFail
	VerdictUngradable = scenario.VerdictUngradable

	StatusPass           = scenario.StatusPass
	StatusFail           = scenario.StatusFail
	StatusNotImplemented = scenario.StatusNotImplemented
	StatusUngradable     = scenario.StatusUngradable
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
