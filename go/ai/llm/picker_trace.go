// Tracing helpers for PickProvider. The gate is read per call so operators
// can flip it without restarting long-lived processes.

package llm

import (
	"os"
	"strings"
)

// tracingEnabled reports whether LLM_PICKER_TRACE is set to a recognised
// truthy value ("1", "true", "on", "yes"; case-insensitive). Anything else,
// including unset, is off.
func tracingEnabled() bool {
	switch strings.ToLower(strings.TrimSpace(os.Getenv("LLM_PICKER_TRACE"))) {
	case "1", "true", "on", "yes":
		return true
	default:
		return false
	}
}

// tristateString renders a *bool tristate as a stable attribute value so
// downstream log pipelines can parse the field as a plain string without
// special-casing nil.
func tristateString(b *bool) string {
	if b == nil {
		return "<nil>"
	}
	if *b {
		return "true"
	}
	return "false"
}
