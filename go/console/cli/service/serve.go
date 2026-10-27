package service

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/spf13/cobra"
	"github.com/spf13/pflag"

	"hop.top/kit/go/console/cli"
	"hop.top/kit/go/console/cli/cmdmeta"
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
func WithService(svc serve.Service) func(*cli.Root) {
	return func(r *cli.Root) {
		ensureServeRegistry(r)
		stateOf(r).serveReg.Register(svc)
	}
}

// WithServices registers several services in one option, in the order
// given. It is exactly repeated [WithService] and carries the same
// duplicate-name panic.
func WithServices(svcs ...serve.Service) func(*cli.Root) {
	return func(r *cli.Root) {
		ensureServeRegistry(r)
		for _, svc := range svcs {
			stateOf(r).serveReg.Register(svc)
		}
	}
}

// WithServiceOverride registers svc, replacing any service already
// registered under its name and keeping that name's position in the
// listing. This is the documented escape hatch for an adopter
// swapping a kit-shipped service — notably the built-in `api` service
// [WithAPI] registers — for one of its own.
func WithServiceOverride(svc serve.Service) func(*cli.Root) {
	return func(r *cli.Root) {
		ensureServeRegistry(r)
		stateOf(r).serveReg.Override(svc)
	}
}

// WithServicePolicy wires the gate the third validation step consults
// (serve-lifecycle.md §"The override rule"). Without one, every service passes
// the policy gate: a tool that has not wired a policy table has not
// expressed a restriction.
func WithServicePolicy(gate serve.PolicyGate) func(*cli.Root) {
	return func(r *cli.Root) {
		ensureServeRegistry(r)
		stateOf(r).servePolicy = gate
	}
}

// WithServiceBus wires the bus the supervisor publishes lifecycle
// events to. Without one, the log counterpart still runs, so a tool
// with no bus still produces an operator-legible startup trace.
func WithServiceBus(p serve.Publisher) func(*cli.Root) {
	return func(r *cli.Root) {
		ensureServeRegistry(r)
		stateOf(r).serveBus = p
	}
}

// WithOwnServiceConfig declares that the tool layers services.* into
// [cli.Root.Viper] itself, so `service start` (the supervisor and the
// selector), `service list` and the quota commands read services.*
// exactly as the tool left it.
//
// By default those commands layer services.* on their own before
// reading it: the services block of the tool's system, user and
// project config files (the project file discovered from the working
// directory up to $HOME), of each -c <path>, every <TOOL>_SERVICES_*
// variable and every -c services.<key>=<value>, merged above whatever
// the tool put at Viper's config-file rung. A tool with its own loader
// (its own file order, profiles, its own environment prefix) passes
// this option so that none of that happens: no file is discovered or
// read, no variable is bound and no -c token is applied by kit. The
// tool's loader is then the only source, -c included; read the tokens
// with [cli.Root.ConfigArgs]. The `service start` flags (--addr, --socket,
// --enable, --disable and the timeouts) still apply for one run. The
// deprecated root `serve` and its --list run the same paths.
func WithOwnServiceConfig() func(*cli.Root) {
	return func(r *cli.Root) { stateOf(r).serveOwnConfig = true }
}

// ensureServeRegistry creates the registry and mounts the `service`
// group on first use: `service start`, `service list`, `service stop`,
// `service restart` and `service status`, plus the hidden, deprecated
// root `serve` that runs `service start`. Mounting here rather than in
// New is what lets the registry take the `serve` word away from any
// leaf a prior option mounted: whichever option runs, exactly one
// command owns the word (serve-lifecycle.md §"Compatibility").
func ensureServeRegistry(r *cli.Root) {
	st := stateOf(r)
	if st.serveReg != nil {
		return
	}
	st.serveReg = serve.NewRegistry()

	// The registry wins: drop any leaf `serve` a prior option
	// mounted. The two MUST NOT both own the word.
	for _, c := range r.Cmd.Commands() {
		if c.Name() == serveAliasName {
			r.Cmd.RemoveCommand(c)
		}
	}
	st.serveCmd = serviceStartCmd(r)
	st.serviceCmd = serviceGroupCmd(r, st.serveCmd)
	st.serveAlias = serveAliasCmd(r)
	r.Cmd.AddCommand(st.serviceCmd, st.serveAlias)
	// Reserved whether the registry was created by an option inside
	// New (where the snapshot would catch them) or later.
	r.MarkReserved(ServiceCommandName)
	r.MarkReserved(serveAliasName)
}

