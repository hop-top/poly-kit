package cli

import (
	"context"
	"net/url"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"hop.top/kit/go/console/output"
	"hop.top/kit/go/transport/cmdsurface"
)

// WithQuotaCommand returns a cli.New option that mounts the kit-shipped
// `<tool> quota show` and `<tool> quota reset`, which read and clear
// the counts the services.<svc>.quota blocks keep.
//
// Mounted by cli.New, the commands are kit-reserved: withheld from
// every served surface as management-only, so a remote caller can
// never read another caller's usage or clear its own.
func WithQuotaCommand() func(*Root) {
	return func(r *Root) {
		if r == nil || r.Cmd == nil {
			return
		}
		r.Cmd.AddCommand(quotaCmd(r))
	}
}

func quotaCmd(r *Root) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "quota",
		Short: "Read and reset served usage quotas",
		Long:  "Read and reset the per-caller usage the services' quota blocks count.",
		Args:  cobra.NoArgs,
	}
	cmd.AddCommand(quotaShowCmd(r), quotaResetCmd(r))
	return cmd
}

// QuotaRow is one caller's usage in `quota show`'s output.
type QuotaRow struct {
	// Service is the service the quota belongs to.
	Service string `json:"service" table:"SERVICE"`
	// Caller is who is counted: principal/<name>/<tenant>,
	// tenant/<name>, address/<ip>, or surface/<name>.
	Caller string `json:"caller" table:"CALLER"`
	// Ops and MaxOps are the calls counted and allowed this window;
	// MaxOps 0 is no limit.
	Ops    int64 `json:"ops" table:"OPS"`
	MaxOps int64 `json:"max_ops" table:"MAX OPS"`
	// Bytes and MaxBytes are the output bytes counted and allowed;
	// MaxBytes 0 is no limit.
	Bytes    int64 `json:"bytes" table:"BYTES"`
	MaxBytes int64 `json:"max_bytes" table:"MAX BYTES"`
	// Window is the quota's window; Resets when the current one ends.
	Window string    `json:"window" table:"WINDOW"`
	Resets time.Time `json:"resets" table:"RESETS"`
}

func quotaShowCmd(r *Root) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "show",
		Short: "Show each caller's usage in the current window",
		Long: "List every caller a service's quota has counted in the current window,\n" +
			"with its calls and output bytes against the limits. --service narrows\n" +
			"the list to one service.",
		Args: cobra.NoArgs,
		RunE: func(c *cobra.Command, _ []string) error {
			svc, _ := c.Flags().GetString("service")
			rows, err := quotaRows(c.Context(), r, svc)
			if err != nil {
				return err
			}
			return output.Dispatch(c, r.Viper, rows)
		},
	}
	cmd.Flags().String("service", "", "Show this service's quota only")
	SetSideEffect(cmd, SideEffectRead)
	SetIdempotency(cmd, IdempotencyYes)
	_ = SetOutputSchema(cmd, OutputSchema{Type: &[]QuotaRow{}, Version: "1.0"})
	_ = SetExamples(cmd, []Example{
		{Title: "Every service", Command: r.Config.Name + " quota show"},
		{Title: "The api service, as JSON", Command: r.Config.Name + " quota show --service api --format json"},
	})
	return cmd
}

// quotaResetResult is what quota reset renders: how many quota counts
// it cleared.
type quotaResetResult struct {
	Reset int `json:"reset" yaml:"reset" table:"RESET"`
}

func quotaResetCmd(r *Root) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "reset [caller]",
		Short: "Clear a caller's usage, so its quota starts over",
		Long: "Clear the usage a service's quota counted for caller — as `quota show`\n" +
			"prints it — so the caller's quota starts over now. --all clears every\n" +
			"caller. --service limits either to one service.",
		Args: cobra.MaximumNArgs(1),
		RunE: func(c *cobra.Command, args []string) error {
			svc, _ := c.Flags().GetString("service")
			all, _ := c.Flags().GetBool("all")
			if (len(args) == 0) == !all {
				return output.UsageError("name one caller, or pass --all")
			}
			caller := ""
			if len(args) == 1 {
				caller = args[0]
			}
			if IsDryRun(c) {
				return planQuotaReset(c, r, svc, caller)
			}
			n, err := quotaReset(c.Context(), r, svc, caller)
			if err != nil {
				return err
			}
			if n == 0 {
				e := output.NotFoundError("no quota usage recorded for " + orAll(caller))
				e.SuggestedFix = "run `" + r.Config.Name + " quota show` for the callers counted"
				return e
			}
			return output.Dispatch(c, r.Viper, quotaResetResult{Reset: n})
		},
	}
	cmd.Flags().String("service", "", "Reset this service's quota only")
	cmd.Flags().Bool("all", false, "Reset every caller")
	SetSideEffect(cmd, SideEffectWriteLocal)
	SetIdempotency(cmd, IdempotencyYes)
	cmd.Annotations[kitExitCodes] = "OK,GENERIC,USAGE,NOT_FOUND"
	_ = SetOutputSchema(cmd, OutputSchema{Type: &quotaResetResult{}, Version: "1.0"})
	_ = SetExamples(cmd, []Example{
		{Title: "One principal", Command: r.Config.Name + " quota reset principal/alice/acme"},
		{Title: "Every caller of the api service", Command: r.Config.Name + " quota reset --all --service api"},
	})
	return cmd
}

