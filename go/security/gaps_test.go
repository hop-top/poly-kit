package security_test

// Gap test for the missing kit/security/scorecard wrapper.

import "testing"

// Gap: kit/security/scorecard wrapper does not exist.
//
// The OSSF Scorecard call lives in rsx alone today (rsx scores repo
// trust signals). dpkms and wsm have surfaced the same need (vetting
// dependencies, gating PRs on score thresholds), so the call belongs
// in shared kit code rather than rsx-only.
//
// Desired API shape (sketch):
//
//	score, err := scorecard.Run(ctx, scorecard.Options{
//	    Repo:    "github.com/foo/bar",
//	    Token:   ghToken,         // optional, for higher rate limits
//	    Checks:  scorecard.DefaultChecks,
//	})
//	if score.Aggregate < 7.0 {
//	    return fmt.Errorf("scorecard below threshold: %.1f", score.Aggregate)
//	}
//
// Implementation note: scorecard.Run can shell out to the official
// `scorecard` binary (preferred — keeps the check authoritative)
// or call the GitHub Scorecard REST endpoint for cached results.
// The wrapper picks one and exposes the same return shape.
func TestGap_SecurityScorecardWrapper_Missing(t *testing.T) {
	t.Skip("gap: kit/security/scorecard wrapper not implemented; OSSF Scorecard call lives only in rsx — dpkms/wsm need the same primitive")

	// Pin: placeholder package exists; wrapper does not. When the
	// wrapper ships, this test should run a recorded/mock scorecard
	// invocation and assert the aggregate score parses.
}