// ServiceCommandName is the word kit mounts its service lifecycle
// group under: `<tool> service start|list|stop|restart|status`.
const ServiceCommandName = "service"

// serveAliasName is the deprecated root word for `service start`.
const serveAliasName = "serve"

// ServeRegistry returns the registry `service start` selects from, or
// nil when no service has been registered. Adopters that need to
// inspect or extend the set after construction use it; the normal path
// is [WithService].
func ServeRegistry(r *cli.Root) *serve.Registry {
	if s, ok := lookup(r); ok {
		return s.serveReg
	}
	return nil
}

// ServiceCommand returns the kit-owned `service` group, or nil when no
// service is registered. It is the extension point for an adopter's own
// lifecycle leaves:
//
//	service.ServiceCommand(root).AddCommand(setupCmd) // <tool> service setup
//
// A leaf added here is a management command like kit's own: withheld
// from every served surface and exempt from the shape checks. The
// words start, list, stop, restart and status are kit's.
func ServiceCommand(r *cli.Root) *cobra.Command {
	if s, ok := lookup(r); ok {
		return s.serviceCmd
	}
	return nil
}

// ServeCommand returns kit's `service start` command, the one that
// runs the services, or nil when no service is registered. Services
// that put their own flags on it, like the mcp and rpc services, find
// it here; the deprecated root `serve` shares every one of its flags.
func ServeCommand(r *cli.Root) *cobra.Command {
	if s, ok := lookup(r); ok {
		return s.serveCmd
	}
	return nil
}

// serviceGroupCmd builds the `service` group around start.
func serviceGroupCmd(root *cli.Root, start *cobra.Command) *cobra.Command {
	cmd := &cobra.Command{
		Use:   ServiceCommandName,
		Short: "Run, inspect, stop and restart the tool's services",
		Long: "Run the tool's services under one lifecycle, list them, and ask a\n" +
			"running instance for its status, to stop, or to restart in place.",
	}
	cmd.AddCommand(start, serviceListCmd(root), serviceStopCmd(root),
		serviceRestartCmd(root), serviceStatusCmd(root))
	return cmd
}

// serviceStartCmd builds `service start`.
//
// With no positional argument it is the supervisor over every
// configured and enabled service; with exactly one it is the selector,
// which overrides aggregate enablement. Both forms share one lifecycle
// implementation, so a single service started by the selector observes
// the same readiness, shutdown, and exit semantics as the same service
// started by the supervisor (serve-lifecycle.md §"Command hierarchy").
func serviceStartCmd(root *cli.Root) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "start [service]",
		Short: "Run configured services under one lifecycle",
		Long: "Run every configured and enabled service under one lifecycle,\n" +
			"or exactly the named service.\n\n" +
			"Naming a service starts it even when it is not enabled, provided\n" +
			"its configuration and policy validate.",
		Args:              cobra.MaximumNArgs(1),
		ValidArgsFunction: serviceNames(root),
		RunE: func(cmd *cobra.Command, args []string) error {
			return runServe(cmd, root, args)
		},
	}
	annotateServeRunner(cmd)
	// The running forms emit logs, never a document; the one document
	// `service start` renders is its --dry-run plan.
	_ = cli.SetOutputSchema(cmd, cli.OutputSchema{Type: &cli.Plan{}, Version: "1.0"})
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

// annotateServeRunner declares what running the services is: a shared
// write that is not idempotent, and self-hosting by its own mark rather
// than only by the word serve, so `service start` is withheld from
// every served surface — starting it inside a served invocation would
// start a server inside the server.
func annotateServeRunner(cmd *cobra.Command) {
	cli.SetSideEffect(cmd, cli.SideEffectWriteShared)
	cli.SetIdempotency(cmd, cli.IdempotencyNo)
	setAnnotationTrue(cmd, cmdmeta.KeySelfHosting)
}

