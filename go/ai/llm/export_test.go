// Test-only re-exports keep production identifiers unexported while still
// letting llm_test exercise them.

package llm

// TracingEnabled exposes [tracingEnabled] to the external test package.
var TracingEnabled = tracingEnabled