// planQuotaReset is quota reset under --dry-run: the services'
// quota configuration resolves as for the real reset, and each quota
// the reset would clear becomes an effect. The usage ledger is not
// opened, so the plan does not say how many callers have usage.
func planQuotaReset(c *cobra.Command, r *Root, svc, caller string) error {
	if err := r.loadServiceConfig(); err != nil {
		return err
	}
	quotas, err := quotaServices(r, svc)
	if err != nil {
		return err
	}
	who := caller
	if who == "" {
		who = "*"
	}
	plan := Plan{Args: map[string]any{"service": svc, "caller": caller}}
	for _, q := range quotas {
		plan.Effects = append(plan.Effects, Effect{Kind: "delete",
			Target: "quota:" + q.Scope + "/" + who, Reversible: false,
			Detail: "clear usage counted in the current window"})
	}
	return RenderPlan(c, plan)
}

func orAll(caller string) string {
	if caller == "" {
		return "any caller"
	}
	return caller
}

// quotaServices returns the services whose quota is on, with their
// quotas: svc alone when named, else every registered service.
func quotaServices(r *Root, svc string) ([]cmdsurface.Quota, error) {
	names := []string{svc}
	if svc == "" {
		names = nil
		if r.serveReg != nil {
			names = r.serveReg.Names()
		}
	}
	var out []cmdsurface.Quota
	for _, name := range names {
		q, on, err := serveQuota(r.Viper, name)
		if err != nil {
			return nil, output.UsageError(err.Error())
		}
		if on {
			out = append(out, q)
		}
	}
	if len(out) == 0 {
		e := output.NotFoundError("no quota is configured for " + orService(svc))
		e.SuggestedFix = "set services.<svc>.quota.ops or .bytes"
		return nil, e
	}
	return out, nil
}

func orService(svc string) string {
	if svc == "" {
		return "any service"
	}
	return "service " + svc
}

// quotaPrefix is the ledger prefix of every key q counts.
func quotaPrefix(q cmdsurface.Quota) string {
	return "quota/" + url.PathEscape(q.Scope) + "/"
}

// quotaRows reads the current window's usage of every counted caller.
func quotaRows(ctx context.Context, r *Root, svc string) ([]QuotaRow, error) {
	if err := r.loadServiceConfig(); err != nil {
		return nil, err
	}
	quotas, err := quotaServices(r, svc)
	if err != nil {
		return nil, err
	}
	ledger, err := usageLedgerOf(r)
	if err != nil {
		return nil, err
	}
	rows := []QuotaRow{}
	for _, q := range quotas {
		keys, err := ledger.Keys(ctx, quotaPrefix(q))
		if err != nil {
			return nil, err
		}
		for _, k := range keys {
			u, err := ledger.Usage(ctx, k, q.Window)
			if err != nil {
				return nil, err
			}
			if u.Ops == 0 && u.Bytes == 0 {
				continue // counted in an earlier window only
			}
			rows = append(rows, QuotaRow{
				Service:  q.Scope,
				Caller:   strings.TrimPrefix(k, quotaPrefix(q)),
				Ops:      u.Ops,
				MaxOps:   q.Ops,
				Bytes:    u.Bytes,
				MaxBytes: q.Bytes,
				Window:   q.Window.String(),
				Resets:   u.Reset.UTC(),
			})
		}
	}
	return rows, nil
}

// quotaReset clears caller's counts — every caller's when caller is
// empty — and reports how many it cleared.
func quotaReset(ctx context.Context, r *Root, svc, caller string) (int, error) {
	if err := r.loadServiceConfig(); err != nil {
		return 0, err
	}
	quotas, err := quotaServices(r, svc)
	if err != nil {
		return 0, err
	}
	ledger, err := usageLedgerOf(r)
	if err != nil {
		return 0, err
	}
	n := 0
	for _, q := range quotas {
		keys, err := ledger.Keys(ctx, quotaPrefix(q)+caller)
		if err != nil {
			return n, err
		}
		for _, k := range keys {
			if caller != "" && k != quotaPrefix(q)+caller {
				continue
			}
			if err := ledger.Reset(ctx, k); err != nil {
				return n, err
			}
			n++
		}
	}
	return n, nil
}
