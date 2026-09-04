package cmdmeta

import "github.com/spf13/cobra"

// Annotation keys under the kit/ reserved prefix. They are the same
// strings cli writes; this package is the canonical home for the
// ones it reads, and cli aliases these constants rather than
// repeating the literals.
const (
	// KeyTopLevelVerb marks an intentional depth-1 leaf.
	KeyTopLevelVerb = "kit/top-level-verb"
	// KeyHierarchical marks an intermediate node as an intentional
	// grouping level.
	KeyHierarchical = "kit/hierarchical"
	// KeyPassthrough marks a command forwarding opaque `-- args` to
	// a child process.
	KeyPassthrough = "kit/passthrough"
	// KeyRetryable marks a command as safely re-runnable.
	KeyRetryable = "kit/retryable"
	// KeyOutputSchema holds the JSON Schema bytes for structured
	// output.
	KeyOutputSchema = "kit/output-schema"
	// KeyOutputSchemaVersion holds the MAJOR.MINOR version paired
	// with KeyOutputSchema.
	KeyOutputSchemaVersion = "kit/output-schema-version"
	// KeyExamples holds a JSON-encoded []Example.
	KeyExamples = "kit/examples"
	// KeyNextSteps holds a JSON-encoded []NextStep.
	KeyNextSteps = "kit/next-steps"
	// KeySideEffect holds the declared side-effect tier.
	KeySideEffect = "kit/side-effect"
	// KeyDryRun holds the explicit dry-run declaration.
	KeyDryRun = "kit/dry-run"
)

// ReadBool reports whether key is present on cmd with the value
// "true". A nil command or nil annotation map reads false.
func ReadBool(cmd *cobra.Command, key string) bool {
	if cmd == nil || cmd.Annotations == nil {
		return false
	}
	return cmd.Annotations[key] == "true"
}

// read returns the raw annotation value, tolerating a nil command
// or nil map.
func read(cmd *cobra.Command, key string) string {
	if cmd == nil || cmd.Annotations == nil {
		return ""
	}
	return cmd.Annotations[key]
}

// IsTopLevelVerb reports whether kit/top-level-verb is "true" on cmd.
func IsTopLevelVerb(cmd *cobra.Command) bool {
	return ReadBool(cmd, KeyTopLevelVerb)
}

// IsHierarchical reports whether kit/hierarchical is "true" on cmd.
func IsHierarchical(cmd *cobra.Command) bool {
	return ReadBool(cmd, KeyHierarchical)
}

// IsPassthrough reports whether kit/passthrough is "true" on cmd.
func IsPassthrough(cmd *cobra.Command) bool {
	return ReadBool(cmd, KeyPassthrough)
}

// IsRetryable reports whether kit/retryable is "true" on cmd.
func IsRetryable(cmd *cobra.Command) bool {
	return ReadBool(cmd, KeyRetryable)
}
