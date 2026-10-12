package cli

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"net"
	"os"
	"sync"
	"syscall"
	"time"

	"github.com/spf13/cobra"

	"hop.top/kit/go/console/cli/cmdmeta"
	"hop.top/kit/go/console/output"
	"hop.top/kit/go/console/serve"
	"hop.top/kit/go/core/xdg"
	"hop.top/kit/go/transport/socket"
)

// The control channel.
//
// While `service start` runs it listens on a Unix socket at
// <runtime dir>/<tool>/control.sock, and `service stop`, `service
// restart` and `service status` are its only clients. The protocol is
// one JSON request line per connection and JSON response lines back:
//
//	{"op":"status"}  → {"ok":true,"pid":…,"services":[…]}
//	{"op":"stop"}    → {"ok":true,"stopping":true}
//	                   … the run drains …
//	                   {"ok":true,"stopped":true,"exit_code":0}
//	{"op":"restart"} → {"ok":true,"restarting":true,"pid":…}
//	                   … the run drains, the process re-executes …
//	                   EOF, or {"ok":false,"error":…} when it did not
//
// Only the daemon's own user may use it. The socket is created owner
// only (0600, in the runtime directory xdg creates 0700), and every
// connection's peer credentials are read from the kernel and refused
// unless the peer's uid is the daemon's. On a platform without peer
// credentials the file mode is the whole check.
//
// A stop request cancels the run's context, exactly as the first
// SIGINT/SIGTERM does: the supervisor drains every service in reverse
// start order within its budgets. Nothing is killed and the daemon
// sends itself no signal. A restart request drains the same way and
// then re-executes the binary in place (service_restart.go).

// controlSocketName is the control socket's file name under the tool's
// runtime directory.
const controlSocketName = "control.sock"

// controlDialTimeout bounds a client's connect and a request's read.
const controlDialTimeout = 2 * time.Second

// defaultStopWait bounds how long `service stop` waits for the drain:
// the supervisor's default shutdown budget plus slack.
const defaultStopWait = serve.DefaultShutdownTimeout + 30*time.Second

type controlRequest struct {
	Op string `json:"op"`
}

type controlResponse struct {
	OK    bool   `json:"ok"`
	Error string `json:"error,omitempty"`
	// Code classifies a refusal that is not an authorization failure
	// (CONFLICT, USAGE); empty is UNAUTHORIZED.
	Code       string             `json:"code,omitempty"`
	PID        int                `json:"pid,omitempty"`
	Services   []serviceStatusRow `json:"services,omitempty"`
	Stopping   bool               `json:"stopping,omitempty"`
	Stopped    bool               `json:"stopped,omitempty"`
	ExitCode   *int               `json:"exit_code,omitempty"`
	Restarting bool               `json:"restarting,omitempty"`
}

// serviceStatusRow is one row of `service status`: a service the
// running instance started, its readiness and where it listens.
type serviceStatusRow struct {
	Service string `json:"service" yaml:"service" table:"SERVICE"`
	Ready   bool   `json:"ready"   yaml:"ready"   table:"READY"`
	Addr    string `json:"addr"    yaml:"addr"    table:"ADDR"`
	PID     int    `json:"pid"     yaml:"pid"     table:"PID"`
}

// serviceStopRow is the single row `service stop` reports.
type serviceStopRow struct {
	// Result is "stopped" or "not_running".
	Result string `json:"result" yaml:"result" table:"RESULT"`
}

// controlSocketPath is where the tool's control socket lives.
func controlSocketPath(r *Root) (string, error) {
	tool := "kit"
	if r != nil && r.Config.Name != "" {
		tool = r.Config.Name
	}
	return xdg.RuntimeFile(tool, controlSocketName)
}

// controlIntent is what the control channel asked the run to end for.
type controlIntent int

const (
	intentNone controlIntent = iota
	intentStop
	intentRestart
)

