package cli

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"github.com/spf13/cobra"

	"hop.top/kit/go/console/output"
	"hop.top/kit/go/core/xdg"
	"hop.top/kit/go/security"
)

// WithAuditCommand returns a cli.New option that mounts the kit-shipped
// `<tool> audit verify`, which checks the tamper-evident audit chains
// services.<svc>.audit.sinks writes.
//
// Mounted by cli.New, the command is kit-reserved: it is withheld from
// every served surface as management-only, so a remote caller can never
// ask the tool to vouch for its own audit trail.
func WithAuditCommand() func(*Root) {
	return func(r *Root) {
		if r == nil || r.Cmd == nil {
			return
		}
		r.Cmd.AddCommand(auditCmd(r))
	}
}

func auditCmd(r *Root) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "audit",
		Short: "Check the audit trail",
		Long:  "Check the tamper-evident audit chains the served transports write.",
		Args:  cobra.NoArgs,
	}
	cmd.AddCommand(auditVerifyCmd(r))
	return cmd
}

// AuditVerifyRow is one checked chain in `audit verify`'s output.
type AuditVerifyRow struct {
	// File is the chain's active file.
	File string `json:"file" table:"FILE"`
	// Status is ok, tampered, or missing.
	Status string `json:"status" table:"STATUS"`
	// Records is how many records verified.
	Records uint64 `json:"records" table:"RECORDS"`
	// FirstSeq is the oldest retained record's seq.
	FirstSeq uint64 `json:"first_seq,omitempty" table:"FIRST"`
	// HeadSeq and HeadHash identify the last verified record.
	HeadSeq  uint64 `json:"head_seq,omitempty" table:"HEAD"`
	HeadHash string `json:"head_hash,omitempty" table:"HEAD HASH"`
	// Break locates the first record at which the chain does not hold.
	Break string `json:"break,omitempty" table:"BREAK"`
	// TornTail reports an unfinished final write, which is not a break.
	TornTail bool `json:"torn_tail,omitempty" table:"TORN TAIL"`
}

// Row statuses.
const (
	auditStatusOK       = "ok"
	auditStatusTampered = "tampered"
	auditStatusMissing  = "missing"
)

func auditVerifyCmd(r *Root) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "verify",
		Short: "Verify the audit chain has not been edited",
		Long: "Walk every record of the audit chain — the configured chains, or --file —\n" +
			"and check each record's hash and its link to the one before. Exits 0 when\n" +
			"every chain holds, 71 (TAMPER_DETECTED) at the first record that was edited,\n" +
			"deleted, reordered or inserted, and 3 (NOT_FOUND) when a chain is absent.\n\n" +
			"Records removed from the end leave a chain that holds: compare the reported\n" +
			"head with one recorded earlier, somewhere the tool cannot write.",
		Args: cobra.NoArgs,
		RunE: func(c *cobra.Command, _ []string) error {
			file, _ := c.Flags().GetString("file")
			return runAuditVerify(c, r, file)
		},
	}
	cmd.Flags().String("file", "", "Verify this chain file instead of the configured ones")

	SetSideEffect(cmd, SideEffectRead)
	SetIdempotency(cmd, IdempotencyYes)
	cmd.Annotations[kitExitCodes] = "OK,GENERIC,NOT_FOUND," + security.CodeTamperDetected
	_ = SetOutputSchema(cmd, OutputSchema{Type: &[]AuditVerifyRow{}, Version: "1.0"})
	_ = SetExamples(cmd, []Example{
		{Title: "Every configured chain", Command: r.Config.Name + " audit verify"},
		{Title: "One file, as JSON", Command: r.Config.Name + " audit verify --file audit.chain --format json"},
	})
	return cmd
}

// runAuditVerify verifies each chain, renders one row per chain, and
// returns the error the worst row calls for: tampering over absence.
func runAuditVerify(cmd *cobra.Command, r *Root, file string) error {
	paths, err := auditVerifyPaths(r, file)
	if err != nil {
		return err
	}
	rows := make([]AuditVerifyRow, 0, len(paths))
	var tampered, missing []string
	for _, p := range paths {
		row := AuditVerifyRow{File: p, Status: auditStatusOK}
		rep, err := security.VerifyAuditLog(p)
		switch {
		case errors.Is(err, os.ErrNotExist):
			row.Status = auditStatusMissing
			missing = append(missing, p)
		case err != nil:
			return fmt.Errorf("audit verify %s: %w", p, err)
		default:
			row.Records, row.FirstSeq, row.TornTail = rep.Records, rep.FirstSeq, rep.TornTail
			row.HeadSeq, row.HeadHash = rep.Head.Seq, rep.Head.Hash
			if !rep.OK() {
				row.Status, row.Break = auditStatusTampered, rep.Break.Error()
				tampered = append(tampered, rep.Break.Error())
			}
		}
		rows = append(rows, row)
	}
	if err := output.Dispatch(cmd, r.Viper, rows); err != nil {
		return err
	}
	switch {
	case len(tampered) > 0:
		return &output.Error{
			Code:         security.CodeTamperDetected,
			Message:      "audit chain broken at " + tampered[0],
			SuggestedFix: "keep the file as evidence; the records from the break on cannot be trusted",
			ExitCode:     security.ExitTamperDetected,
			Transience:   output.TransiencePermanent,
		}
	case len(missing) > 0:
		e := output.NotFoundError("no audit chain at " + missing[0])
		e.SuggestedFix = "check services.<svc>.audit.sinks, or pass --file"
		return e
	}
	return nil
}

// auditVerifyPaths resolves which chains to verify: --file, else every
// chain configuration names, else the default chain when one exists.
func auditVerifyPaths(r *Root, file string) ([]string, error) {
	if file != "" {
		abs, err := filepath.Abs(file)
		if err != nil {
			return nil, err
		}
		return []string{abs}, nil
	}
	var services []string
	if r.serveReg != nil {
		services = r.serveReg.Names()
	}
	paths, err := serveAuditChainPaths(r.Viper, r.Config.Name, services)
	if err != nil {
		e := output.UsageError(err.Error())
		return nil, e
	}
	if len(paths) > 0 {
		return paths, nil
	}
	if dir, err := xdg.RawStateDir(r.Config.Name); err == nil {
		def := filepath.Join(dir, auditChainFile)
		if _, err := os.Stat(def); err == nil {
			return []string{def}, nil
		}
	}
	e := output.NotFoundError("no audit chain is configured")
	e.SuggestedFix = "set services.<svc>.audit.sinks: [chain], or pass --file"
	return nil, e
}
