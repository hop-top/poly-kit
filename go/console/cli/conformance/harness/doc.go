// Package harness is the kit-shipped integration-test toolkit
// adopters import to assert kit-blessed contract properties of a
// cobra-driven CLI under controlled conditions.
//
// Adopter use site looks like:
//
//	import (
//	    "testing"
//	    "hop.top/kit/go/console/cli/conformance/harness"
//	)
//
//	func TestSpaced_Idempotent(t *testing.T) {
//	    cmd := buildRoot().Cmd
//	    harness.PlanApplyReplay(t, cmd,
//	        harness.Args("launch", "--payload", "alpha"))
//	}
//
//	func TestSpaced_DryRunNoMutation(t *testing.T) {
//	    cmd := buildRoot().Cmd
//	    harness.AssertDryRunNoMutation(t, cmd,
//	        harness.Args("launch", "--payload", "alpha"))
//	}
//
// The harness sits on xrr cassettes: each `Assert*` invocation
// wraps the cobra command in an xrr Session, captures the
// adapter-mediated side effects, and asserts on the shape of the
// recorded cassette. See ADR-0021 (xrr-first integration model)
// for the rationale.
//
// The harness's primary surface is this Go package; adopters who
// want a CLI subcommand to summarize / refresh cassettes pick that
// up from the (currently reserved) `kit conformance harness`
// leaf in a future track.
//
// Six primitives, plus deterministic-environment helpers:
//
//   - PlanApplyReplay         — assert second apply is a no-op
//   - AssertDryRunNoMutation  — assert --dry-run hits only reads
//   - AssertDestructiveGated  — assert destructive ops refuse w/o yes
//   - AssertExitCodeClass     — assert exit code matches declared class
//   - AssertJSONSchema        — assert JSON stdout matches schema
//   - AssertCapabilityRoundtrip — assert every non-interactive leaf
//     re-invokes successfully under --help
//   - NonTTY / WithTTY        — control isatty observability
//   - WithConfigSnapshot      — pin viper config for one invocation
//
// Adopters wire xrr into their own adapter call sites (HTTP
// RoundTripper, sql DB, exec shim, etc.); the harness does not
// auto-instrument adopter code. See the package README for the
// recipe.
package harness
