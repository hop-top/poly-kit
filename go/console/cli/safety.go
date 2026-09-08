package cli

import (
	"fmt"

	"github.com/spf13/cobra"
)

// SafetyLevel classifies the risk of a CLI operation.
type SafetyLevel string

const (
	SafetyRead      SafetyLevel = "read"
	SafetyCaution   SafetyLevel = "caution"
	SafetyDangerous SafetyLevel = "dangerous"
)

// SafetyGuard checks safety level and enforces confirmation.
// Returns nil if safe to proceed, error if blocked.
//
// Deprecated: use the side-effect tag plus the --confirm gate
// (SetSideEffect + the policy middleware) instead. That path is
// wired into the root command chain, honors --confirm/--dry-run/
// --confirm-token and renders typed refusals; SafetyGuard is an
// unwired standalone predating it and answers only y/n on a
// terminal.
//
// Retained and corrected rather than removed because it is exported
// API. The prompt now reads and writes the controlling terminal via
// the shared helper, so it can no longer consume the command's
// stdin, and the non-interactive check asks whether there is a
// terminal to prompt on rather than whether stdin happens to be one.
func SafetyGuard(cmd *cobra.Command, level SafetyLevel) error {
	if level == SafetyRead {
		return nil
	}
	if f := cmd.Flags().Lookup("force"); f != nil && f.Value.String() == "true" {
		return nil
	}
	answer, asked := promptAsk(nil, fmt.Sprintf(
		"This is a %s operation. Continue? [y/N] ", level))
	if !asked {
		return fmt.Errorf(
			"%s operation requires --force in non-interactive mode",
			level,
		)
	}
	if answer != "y" && answer != "yes" {
		return fmt.Errorf("aborted by user")
	}
	return nil
}
