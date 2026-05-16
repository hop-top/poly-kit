package scope

import (
	"errors"
	"fmt"

	"github.com/spf13/cobra"
	"github.com/spf13/viper"
	kitcli "hop.top/kit/go/console/cli"
	"hop.top/kit/go/console/output"
	scopepkg "hop.top/kit/go/core/scope"
)

// checkResult is the structured form of a single (path, op) check.
type checkResult struct {
	Path     string `table:"PATH"     json:"path"     yaml:"path"`
	Op       string `table:"OP"       json:"op"       yaml:"op"`
	Decision string `table:"DECISION" json:"decision" yaml:"decision"`
	Tool     string `table:"-"        json:"tool,omitempty" yaml:"tool,omitempty"`
}

func checkCmd() *cobra.Command {
	var (
		tool string
		op   string
	)
	cmd := &cobra.Command{
		Use:   "check <path>",
		Short: "Check whether one path is allowed under the policy",
		Long: "Resolve the kit/scope policy for a single (path, op) pair " +
			"and emit the decision. Exit 0 when allowed, 1 when denied, " +
			"2 on a malformed --op. The policy can be re-rooted via " +
			"--tool <name> which loads from FromConfig instead of Default.",
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			pol, err := resolvePolicy(tool)
			if err != nil {
				return err
			}
			parsedOp, err := parseOp(op)
			if err != nil {
				return usageError(err)
			}
			path := args[0]
			dec, err := pol.Check(scopepkg.Path(path), parsedOp)
			if err != nil {
				return err
			}
			res := checkResult{
				Path:     path,
				Op:       opLabel(parsedOp),
				Decision: decisionName(dec),
				Tool:     tool,
			}

			if err := output.Dispatch(cmd, viper.GetViper(), res); err != nil {
				return err
			}

			// Resolve through Mode for the exit code.
			if eErr := pol.Enforce(scopepkg.Path(path), parsedOp); eErr != nil {
				if errors.Is(eErr, scopepkg.ErrDenied) {
					return makeExitErr(1)
				}
				return eErr
			}
			return nil
		},
	}
	cmd.Flags().StringVar(&tool, "tool", "", "Load policy via FromConfig(<tool>) instead of Default()")
	cmd.Flags().StringVar(&op, "op", "read", "Operation: read | write | exec")
	kitcli.SetSideEffect(cmd, kitcli.SideEffectRead)
	kitcli.SetIdempotency(cmd, kitcli.IdempotencyYes)
	return cmd
}

// usageError wraps err so cobra prints usage. Renders as a USAGE
// envelope with exit code 2 — kit's RunE middleware preserves the
// envelope through fang.Execute and main() honors envelope.ExitCode.
type usageErr struct{ err error }

func (e *usageErr) Error() string { return "usage: " + e.err.Error() }
func (e *usageErr) Unwrap() error { return e.err }

// AsCLIError implements the conversion interface used by the RunE
// middleware so usage errors keep their exit-2 contract through the
// error envelope.
func (e *usageErr) AsCLIError() *output.Error {
	return output.UsageError(e.Error())
}

func usageError(err error) error { return &usageErr{err: err} }

// exitErr carries a target exit code through the cobra error chain.
// AsCLIError emits a CodeGeneric envelope so kit's main exits with
// the embedded code (1 for Denied).
type exitErr struct {
	Code int
}

func (e exitErr) Error() string { return fmt.Sprintf("denied (exit %d)", e.Code) }

// AsCLIError surfaces the carried exit code via the error envelope.
func (e exitErr) AsCLIError() *output.Error {
	return &output.Error{Code: output.CodeGeneric, Message: e.Error(), ExitCode: e.Code}
}

func makeExitErr(code int) error { return exitErr{Code: code} }

// IsDeniedExit reports whether err is the sentinel returned by check/test on
// a Denied decision. Callers (e.g. main) can map it to os.Exit(1) without
// printing the wrapper message.
func IsDeniedExit(err error) bool {
	var e exitErr
	return errors.As(err, &e) && e.Code == 1
}

// IsUsageError reports whether err originated from a malformed flag/argument.
func IsUsageError(err error) bool {
	var u *usageErr
	return errors.As(err, &u)
}
