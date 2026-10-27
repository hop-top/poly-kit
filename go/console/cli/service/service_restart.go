package service

import (
	"errors"
	"fmt"
	"net"
	"os"
	"strings"
	"time"

	"charm.land/log/v2"
	"github.com/spf13/cobra"

	"hop.top/kit/go/console/cli"
	"hop.top/kit/go/console/cli/cmdmeta"
	"hop.top/kit/go/console/output"
	"hop.top/kit/go/console/serve"
)

// `service restart`.
//
// The running instance drains exactly as `service stop` makes it (the
// run's context is canceled; the supervisor stops every service in
// reverse start order within its budgets), releases what the run held,
// and then replaces its own process image with the same binary, the
// arguments and environment it was launched with, from the directory
// it was launched in. The pid is kept, so an OS service manager
// (systemd, launchd) sees no exit; configuration, flags and the binary
// itself are read afresh by the new run.
//
// The instance does not re-execute when the run did not end because of
// the restart alone: a shutdown signal or a `service stop` during the
// drain wins and the process exits as a stop; a drain that did not end
// cleanly exits with the run's failure. If the re-execution itself
// fails the process exits non-zero with the reason on stderr, which is
// the signal for a service manager to start it again.

// defaultRestartWait bounds how long `service restart` waits: the
// drain, then the new run's readiness.
const defaultRestartWait = defaultStopWait + serve.DefaultReadyTimeout

// restartPollInterval is how often the client asks the new instance
// whether it is up and ready.
const restartPollInterval = 50 * time.Millisecond

// errReexecUnsupported is the refusal on a platform without exec(2).
var errReexecUnsupported = errors.New(
	"service restart is not supported on this platform: it re-executes the process in place, which needs exec(2)")

// The launch state a restart re-executes with, captured at package
// initialization — before main runs and before -C changes the working
// directory — so the new run starts exactly as this one did.
var (
	launchArgs = append([]string(nil), os.Args...)
	launchEnv  = os.Environ()
	launchDir  = launchWorkingDir()
)

func launchWorkingDir() string {
	dir, err := os.Getwd()
	if err != nil {
		return ""
	}
	return dir
}

// execSelf replaces the process image. Tests swap it.
var execSelf = reexec

// pendingRestart is a restart the control channel asked a run for,
// carried out once runServe has released everything the run held.
type pendingRestart struct {
	control *controlServer
	logger  *log.Logger
	// signaled is set when a shutdown signal (or the caller's context)
	// ended the run as well: that stop wins.
	signaled bool
}

// run re-executes the process when the drain allows it. runErr is the
// run's result; what run returns is the run's result when it does not
// re-execute, and the re-execution failure when that fails.
func (p *pendingRestart) run(runErr error) error {
	switch {
	case p.signaled:
		p.control.finishRestart("a shutdown signal arrived during the drain")
		return runErr
	case runErr != nil:
		p.control.finishRestart("the drain did not end cleanly: " + runErr.Error())
		return runErr
	}
	exe, err := os.Executable()
	if err == nil && launchDir != "" {
		err = os.Chdir(launchDir)
	}
	if err == nil {
		p.logger.Info("serve: restarting", "object", "supervisor", "path", exe, "pid", os.Getpid())
		err = execSelf(exe, launchArgs, launchEnv)
	}
	if err == nil {
		// Only a stand-in for exec returns without an error; the
		// real one does not return at all on success.
		p.control.finishRestart("")
		return nil
	}
	e := output.GenericError(fmt.Sprintf(
		"restart: re-executing %s failed after the drain: %v", exe, err))
	e.SuggestedFix = "start it again with `" + launchCommand() + "`"
	p.control.finishRestart(e.Message)
	return e
}

// launchCommand is the launch argv as one line, for messages.
func launchCommand() string {
	return strings.Join(launchArgs, " ")
}