// serveAliasCmd builds the hidden root `serve`, the deprecated spelling
// of `service start`. It shares every flag of `service start` (see
// syncServeAlias) and keeps the `--list` flag `service list` replaced.
func serveAliasCmd(root *cli.Root) *cobra.Command {
	cmd := &cobra.Command{
		Use:               serveAliasName + " [service]",
		Short:             "Deprecated: use `service start`",
		Long:              "Deprecated spelling of `service start`; `serve --list` is `service list`.",
		Hidden:            true,
		Args:              cobra.MaximumNArgs(1),
		ValidArgsFunction: serviceNames(root),
		RunE: func(cmd *cobra.Command, args []string) error {
			if list, _ := cmd.Flags().GetBool("list"); list {
				return runServeList(cmd, root)
			}
			return runServe(cmd, root, args)
		},
	}
	annotateServeRunner(cmd)
	cmd.Annotations[cmdmeta.KeyDeprecatedSince] = serveAliasDeprecatedSince
	cmd.Annotations[cmdmeta.KeyReplacedBy] = ServiceCommandName + " start"
	cmd.Flags().Bool("list", false, "Deprecated: use `service list`")
	// Unchanged from when this was the serve command: the schema
	// describes `serve --list`.
	_ = cli.SetOutputSchema(cmd, cli.OutputSchema{Type: &[]serveListRow{}, Version: "1.0"})
	return cmd
}

// serveAliasDeprecatedSince is the kit release that moved `serve` to
// `service start`.
const serveAliasDeprecatedSince = "0.5.0-alpha.25"

// syncServeAlias gives the deprecated `serve` every flag `service
// start` has. Services register theirs (--addr, --socket, --mcp-addr,
// ...) on `service start` as their options run; the alias takes the
// same *pflag.Flag, so a value parsed on either command is the value
// the services read. Idempotent; run before argv is parsed.
func syncServeAlias(r *cli.Root) {
	st := stateOf(r)
	if st.serveCmd == nil || st.serveAlias == nil {
		return
	}
	inherited := st.serveCmd.InheritedFlags()
	st.serveCmd.Flags().VisitAll(func(f *pflag.Flag) {
		if f.Name == "help" || inherited.Lookup(f.Name) != nil {
			return
		}
		if st.serveAlias.Flags().Lookup(f.Name) == nil {
			st.serveAlias.Flags().AddFlag(f)
		}
	})
}

// serviceNames completes a service name from the registry.
func serviceNames(root *cli.Root) func(*cobra.Command, []string, string) ([]string, cobra.ShellCompDirective) {
	st := stateOf(root)
	return func(_ *cobra.Command, _ []string, _ string) ([]string, cobra.ShellCompDirective) {
		if st.serveReg == nil {
			return nil, cobra.ShellCompDirectiveNoFileComp
		}
		return st.serveReg.Names(), cobra.ShellCompDirectiveNoFileComp
	}
}

// serviceListCmd builds `service list`.
func serviceListCmd(root *cli.Root) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "list",
		Short: "List registered services and their state",
		Long: "List every registered service with whether it is configured,\n" +
			"enabled for a bare `service start`, and ready in this process.",
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			return runServeList(cmd, root)
		},
	}
	cli.SetSideEffect(cmd, cli.SideEffectRead)
	cli.SetIdempotency(cmd, cli.IdempotencyYes)
	_ = cli.SetOutputSchema(cmd, cli.OutputSchema{Type: &[]serveListRow{}, Version: "1.0"})
	return cmd
}