// controlServer is the daemon's half of the control channel.
type controlServer struct {
	ln     net.Listener
	cancel context.CancelFunc
	status func() []serviceStatusRow

	mu      sync.Mutex
	waiters []net.Conn
	// restartWaiters are the restart clients, held open past close
	// until the process re-executes (which closes them) or reports
	// why it did not (finishRestart).
	restartWaiters []net.Conn
	intent         controlIntent
	closed         bool
	wg             sync.WaitGroup
}

// errControlInUse reports a control socket a live instance answers on.
var errControlInUse = errors.New("another instance is running")

// listenControl opens the control socket. cancel ends the run;
// status reports its services. A socket file nobody answers on is a
// stale one from an instance that did not exit cleanly, and is
// replaced; one a live instance answers on is left alone and reported
// as errControlInUse.
func listenControl(r *Root, cancel context.CancelFunc, status func() []serviceStatusRow) (*controlServer, error) {
	path, err := controlSocketPath(r)
	if err != nil {
		return nil, err
	}
	if len(path) > maxSocketPathLen {
		return nil, fmt.Errorf("control socket path %q is over the %d-byte limit", path, maxSocketPathLen)
	}
	if _, statErr := os.Lstat(path); statErr == nil {
		if conn, dialErr := net.DialTimeout("unix", path, controlDialTimeout); dialErr == nil {
			_ = conn.Close()
			return nil, fmt.Errorf("%w: %s", errControlInUse, path)
		}
		if rmErr := os.Remove(path); rmErr != nil {
			return nil, rmErr
		}
	}
	ln, err := net.Listen("unix", path)
	if err != nil {
		return nil, err
	}
	if err := os.Chmod(path, 0o600); err != nil {
		_ = ln.Close()
		return nil, err
	}
	cs := &controlServer{ln: ln, cancel: cancel, status: status}
	cs.wg.Add(1)
	go cs.serve()
	return cs, nil
}

func (cs *controlServer) serve() {
	defer cs.wg.Done()
	for {
		conn, err := cs.ln.Accept()
		if err != nil {
			return
		}
		cs.wg.Add(1)
		go func() {
			defer cs.wg.Done()
			cs.handle(conn)
		}()
	}
}

// handle answers one connection. A stop connection is held open until
// the run ends, so the client learns when the drain finished.
func (cs *controlServer) handle(conn net.Conn) {
	keep := false
	defer func() {
		if !keep {
			_ = conn.Close()
		}
	}()
	enc := json.NewEncoder(conn)
	if cred, err := socket.PeerCredentials(conn); err == nil && int(cred.UID) != os.Getuid() {
		_ = enc.Encode(controlResponse{Error: fmt.Sprintf(
			"peer uid %d is not this instance's uid %d", cred.UID, os.Getuid())})
		return
	}
	_ = conn.SetReadDeadline(time.Now().Add(controlDialTimeout))
	line, err := bufio.NewReader(conn).ReadBytes('\n')
	if err != nil && len(line) == 0 {
		return
	}
	_ = conn.SetReadDeadline(time.Time{})
	var req controlRequest
	if err := json.Unmarshal(line, &req); err != nil {
		_ = enc.Encode(controlResponse{Error: "malformed request: " + err.Error()})
		return
	}
	switch req.Op {
	case "status":
		_ = enc.Encode(controlResponse{OK: true, PID: os.Getpid(), Services: cs.status()})
	case "stop":
		cs.mu.Lock()
		if cs.closed {
			cs.mu.Unlock()
			_ = enc.Encode(controlResponse{OK: true, Stopping: true})
			return
		}
		// A stop outranks a restart already draining: the instance
		// exits instead of coming back.
		cs.intent = intentStop
		cs.waiters = append(cs.waiters, conn)
		keep = true
		cs.mu.Unlock()
		_ = enc.Encode(controlResponse{OK: true, Stopping: true})
		cs.cancel()
	case "restart":
		if !reexecSupported {
			_ = enc.Encode(controlResponse{Code: output.CodeUsage, Error: errReexecUnsupported.Error()})
			return
		}
		cs.mu.Lock()
		if cs.closed || cs.intent == intentStop {
			cs.mu.Unlock()
			_ = enc.Encode(controlResponse{Code: output.CodeConflict, Error: "the instance is already stopping"})
			return
		}
		cs.intent = intentRestart
		cs.restartWaiters = append(cs.restartWaiters, conn)
		keep = true
		cs.mu.Unlock()
		_ = enc.Encode(controlResponse{OK: true, Restarting: true, PID: os.Getpid()})
		cs.cancel()
	default:
		_ = enc.Encode(controlResponse{Error: fmt.Sprintf("unknown op %q", req.Op)})
	}
}

