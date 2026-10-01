package cli

import (
	"fmt"
	"strings"

	"github.com/spf13/cobra"

	"hop.top/kit/go/console/cli/svcconfig"
	kitlog "hop.top/kit/go/console/log"
	"hop.top/kit/go/console/output"
	"hop.top/kit/go/console/serve"
)

// WithService returns a Root option registering svc as a `serve`
// child, mounting the kit-owned serve parent on first use.
//
// Registering a name twice panics at construction time, matching the
// registry's contract: a collision is a wiring bug in main,
// discoverable on the first run rather than at the first serve. An
// adopter deliberately replacing a kit-shipped service uses
// [WithServiceOverride].
func WithService(svc serve.Service) func(*Root) {
	return func(r *Root) {
		r.ensureServeRegistry()
		r.serveReg.Register(svc)
	}
}

// WithServices registers several services in one option, in the order
// given. It is exactly repeated [WithService] and carries the same
// duplicate-name panic.
func WithServices(svcs ...serve.Service) func(*Root) {
	return func(r *Root) {
		r.ensureServeRegistry()
		for _, svc := range svcs {
			r.serveReg.Register(svc)
		}
	}
}

// WithServiceOverride registers svc, replacing any service already
// registered under its name and keeping that name's position in the
// listing. This is the documented escape hatch for an adopter
// swapping a kit-shipped service — notably the built-in `api` service
// [WithAPI] registers — for one of its own.
func WithServiceOverride(svc serve.Service) func(*Root) {
	return func(r *Root) {
		r.ensureServeRegistry()
		r.serveReg.Override(svc)
	}
}

// WithServicePolicy wires the gate the third validation step consults
// (serve-lifecycle.md §"The override rule"). Without one, every service passes
// the policy gate: a tool that has not wired a policy table has not
// expressed a restriction.
func WithServicePolicy(gate serve.PolicyGate) func(*Root) {
	return func(r *Root) {
		r.ensureServeRegistry()
		r.servePolicy = gate
	}
}

// WithServiceBus wires the bus the supervisor publishes lifecycle
// events to. Without one, the log counterpart still runs, so a tool
// with no bus still produces an operator-legible startup trace.
func WithServiceBus(p serve.Publisher) func(*Root) {
	return func(r *Root) {
		r.ensureServeRegistry()
		r.serveBus = p
	}
}

// ensureServeRegistry creates the registry and mounts the serve parent
// on first use. Mounting here rather than in New is what lets the
// registry take the `serve` word away from WithAPI's leaf: whichever
// option runs, exactly one command owns the word (serve-lifecycle.md
// §"Compatibility").
func (r *Root) ensureServeRegistry() {
	if r.serveReg != nil {
		return
	}
	r.serveReg = serve.NewRegistry()

	// The registry wins: drop any leaf `serve` a prior WithAPI
	// mounted. The two MUST NOT both own the word.
	for _, c := range r.Cmd.Commands() {
		if c.Name() == "serve" {
			r.Cmd.RemoveCommand(c)
		}
	}
	r.Cmd.AddCommand(serveParentCmd(r))
}

// ServeRegistry returns the registry `serve` children are registered
// into, or nil when no service has been registered. Adopters that
// need to inspect or extend the set after construction use it; the
// normal path is [WithService].
func (r *Root) ServeRegistry() *serve.Registry { return r.serveReg }

