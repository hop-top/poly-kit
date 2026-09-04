package cli

import (
	"encoding/json"
	"fmt"

	"github.com/spf13/cobra"

	"hop.top/kit/go/console/cli/cmdmeta"
)

// Example is one entry in a leaf command's kit/examples annotation.
// Title is a short human label; Command is the literal invocation
// (e.g. "kit foo create --name=bar"); Output is an optional embedded
// snippet illustrating expected output.
//
// An ALIAS of [cmdmeta.Example], not a copy: the reader lives in the
// leaf package, and an alias keeps the two spellings the same type,
// so a value crossing between cli and cmdmeta needs no conversion.
type Example = cmdmeta.Example

// NextStep is one entry in a leaf command's kit/next-steps
// annotation. Surfaced to agents post-invocation to chain follow-up
// commands. When is a free-form condition string ("on success",
// "when no results"); Suggest is the literal next invocation;
// Reason explains why the suggestion fits.
//
// An alias of [cmdmeta.NextStep]; see [Example].
type NextStep = cmdmeta.NextStep

// Guidance bundles Examples and NextSteps for SetGuidance — a
// convenience over the two singular setters.
type Guidance struct {
	Examples  []Example
	NextSteps []NextStep
}

// SetExamples attaches a JSON-encoded []Example to the kit/examples
// annotation. The encoded payload must not exceed cap bytes (0 =
// defaultMaxGuidanceBytes). Returns an error on marshal failure or
// when the payload exceeds the cap.
func SetExamples(cmd *cobra.Command, ex []Example) error {
	return setGuidanceList(cmd, kitExamples, ex, 0)
}

// SetExamplesWithCap is the explicit-cap form of SetExamples. Pass 0
// to use the default cap.
func SetExamplesWithCap(cmd *cobra.Command, ex []Example, cap int) error {
	return setGuidanceList(cmd, kitExamples, ex, cap)
}

// SetNextSteps attaches a JSON-encoded []NextStep to the
// kit/next-steps annotation. Size-bounded; see SetExamples.
func SetNextSteps(cmd *cobra.Command, ns []NextStep) error {
	return setGuidanceList(cmd, kitNextSteps, ns, 0)
}

// SetNextStepsWithCap is the explicit-cap form of SetNextSteps.
func SetNextStepsWithCap(cmd *cobra.Command, ns []NextStep, cap int) error {
	return setGuidanceList(cmd, kitNextSteps, ns, cap)
}

// SetGuidance is the convenience setter installing both Examples and
// NextSteps in one call. Either side may be empty; the corresponding
// annotation is then cleared.
func SetGuidance(cmd *cobra.Command, g Guidance) error {
	if err := SetExamples(cmd, g.Examples); err != nil {
		return err
	}
	return SetNextSteps(cmd, g.NextSteps)
}

// GetExamples decodes the kit/examples annotation into []Example.
// Returns (nil, false) when the annotation is absent or malformed;
// callers that want decode errors should use json.Unmarshal directly
// on the annotation bytes.
//
// Forwards to [cmdmeta.GetExamples].
func GetExamples(cmd *cobra.Command) ([]Example, bool) {
	return cmdmeta.GetExamples(cmd)
}

// GetNextSteps decodes the kit/next-steps annotation into []NextStep.
// Same semantics as GetExamples.
//
// Forwards to [cmdmeta.GetNextSteps].
func GetNextSteps(cmd *cobra.Command) ([]NextStep, bool) {
	return cmdmeta.GetNextSteps(cmd)
}

// setGuidanceList marshals v to JSON and stores the result under
// key on cmd, capped at cap bytes (0 = defaultMaxGuidanceBytes).
// An empty/nil v clears the annotation.
func setGuidanceList(cmd *cobra.Command, key string, v any, cap int) error {
	if cmd == nil {
		return fmt.Errorf("%s: nil command", key)
	}
	if cmd.Annotations == nil {
		cmd.Annotations = make(map[string]string)
	}
	if isEmptyGuidance(v) {
		delete(cmd.Annotations, key)
		return nil
	}
	raw, err := json.Marshal(v)
	if err != nil {
		return fmt.Errorf("%s: marshal: %w", key, err)
	}
	if cap == 0 {
		cap = defaultMaxGuidanceBytes
	}
	if len(raw) > cap {
		return fmt.Errorf("%s: encoded payload %d bytes exceeds cap %d",
			key, len(raw), cap)
	}
	cmd.Annotations[key] = string(raw)
	return nil
}

// isEmptyGuidance reports whether v is a zero-length slice ([]Example
// or []NextStep). Used to clear the annotation when an empty payload
// is supplied.
func isEmptyGuidance(v any) bool {
	switch s := v.(type) {
	case []Example:
		return len(s) == 0
	case []NextStep:
		return len(s) == 0
	case nil:
		return true
	}
	return false
}
