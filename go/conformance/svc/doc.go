// Package svc is the HTTP service surface for the conformance grader
// library. It wraps hop.top/kit/go/conformance/scenario in REST routes,
// adds bearer-token auth, rate limiting, cassette upload, scenario
// storage, and operator CLI surfaces.
//
// The package is the network surface only; the actual grading logic
// lives in hop.top/kit/go/conformance/scenario. The ScenarioGrader
// interface is the seam; LibGrader (grader.go) is the production
// implementation delegating to scenario.Grade.
//
// Layout overview:
//
//	doc.go              package overview
//	types.go            shared types: ScenarioRef, ScenarioMeta, etc.
//	scenario_bridge.go  ScenarioGrader seam + scenario type aliases
//	grader.go           LibGrader — scenario.Grade delegation
//	errors.go           output.Error helpers + HTTP shaping
//	manifest.go         manifest.yaml types + validator
//	cassette.go         tar.gz receiver + size/traversal hardening
//	store.go            ScenarioStore interface
//	fsstore.go          filesystem driver
//	claims.go           Claim type + scope vocabulary
//	auth.go             bearer claim store + Auth middleware
//	ratelimit.go        per-claim token bucket (in-process)
//	judge_registry.go   ModelRegistry interface + Null/Stub/Config
//	handler.go          http.Handler / Router wiring
//	grade.go            POST /v1/grade handler
//	capabilities.go     GET /v1/capabilities
//	health.go           /healthz, /readyz
//	observability.go    metrics + tracing wiring
//
// CLI placement: go/console/cli/conformance/svc/ — cobra subcommand
// "kit conformance svc {serve,token mint,token list,token revoke}".
package svc

// Version is the service implementation version surfaced in responses
// and the startup JSON line.
const Version = "0.1.0"
