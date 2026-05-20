package telemetry

// DefaultEndpoint is the build-time HTTPS endpoint for the telemetry
// collector. Adopters set it via Go's `-ldflags -X` so the URL bakes
// into the binary at build time without ever landing in a source file
// or in `go env`:
//
//	go build -ldflags="-X 'hop.top/kit/go/runtime/telemetry.DefaultEndpoint=$URL'" ./cmd/spaced
//
// The intended pipeline keeps the URL in a CI secret (e.g. a GitHub
// Actions repository secret) and references it from the release
// workflow; the value never lives in git. Dev builds without the
// ldflag leave DefaultEndpoint empty, which forces emitters to fall
// back to the local jsonl sink — safe by construction.
//
// Runtime overrides still win. `KIT_TELEMETRY_ENDPOINT` in the
// process env beats the ldflag default; an explicit
// `cli.TelemetryConfig{Endpoint: ...}` from the adopter's wire-up
// beats both. ResolveEndpoint encodes that ordering.
var DefaultEndpoint = ""

// ResolveEndpoint walks the endpoint-resolution precedence chain and
// returns the URL the emitter should target. The order — highest
// priority first — is:
//
//  1. envOverride: a non-empty value (typically read from
//     `KIT_TELEMETRY_ENDPOINT`). Lets operators repoint a shipped
//     binary at staging without a rebuild.
//  2. wireConfig: a non-empty value passed by the adopter via
//     `cli.TelemetryConfig.Endpoint`. Useful in multi-binary
//     monorepos where one binary points at a different collector.
//  3. DefaultEndpoint: the ldflag-injected build-time default.
//  4. "" (empty): the adopter has no endpoint configured. Callers
//     interpret empty as "no HTTPS sink"; the jsonl sink remains the
//     safe default.
//
// The helper is pure: no env reads, no globals beyond DefaultEndpoint.
// Callers that want env-var resolution pass `os.Getenv("KIT_TELEMETRY_ENDPOINT")`
// as `envOverride`. Keeping env reads at the call site means tests
// don't need `t.Setenv` for this codepath.
func ResolveEndpoint(envOverride, wireConfig string) string {
	if envOverride != "" {
		return envOverride
	}
	if wireConfig != "" {
		return wireConfig
	}
	return DefaultEndpoint
}