// serviceRestartCmd builds `service restart`.
func serviceRestartCmd(root *cli.Root) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "restart",
		Short: "Drain the running instance and re-execute it in place",
		Long: "Ask the running instance, over its control socket, to drain the way\n" +
			"`service stop` does and then re-execute itself in place: the same\n" +
			"binary, arguments and environment, under the same pid, so a service\n" +
			"manager sees no exit. The new run reads its configuration and flags\n" +
			"afresh. Waits for the new instance to answer with every required service\n" +
			"ready or stopped and prints its status, a failed optional service among\n" +
			"it. A required service that failed fails the restart at once, exit 1, as\n" +
			"does a new run with nothing left running.\n\n" +
			"Exits 3 (NOT_FOUND) when no instance is running, like `service status`:\n" +
			"there is nothing to restart, and restart does not start one.",
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			return runServiceRestart(cmd, root)
		},
	}
	cli.SetSideEffect(cmd, cli.SideEffectWriteShared)
	// Each call interrupts the services once more.
	cli.SetIdempotency(cmd, cli.IdempotencyNo)
	// Restarting the instance from inside one of its own served calls
	// would take down the server answering it.
	setAnnotationTrue(cmd, cmdmeta.KeySelfHosting)
	_ = cli.SetOutputSchema(cmd, cli.OutputSchema{Type: &serviceStatusOutput{}, Version: "1.0"})
	cmd.Flags().Duration("wait", defaultRestartWait,
		"How long to wait for the drain and the new instance's readiness")
	return cmd
}

func runServiceRestart(cmd *cobra.Command, root *cli.Root) error {
	if cli.IsDryRun(cmd) {
		return cli.RenderPlan(cmd, cli.Plan{Effects: []cli.Effect{{
			Kind: "restart", Target: "instance:" + root.Config.Name, Reversible: true,
			Detail: "drain every running service, then re-execute in place",
		}}})
	}
	if !reexecSupported {
		e := output.UsageError(errReexecUnsupported.Error())
		e.SuggestedFix = "run `" + root.Config.Name + " " + ServiceCommandName + " stop`, then start it again"
		return e
	}
	wait, _ := cmd.Flags().GetDuration("wait")
	if wait <= 0 {
		wait = defaultRestartWait
	}
	deadline := time.Now().Add(wait)

	ep, err := controlEndpointFor(root, cmd)
	if err != nil {
		return err
	}
	conn, notRunning, err := dialControl(ep.path)
	if err != nil {
		return err
	}
	if notRunning {
		return notRunningError(root, ep)
	}
	dec, ack, err := controlCall(conn, "restart")
	if err != nil {
		_ = conn.Close()
		return err
	}

	// The old image holds the connection until it re-executes, which
	// closes it, or writes why it did not.
	_ = conn.SetReadDeadline(deadline)
	var final controlResponse
	decErr := dec.Decode(&final)
	_ = conn.Close()
	if decErr == nil && !final.OK {
		return output.GenericError(fmt.Sprintf("%s: %s", root.Config.Name, final.Error))
	}
	var ne net.Error
	if errors.As(decErr, &ne) && ne.Timeout() {
		return output.TransientError(fmt.Sprintf(
			"%s is still draining after %s", root.Config.Name, wait))
	}

	if err := awaitRestarted(root, ep.path, ack.PID, deadline, wait); err != nil {
		return err
	}
	// Readiness was polled without the tool's sections; the report
	// carries them, as `service status` does.
	out, notRunning, err := fetchStatus(ep.path, true)
	if err == nil && notRunning {
		err = fmt.Errorf("%s stopped answering right after it came up", root.Config.Name)
	}
	if err != nil {
		return output.TransientError(fmt.Sprintf("%s restarted, but its status could not be read: %v",
			root.Config.Name, err))
	}
	return renderServiceStatus(cmd, root, out)
}

