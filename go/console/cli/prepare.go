package cli

import "errors"

// Prepare installs on the command tree everything Execute installs
// before it parses argv — the alias annotations, the completion
// command, leaf help, the kit-managed flags, the help addenda, and
// the RunE middleware chain that carries the confirmation, policy,
// idempotency, and error-envelope gates ([Root.WrapRunE]) — and runs
// the same validation, without executing anything and without
// touching cobra's process-global initializer list.
//
// It is the pre-flight half of Execute for a tree that will be
// executed by something other than Execute: a root factory's trees,
// or an adopter's own dispatch loop over r.Cmd. Execute performs the
// same steps itself, so a Root that goes through Execute needs no
// Prepare, and calling both is harmless: every step is idempotent.
//
// Unlike Execute, a validation failure is returned rather than
// dispatched through Config.ValidationFailureMode — a *ValidationError
// when Config.EnforceValidate is set and the tree fails it, a
// *SignatureReportError under SignatureStrictnessReject. Under
// SignatureStrictnessWarn the violations are logged, as Execute logs
// them. When Prepare returns an error the gates are not installed.
func (r *Root) Prepare() error {
	if r == nil || r.Cmd == nil {
		return errors.New("cli: Prepare on a Root with no command")
	}
	r.prepareTree()
	if err := r.validateTree(); err != nil {
		return err
	}
	switch r.Config.SignatureStrictness {
	case SignatureStrictnessWarn:
		// Logs every violation and never dispatches.
		_, _ = r.dispatchSignatureReport()
	case SignatureStrictnessReject:
		if report := r.ValidateSignature(); report.HasViolations() {
			return &SignatureReportError{Report: report}
		}
	}
	r.WrapRunE()
	return nil
}