// runServe resolves the invocation and runs the resulting set.
func runServe(cmd *cobra.Command, root *cli.Root, args []string) (err error) {
	// A `service restart` re-executes the process once everything
	// below is released: deferred first, this runs after every other
	// deferred cleanup (service_restart.go).
	var restart *pendingRestart
	defer func() {
		if restart != nil {
			err = restart.run(err)
		}
	}()
	// Validation may open audit chains before a later step fails;
	// whatever path this returns by, they are released.
	defer func() { _ = closeAuditChains(root) }()
	// The idempotency store is opened on the first keyed call.
	defer func() { _ = closeServeIdempotency(root) }()
	st := stateOf(root)
	reg := st.serveReg
	if reg == nil {
		return output.UsageError("no services registered")
	}
	if err := loadServiceConfig(root); err != nil {
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

	if err := validateServiceOptional(root.Viper, reg); err != nil {
		return output.UsageError(err.Error())
	}
	configs := serveConfigs(root.Viper, reg.Names(), enable, disable)
	applyAPICompat(cmd, root, configs)
	applySocketFlags(cmd, root)

	outcome := serve.Resolve(reg, serve.Request{
		Args:    args,
		Configs: configs,
		Policy:  st.servePolicy,
	})
	if outcome.Err != nil {
		return outcome.Err
	}
	// Whether the run depends on each service is settled for what it
	// runs, the selector form's unconfigured service included; the
	// timeout flags then reach every entry, that one too.
	if err := applyServiceOptional(root.Viper, reg, configs, outcome.Selected); err != nil {
		return output.UsageError(err.Error())
	}
	readyTO, _ := cmd.Flags().GetDuration("ready-timeout")
	stopTO, _ := cmd.Flags().GetDuration("stop-timeout")
	serveTimeoutOverrides(configs, readyTO, stopTO)
	// Where `service stop`, `service restart` and `service status`
	// reach this run: resolved here, after the tool's own
	// configuration and profile resolution, so a bad scope is refused
	// by a dry run as well.
	ep, err := controlEndpointFor(root, cmd)
	if err != nil {
		return err
	}
	if cli.IsDryRun(cmd) {
		// The invocation validated and resolved as a real one would;
		// the preview names the services it would start.
		plan := cli.Plan{Args: map[string]any{"services": outcome.Selected}}
		for _, name := range outcome.Selected {
			plan.Effects = append(plan.Effects, cli.Effect{Kind: "start",
				Target: "service:" + name, Reversible: true,
				Detail: "run until stopped"})
		}
		return cli.RenderPlan(cmd, plan)
	}

	// The supervisor owns the signals from here: the first begins the
	// drain, a second aborts it (serve-lifecycle.md §"Signals").
	sigCtx, escalate, stop := serve.SignalContext(cmd.Context())
	defer stop()
	// `service stop` and `service restart` end the run the way the
	// first signal does: by canceling the context the supervisor runs
	// on.
	ctx, stopRequested := context.WithCancel(sigCtx)
	defer stopRequested()

	// `service status` reports the supervisor's record of the run,
	// handed over as the run begins.
	record := &runRecord{}
	// The control socket `service stop`, `service restart` and
	// `service status` talk to, claimed before anything starts. A live
	// instance already on it is a CONFLICT: a second run would serve
	// with no control channel, unreachable but by signal, while those
	// commands went on reaching the first. Any other failure to listen
	// (a platform or sandbox without unix sockets) leaves the run
	// serving without one, and says so.
	control, controlErr := listenControl(ep, root.Config.Name+" "+ServiceCommandName+" stop",
		stopRequested, serviceStatusRows(reg, outcome.Selected, configs, record), func(ctx context.Context) []cli.StatusSection { return cli.ServiceStatusSections(ctx, root) })
	var refused *output.Error
	if errors.As(controlErr, &refused) {
		return controlErr
	}
	// Released by close after the run; this covers the returns below
	// that come before it.
	defer func() {
		if control != nil {
			control.close(1)
		}
	}()

	// Tracing and metrics start before any service and flush after
	// the last one stops, so every request the services answer is
	// recorded and exported.
	flushObservability, err := startObservability(root, ctx, reg.Names())
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
		serve.WithPublisher(st.serveBus),
		serve.WithEscalation(escalate),
		serve.WithRunObserver(record.set),
	)

	if controlErr != nil {
		logger.Warn("service control socket unavailable; service stop and service status will not reach this instance",
			"error", controlErr)
	}

	res := sup.Run(ctx, outcome.Selected, configs)
	cs := control
	control = nil
	if cs != nil && cs.close(res.ExitCode()) {
		restart = &pendingRestart{
			control: cs,
			logger:  logger,
			// Read before the deferred stop cancels it: set only by a
			// shutdown signal or the caller.
			signaled: sigCtx.Err() != nil,
		}
	}
	// Every service has stopped: nothing can audit any more, so the
	// chains they opened are synced and released.
	if err := closeAuditChains(root); err != nil && res.Err == nil {
		return err
	}
	if res.Err != nil {
		return res.Err
	}
	return nil
}