// awaitRestarted polls the control socket at path until the new
// instance answers with every service settled: each required service
// ready or stopped, at least one service ready; an optional service
// that failed is not waited for, and is reported in the status restart
// prints, while one still starting is waited on until its readiness
// budget ends. It gives up when deadline passes; at once, exit 1, when
// a required service failed (the supervisor restarts nothing, so it
// will not come up) or nothing is left running (every service failed
// or stopped: the run is over); or as soon as pid, the re-executed
// process, is gone: a new run that refused to start (a bad
// configuration change) exits rather than answering.
func awaitRestarted(root *cli.Root, path string, pid int, deadline time.Time, wait time.Duration) error {
	name := root.Config.Name
	answered := false
	for {
		if got, ok := pollStatus(path); ok {
			answered = true
			settled, failed := restartSettled(got)
			if failed != nil {
				e := output.GenericError(fmt.Sprintf(
					"%s restarted, but its required service %s failed (%s): %s",
					name, failed.Service, failed.Reason, failed.Error))
				e.SuggestedFix = "read its stderr or service log for the cause; `" +
					name + " " + ServiceCommandName + " status` shows the instance"
				return e
			}
			if ended := restartEnded(got); ended != "" {
				e := output.GenericError(fmt.Sprintf(
					"%s drained and re-executed, but the new run has nothing left running (%s); the run is over",
					name, ended))
				e.SuggestedFix = "read its stderr or service log for the reason, then start it with `" +
					name + " " + ServiceCommandName + " start`"
				return e
			}
			if settled {
				return nil
			}
		} else if pid > 0 && !processAlive(pid) {
			e := output.GenericError(fmt.Sprintf(
				"%s (pid %d) drained and re-executed, but the new run exited without coming up", name, pid))
			e.SuggestedFix = "read its stderr or service log for the reason, then start it with `" +
				name + " " + ServiceCommandName + " start`"
			return e
		}
		if time.Now().After(deadline) {
			if !answered {
				return output.TransientError(fmt.Sprintf("%s did not come back within %s", name, wait))
			}
			return output.TransientError(fmt.Sprintf(
				"%s restarted but not every service settled within %s (required ready or stopped; optional still starting)",
				name, wait))
		}
		time.Sleep(restartPollInterval)
	}
}

func pollStatus(path string) ([]serviceStatusRow, bool) {
	out, notRunning, err := fetchStatus(path, false)
	if err != nil || notRunning {
		return nil, false
	}
	return out.Services, true
}

// restartSettled reads one status answer of the new run: settled when
// every row is ready, stopped (a clean return is no failure), or
// optional and failed, and at least one is ready — a run with nothing
// serving is down, not degraded; failed is the first required service
// the supervisor recorded failed, nil when none is. A row is ready
// when it says so and its state agrees: an answer without a state (an
// instance that predates it) is taken at its ready.
func restartSettled(rows []serviceStatusRow) (settled bool, failed *serviceStatusRow) {
	settled = true
	serving := false
	for i, r := range rows {
		switch {
		case r.State == string(serve.StateFailed) && !r.Optional:
			return false, &rows[i]
		case restartRowEnded(r):
		case !r.Ready || (r.State != "" && r.State != string(serve.StateReady)):
			settled = false
		default:
			serving = true
		}
	}
	return settled && serving, nil
}

// restartEnded describes the new run when every row of its answer has
// ended — failed or stopped — so nothing serves and the run is over;
// "" otherwise.
func restartEnded(rows []serviceStatusRow) string {
	if len(rows) == 0 {
		return ""
	}
	parts := make([]string, 0, len(rows))
	for _, r := range rows {
		if !restartRowEnded(r) {
			return ""
		}
		part := r.Service + " " + r.State
		if r.Reason != "" {
			part += " (" + r.Reason + ")"
		}
		parts = append(parts, part)
	}
	return strings.Join(parts, ", ")
}

// restartRowEnded reports whether r's service is over for this run:
// failed or stopped, neither of which the supervisor undoes.
func restartRowEnded(r serviceStatusRow) bool {
	return r.State == string(serve.StateFailed) || r.State == string(serve.StateStopped)
}