// serveParentCmd builds the kit-owned `serve` command.
//
// With no positional argument it is the supervisor over every
// configured and enabled service; with exactly one it is the selector,
// which overrides aggregate enablement. Both forms share one lifecycle
// implementation, so a single service started by the selector observes
// the same readiness, shutdown, and exit semantics as the same service
// started by the supervisor (serve-lifecycle.md §"Command hierarchy").
func serveParentCmd(root *Root) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "serve [service]",
		Short: "Run configured services under one lifecycle",
		Long: "Run every configured and enabled service under one lifecycle,\n" +
			"or exactly the named service.\n\n" +
			"Naming a service starts it even when it is not enabled, provided\n" +
			"its configuration and policy validate.",
		Args: cobra.MaximumNArgs(1),
		ValidArgsFunction: func(_ *cobra.Command, _ []string, _ string) ([]string, cobra.ShellCompDirective) {
			if root.serveReg == nil {
				return nil, cobra.ShellCompDirectiveNoFileComp
			}
			return root.serveReg.Names(), cobra.ShellCompDirectiveNoFileComp
		},
		RunE: func(cmd *cobra.Command, args []string) error {
			if list, _ := cmd.Flags().GetBool("list"); list {
				return runServeList(cmd, root)
			}
			return runServe(cmd, root, args)
		},
	}

	SetSideEffect(cmd, SideEffectWriteShared)
	SetIdempotency(cmd, IdempotencyNo)
	// The schema describes `serve --list`, the only form that renders
	// a document. The long-running forms (the supervisor, a named
	// service) emit logs while they run, never a document to parse.
	_ = SetOutputSchema(cmd, OutputSchema{Type: &[]serveListRow{}, Version: "1.0"})

	// `list` is reserved selector vocabulary, so the listing is a flag
	// rather than a child: registering a `list` service is refused by
	// the registry, and a `serve list` child would be ambiguous with
	// the selector form for exactly that reason.
	cmd.Flags().Bool("list", false, "List registered services and their state")
	cmd.Flags().StringSlice("enable", nil,
		"Enable a service for this run (repeatable, supervisor form only)")
	cmd.Flags().StringSlice("disable", nil,
		"Disable a service for this run (repeatable, supervisor form only)")
	cmd.Flags().Duration("ready-timeout", 0,
		"Per-service budget from start to ready (default 30s)")
	cmd.Flags().Duration("stop-timeout", 0,
		"Per-service budget for one stop (default 30s)")
	cmd.Flags().Duration("shutdown-timeout", 0,
		"Total shutdown budget across all services (default 60s)")

	return cmd
}

// runServe resolves the invocation and runs the resulting set.
func runServe(cmd *cobra.Command, root *Root, args []string) error {
	// Validation may open audit chains before a later step fails;
	// whatever path this returns by, they are released.
	defer func() { _ = root.closeAuditChains() }()
	// The idempotency store is opened on the first keyed call.
	defer func() { _ = root.closeServeIdempotency() }()
	reg := root.serveReg
	if reg == nil {
		return output.UsageError("no services registered")
	}
	if err := root.loadServiceConfig(); err != nil {
		return err
	}
	// The configuration gate every service shares: an unknown key in
	// a middleware block of any service, or anything but a middleware
	// block under services.all, is refused before a service is chosen.
	if err := svcconfig.New(root.Viper).Validate(); err != nil {
		return output.UsageError(err.Error())
	}
	// A service with no HTTP listener has nothing for an HTTP-plane
	// block to act on; one set for it is refused, not ignored.
	if err := validateSocketBlocks(root); err != nil {
		return output.UsageError(err.Error())
	}

	supCfg, badPolicy := serveSupervisorConfig(root.Viper)
	if badPolicy != "" {
		e := output.UsageError(fmt.Sprintf(
			"services.failure_policy: unknown policy %q", badPolicy,
		))
		e.SuggestedFix = fmt.Sprintf("use %q or %q", serve.FailFast, serve.Isolate)
		return e
	}
	if d, _ := cmd.Flags().GetDuration("shutdown-timeout"); d > 0 {
		supCfg.ShutdownTimeout = d
	}

	enable, _ := cmd.Flags().GetStringSlice("enable")
	disable, _ := cmd.Flags().GetStringSlice("disable")
	if len(args) == 1 && (len(enable) > 0 || len(disable) > 0) {
		// Under the selector form the override rule already decides
		// enablement; accepting the flags too would let one
		// invocation say two contradictory things.
		return output.UsageError(
			"--enable/--disable apply to the supervisor form; drop the service name or drop the flags",
		)
	}

	configs := serveConfigs(root.Viper, reg.Names(), enable, disable)
	readyTO, _ := cmd.Flags().GetDuration("ready-timeout")
	stopTO, _ := cmd.Flags().GetDuration("stop-timeout")
	serveTimeoutOverrides(configs, readyTO, stopTO)
	applyAPICompat(cmd, root, configs)
	applySocketFlags(cmd, root)

	outcome := serve.Resolve(reg, serve.Request{
		Args:    args,
		Configs: configs,
		Policy:  root.servePolicy,
	})
	if outcome.Err != nil {
		return outcome.Err
	}
	if IsDryRun(cmd) {
		// The invocation validated and resolved as a real one would;
		// the preview names the services it would start.
		plan := Plan{Args: map[string]any{"services": outcome.Selected}}
		for _, name := range outcome.Selected {
			plan.Effects = append(plan.Effects, Effect{Kind: "start",
				Target: "service:" + name, Reversible: true,
				Detail: "run until stopped"})
		}
		return RenderPlan(cmd, plan)
	}

	// The supervisor owns the signals from here: the first begins the
	// drain, a second aborts it (serve-lifecycle.md §"Signals").
	ctx, escalate, stop := serve.SignalContext(cmd.Context())
	defer stop()

	// Tracing and metrics start before any service and flush after
	// the last one stops, so every request the services answer is
	// recorded and exported.
	flushObservability, err := root.startObservability(ctx, reg.Names())
	if err != nil {
		return err
	}
	defer flushObservability()

	// The trace goes to the command's stderr rather than straight to
	// os.Stderr, so a caller that redirects the command's streams
	// (a test, a wrapper, a supervisor capturing output) still sees
	// the startup and shutdown trace.
	logger := kitlog.New(root.Viper)
	logger.SetOutput(cmd.ErrOrStderr())

	sup := serve.NewSupervisor(
		reg, supCfg,
		serve.WithLogger(logger),
		serve.WithPublisher(root.serveBus),
		serve.WithEscalation(escalate),
	)

	res := sup.Run(ctx, outcome.Selected, configs)
	// Every service has stopped: nothing can audit any more, so the
	// chains they opened are synced and released.
	if err := root.closeAuditChains(); err != nil && res.Err == nil {
		return err
	}
	if res.Err != nil {
		return res.Err
	}
	return nil
}