// close stops accepting, unlinks the socket and tells every waiting
// stop client the run's exit code. It reports whether the run ended
// for a restart; the restart clients stay connected for finishRestart.
func (cs *controlServer) close(exitCode int) (restart bool) {
	cs.mu.Lock()
	cs.closed = true
	waiters := cs.waiters
	cs.waiters = nil
	restart = cs.intent == intentRestart
	cs.mu.Unlock()
	_ = cs.ln.Close()
	for _, conn := range waiters {
		code := exitCode
		_ = json.NewEncoder(conn).Encode(controlResponse{OK: true, Stopped: true, ExitCode: &code})
		_ = conn.Close()
	}
	cs.wg.Wait()
	if !restart {
		cs.finishRestart("a stop was requested during the drain")
	}
	return restart
}

// finishRestart releases the restart clients: with a reason when the
// process is not re-executing, without one when it is about to.
func (cs *controlServer) finishRestart(reason string) {
	cs.mu.Lock()
	waiters := cs.restartWaiters
	cs.restartWaiters = nil
	cs.mu.Unlock()
	for _, conn := range waiters {
		if reason != "" {
			_ = json.NewEncoder(conn).Encode(controlResponse{Error: "not restarted: " + reason})
		}
		_ = conn.Close()
	}
}

// serviceStatusRows reports the services a run selected, as the
// control channel's status answer.
func serviceStatusRows(reg *serve.Registry, selected []string) func() []serviceStatusRow {
	return func() []serviceStatusRow {
		rows := make([]serviceStatusRow, 0, len(selected))
		for _, name := range selected {
			row := serviceStatusRow{Service: name, PID: os.Getpid()}
			if svc, ok := reg.Lookup(name); ok {
				row.Ready = svc.Ready()
				if a, ok := svc.(serve.Addressed); ok {
					row.Addr = a.Addr()
				}
			}
			rows = append(rows, row)
		}
		return rows
	}
}

// dialControl connects to a running instance. notRunning is true when
// there is none: no socket file, or one nobody answers on.
func dialControl(r *Root) (conn net.Conn, notRunning bool, err error) {
	path, err := controlSocketPath(r)
	if err != nil {
		return nil, false, err
	}
	conn, err = net.DialTimeout("unix", path, controlDialTimeout)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) || errors.Is(err, syscall.ENOENT) ||
			errors.Is(err, syscall.ECONNREFUSED) {
			return nil, true, nil
		}
		return nil, false, err
	}
	return conn, false, nil
}

// controlCall sends op and decodes the first response line, leaving
// conn open for any that follow.
func controlCall(conn net.Conn, op string) (*json.Decoder, controlResponse, error) {
	var resp controlResponse
	if err := json.NewEncoder(conn).Encode(controlRequest{Op: op}); err != nil {
		return nil, resp, err
	}
	_ = conn.SetReadDeadline(time.Now().Add(controlDialTimeout))
	dec := json.NewDecoder(conn)
	if err := dec.Decode(&resp); err != nil {
		return nil, resp, fmt.Errorf("control socket: %w", err)
	}
	if !resp.OK {
		msg := "control socket refused the request: " + resp.Error
		switch resp.Code {
		case output.CodeConflict:
			return nil, resp, output.ConflictError(msg)
		case output.CodeUsage:
			return nil, resp, output.UsageError(msg)
		}
		return nil, resp, output.UnauthorizedError(msg)
	}
	return dec, resp, nil
}