// serveListRow is one row of `service list`: a registered service and
// whether it is configured, enabled for a bare `service start`, ready,
// and optional.
type serveListRow struct {
	Service    string `json:"service"    yaml:"service"    table:"SERVICE"`
	Configured bool   `json:"configured" yaml:"configured" table:"CONFIGURED"`
	Enabled    bool   `json:"enabled"    yaml:"enabled"    table:"ENABLED"`
	Ready      bool   `json:"ready"      yaml:"ready"      table:"READY"`
	Optional   bool   `json:"optional"   yaml:"optional"   table:"OPTIONAL"`
}

// runServeList prints the registered services with their configured,
// enabled, ready and optional state, in registration order so the
// listing mirrors the adopter's wiring (serve-lifecycle.md §"Command
// hierarchy"). OPTIONAL is the last column, so the earlier columns keep
// their positions for a script that reads them.
func runServeList(cmd *cobra.Command, root *cli.Root) error {
	reg := stateOf(root).serveReg
	if reg == nil {
		return output.UsageError("no services registered")
	}
	if err := loadServiceConfig(root); err != nil {
		return err
	}

	configs := serveConfigs(root.Viper, reg.Names(), nil, nil)
	// The listing reads the same resolution the supervisor runs on,
	// WithAPI's default-on included; anything else would list a
	// service as disabled that a bare `serve` is about to start.
	applyAPIEnabledDefault(root, configs)
	if err := validateServiceOptional(root.Viper, reg); err != nil {
		return output.UsageError(err.Error())
	}
	rows := []serveListRow{}
	for _, svc := range reg.List() {
		name := svc.Name()
		cfg, configured := configs[name]
		// Optional reads the same resolution the supervisor runs on,
		// for a service not yet configured too: it is what applies
		// the moment one is.
		optional, _ := serviceOptional(root.Viper, svc) // validated above
		rows = append(rows, serveListRow{
			Service: name, Configured: configured, Enabled: cfg.Enabled, Ready: svc.Ready(),
			Optional: optional,
		})
	}
	// The table keeps its fixed-width layout: generated project
	// READMEs and the adopter docs print it verbatim, and the table
	// formatter sizes columns to their content. Every other format is
	// Dispatch's.
	if cli.ActiveFormat(cmd, root.Viper) == output.Table {
		w := cmd.OutOrStdout()
		fmt.Fprintf(w, "%-20s %-11s %-8s %-6s %s\n", "SERVICE", "CONFIGURED", "ENABLED", "READY", "OPTIONAL")
		for _, row := range rows {
			fmt.Fprintf(w, "%-20s %-11t %-8t %-6t %t\n",
				row.Service, row.Configured, row.Enabled, row.Ready, row.Optional)
		}
		return nil
	}
	return output.Dispatch(cmd, root.Viper, rows)
}

// serveHelpAddendum appends the registered service names to the help
// of `service start`, so `service start --help` answers "which services
// can I name?" without a second invocation.
func serveHelpAddendum(r *cli.Root) {
	st := stateOf(r)
	if st.serveReg == nil {
		return
	}
	names := st.serveReg.Names()
	if len(names) == 0 {
		return
	}
	addendum := "\nServices: " + strings.Join(names, ", ")
	for _, c := range []*cobra.Command{st.serveCmd, st.serveAlias} {
		if c != nil && !strings.Contains(c.Long, addendum) {
			c.Long += "\n" + addendum
		}
	}
}