// serveListRow is one row of `serve --list`: a registered service and
// whether it is configured, enabled for a bare `serve`, and ready.
type serveListRow struct {
	Service    string `json:"service"    yaml:"service"    table:"SERVICE"`
	Configured bool   `json:"configured" yaml:"configured" table:"CONFIGURED"`
	Enabled    bool   `json:"enabled"    yaml:"enabled"    table:"ENABLED"`
	Ready      bool   `json:"ready"      yaml:"ready"      table:"READY"`
}

// runServeList prints the registered services with their configured,
// enabled, and ready state, in registration order so the listing
// mirrors the adopter's wiring (serve-lifecycle.md §"Command hierarchy").
func runServeList(cmd *cobra.Command, root *Root) error {
	reg := root.serveReg
	if reg == nil {
		return output.UsageError("no services registered")
	}
	if err := root.loadServiceConfig(); err != nil {
		return err
	}

	configs := serveConfigs(root.Viper, reg.Names(), nil, nil)
	// The listing reads the same resolution the supervisor runs on,
	// WithAPI's default-on included; anything else would list a
	// service as disabled that a bare `serve` is about to start.
	applyAPIEnabledDefault(root, configs)
	rows := []serveListRow{}
	for _, svc := range reg.List() {
		name := svc.Name()
		cfg, configured := configs[name]
		rows = append(rows, serveListRow{Service: name, Configured: configured, Enabled: cfg.Enabled, Ready: svc.Ready()})
	}
	// The table keeps its fixed-width layout: generated project
	// READMEs and the adopter docs print it verbatim, and the table
	// formatter sizes columns to their content. Every other format is
	// Dispatch's.
	if activeOutputFormat(cmd, root.Viper) == output.Table {
		w := cmd.OutOrStdout()
		fmt.Fprintf(w, "%-20s %-11s %-8s %s\n", "SERVICE", "CONFIGURED", "ENABLED", "READY")
		for _, row := range rows {
			fmt.Fprintf(w, "%-20s %-11t %-8t %t\n", row.Service, row.Configured, row.Enabled, row.Ready)
		}
		return nil
	}
	return output.Dispatch(cmd, root.Viper, rows)
}

// serveHelpAddendum appends the registered service names to the serve
// command's help, so `serve --help` answers "which services can I
// name?" without a second invocation.
func (r *Root) serveHelpAddendum() {
	if r.serveReg == nil {
		return
	}
	names := r.serveReg.Names()
	if len(names) == 0 {
		return
	}
	for _, c := range r.Cmd.Commands() {
		if c.Name() != "serve" {
			continue
		}
		addendum := "\nServices: " + strings.Join(names, ", ")
		if !strings.Contains(c.Long, addendum) {
			c.Long += "\n" + addendum
		}
		return
	}
}