// notRunningError is `service status`'s answer when no instance runs:
// NOT_FOUND, exit 3 — the thing asked about, a running instance, could
// not be located. Not a usage error (the invocation was right) and not
// a failure to ask (that is exit 1 or 6).
func notRunningError(r *Root) *output.Error {
	e := output.NotFoundError(r.Config.Name + " is not running")
	e.SuggestedFix = "start it with `" + r.Config.Name + " " + ServiceCommandName + " start`"
	return e
}

// serviceStatusCmd builds `service status`.
func serviceStatusCmd(root *Root) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "status",
		Short: "Show the running instance's services, readiness and addresses",
		Long: "Ask the running instance, over its control socket, which services it\n" +
			"started, whether each is ready, and where each listens.\n\n" +
			"Exits 3 (NOT_FOUND) when no instance is running.",
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			return runServiceStatus(cmd, root)
		},
	}
	SetSideEffect(cmd, SideEffectRead)
	SetIdempotency(cmd, IdempotencyYes)
	_ = SetOutputSchema(cmd, OutputSchema{Type: &[]serviceStatusRow{}, Version: "1.0"})
	return cmd
}

func runServiceStatus(cmd *cobra.Command, root *Root) error {
	conn, notRunning, err := dialControl(root)
	if err != nil {
		return err
	}
	if notRunning {
		return notRunningError(root)
	}
	defer func() { _ = conn.Close() }()
	_, resp, err := controlCall(conn, "status")
	if err != nil {
		return err
	}
	rows := resp.Services
	if rows == nil {
		rows = []serviceStatusRow{}
	}
	return output.Dispatch(cmd, root.Viper, rows)
}

// serviceStopCmd builds `service stop`.
func serviceStopCmd(root *Root) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "stop",
		Short: "Ask the running instance to drain and exit",
		Long: "Ask the running instance, over its control socket, to shut down the\n" +
			"way the first SIGINT or SIGTERM does: every service is stopped in\n" +
			"reverse start order within its budgets. Waits for the drain to finish.\n\n" +
			"Exits 0 with result not_running when no instance is running.",
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			return runServiceStop(cmd, root)
		},
	}
	SetSideEffect(cmd, SideEffectWriteShared)
	SetIdempotency(cmd, IdempotencyYes)
	// Stopping the instance from inside one of its own served calls
	// would take down the server answering it.
	setAnnotationTrue(cmd, cmdmeta.KeySelfHosting)
	_ = SetOutputSchema(cmd, OutputSchema{Type: &serviceStopRow{}, Version: "1.0"})
	cmd.Flags().Duration("wait", defaultStopWait, "How long to wait for the drain to finish")
	return cmd
}

func runServiceStop(cmd *cobra.Command, root *Root) error {
	if IsDryRun(cmd) {
		return RenderPlan(cmd, Plan{Effects: []Effect{{
			Kind: "stop", Target: "instance:" + root.Config.Name, Reversible: true,
			Detail: "drain every running service and exit",
		}}})
	}
	conn, notRunning, err := dialControl(root)
	if err != nil {
		return err
	}
	if notRunning {
		return output.Dispatch(cmd, root.Viper, serviceStopRow{Result: "not_running"})
	}
	defer func() { _ = conn.Close() }()
	dec, _, err := controlCall(conn, "stop")
	if err != nil {
		return err
	}
	wait, _ := cmd.Flags().GetDuration("wait")
	if wait > 0 {
		_ = conn.SetReadDeadline(time.Now().Add(wait))
	} else {
		_ = conn.SetReadDeadline(time.Time{})
	}
	var done controlResponse
	if err := dec.Decode(&done); err != nil {
		var ne net.Error
		if errors.As(err, &ne) && ne.Timeout() {
			return output.TransientError(fmt.Sprintf(
				"%s is still draining after %s", root.Config.Name, wait))
		}
		// The instance closed the connection without a final word:
		// it is gone, which is what was asked.
	}
	return output.Dispatch(cmd, root.Viper, serviceStopRow{Result: "stopped"})
}
