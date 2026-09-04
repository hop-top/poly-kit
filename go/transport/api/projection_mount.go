package api

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strconv"
	"strings"
)

// CommandExecutor runs one projected command. The projection decodes
// the request into flags and args, hands them here, and renders what
// comes back.
//
// The implementation is expected to be the cmdsurface bridge, which
// owns the policy gate, the runner, and the safety classification.
// The projection deliberately holds no policy of its own: an HTTP
// layer that re-derived "may this run" would be a second opinion able
// to disagree with the one the CLI and every other surface enforce.
type CommandExecutor interface {
	// Execute invokes the command at path with the decoded flags
	// and positional args.
	//
	// A refusal MUST be reported as an error, not as a non-zero
	// ExitResult: the two mean different things to a caller, and
	// only the error path carries a reason.
	Execute(ctx context.Context, req CommandRequest) (CommandResult, error)
}

// CommandRequest is one decoded projection call.
type CommandRequest struct {
	// Path is the command path below the root.
	Path []string
	// Flags are the decoded flag values keyed by long name.
	Flags map[string]any
	// Args are the positional arguments in order.
	Args []string
	// ConfirmToken is the value of the confirmation header, empty
	// when absent.
	ConfirmToken string
}

// CommandResult is the outcome of one projected call.
type CommandResult struct {
	// ExitCode is the command's process-style exit status.
	ExitCode int `json:"exit_code"`
	// Data is the command's structured output. When non-nil it IS
	// the response body's data field.
	Data any `json:"data,omitempty"`
	// Stdout is the captured standard output, carried so a command
	// with no declared output schema still says something.
	Stdout string `json:"stdout,omitempty"`
	// Stderr is the captured standard error.
	Stderr string `json:"stderr,omitempty"`
}

// Projection errors an executor may return. The projection maps each
// to a status; anything else becomes 500.
var (
	// ErrCommandNotInvocable reports that the addressed command is
	// not mounted on this surface.
	ErrCommandNotInvocable = errors.New("api: command not invocable on this surface")
	// ErrConfirmationRequired reports that the command needs a
	// confirmation token the caller did not supply.
	ErrConfirmationRequired = errors.New("api: confirmation required")
	// ErrDestructiveBlocked reports that policy refuses this
	// command on this surface.
	ErrDestructiveBlocked = errors.New("api: destructive command blocked on this surface")
)

// ConfirmTokenHeader carries the confirmation token for commands that
// require one. It matches the header the existing cmdsurface REST
// mount reads, so a caller that already confirms against one surface
// does not learn a second spelling.
const ConfirmTokenHeader = "X-Confirm-Token"

// ProjectionConfig configures MountCommandProjection.
type ProjectionConfig struct {
	// Descriptors is every command in the tree, invocable or not.
	// Non-invocable entries are listed by discovery and not
	// mounted.
	Descriptors []CommandDescriptor
	// Executor runs invocable commands. A nil Executor mounts
	// discovery only, which is what a tool with no bridge wired
	// should still serve.
	Executor CommandExecutor
	// ToolName labels the discovery document.
	ToolName string
	// ToolVersion labels the discovery document.
	ToolVersion string
}

// MountCommandProjection registers the projected command routes and
// the discovery endpoint on r.
//
// Every invocable descriptor gets exactly one route, at the method
// its side-effect class selects. Non-invocable descriptors are NOT
// mounted; they appear in the discovery listing with invocable=false
// and their stable reason, so the projection describes the whole tree
// even though it serves part of it.
//
// Routes are registered through r.Handle, so the router's configured
// middleware — auth included — wraps them exactly as it wraps an
// adopter's own routes. The projection installs no auth of its own.
func MountCommandProjection(r *Router, cfg ProjectionConfig) {
	if r == nil {
		return
	}

	r.Handle(http.MethodGet, CommandProjectionPrefix,
		discoveryHandler(cfg))

	if cfg.Executor == nil {
		return
	}
	for _, d := range cfg.Descriptors {
		if !d.Invocable {
			continue
		}
		r.Handle(d.Method(), d.Route(), commandHandler(cfg.Executor, d))
	}
}

// commandHandler returns the http.HandlerFunc for one projected
// command.
func commandHandler(ex CommandExecutor, d CommandDescriptor) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		req, err := decodeCommandRequest(r, d)
		if err != nil {
			Error(w, http.StatusBadRequest, &APIError{
				Status:  http.StatusBadRequest,
				Code:    "bad_request",
				Message: err.Error(),
			})
			return
		}

		res, err := ex.Execute(r.Context(), req)
		if err != nil {
			writeProjectionError(w, d, err)
			return
		}
		JSON(w, StatusForExitCode(res.ExitCode), res)
	}
}

// writeProjectionError maps an executor refusal onto a status. The
// descriptor's reason travels in the message so a caller learns why
// rather than only that it failed.
func writeProjectionError(w http.ResponseWriter, d CommandDescriptor, err error) {
	switch {
	case errors.Is(err, ErrCommandNotInvocable):
		msg := "command is not invocable on this surface"
		if d.Reason != "" {
			msg = msg + ": " + d.Reason
		}
		Error(w, StatusNotInvocable, &APIError{
			Status:  StatusNotInvocable,
			Code:    CodeNotInvocable,
			Message: msg,
		})
	case errors.Is(err, ErrConfirmationRequired):
		Error(w, http.StatusPreconditionRequired, &APIError{
			Status:  http.StatusPreconditionRequired,
			Code:    CodeConfirmationRequired,
			Message: ConfirmTokenHeader + " header required for this command",
		})
	case errors.Is(err, ErrDestructiveBlocked):
		Error(w, http.StatusForbidden, &APIError{
			Status:  http.StatusForbidden,
			Code:    CodeDestructiveBlocked,
			Message: err.Error(),
		})
	default:
		ae := MapError(err)
		Error(w, ae.Status, ae)
	}
}

// decodeCommandRequest builds the executor request from the HTTP
// request.
//
// Where the parameters live follows from the method, which follows
// from the side-effect class:
//
//   - GET (read): flags come from the query string, positional args
//     from the repeated "arg" query parameter. A read must stay
//     cacheable and safe to replay, so it carries no body.
//   - POST (everything else): flags and args come from a JSON object
//     body ({"flags":{…},"args":[…]}). Query parameters are still
//     honored for flags so a caller can mix the two, with the body
//     winning on conflict — the body is the more explicit statement.
func decodeCommandRequest(r *http.Request, d CommandDescriptor) (CommandRequest, error) {
	req := CommandRequest{
		Path:         append([]string(nil), d.Path...),
		Flags:        map[string]any{},
		ConfirmToken: r.Header.Get(ConfirmTokenHeader),
	}

	byName := make(map[string]CommandFlag, len(d.Flags))
	for _, f := range d.Flags {
		byName[f.Name] = f
	}

	q := r.URL.Query()
	for name, vals := range q {
		if name == "arg" {
			continue
		}
		f, ok := byName[name]
		if !ok || len(vals) == 0 {
			continue
		}
		v, err := coerceFlag(f, vals[len(vals)-1])
		if err != nil {
			return req, err
		}
		req.Flags[name] = v
	}
	req.Args = append(req.Args, q["arg"]...)

	if r.Method == http.MethodGet || r.Body == nil || r.ContentLength == 0 {
		return req, requireArgs(d, req.Args)
	}

	var body struct {
		Flags map[string]any `json:"flags"`
		Args  []string       `json:"args"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		return req, err
	}
	for name, v := range body.Flags {
		if _, ok := byName[name]; !ok {
			// An undeclared flag is a caller error, not something
			// to forward: the command would reject it anyway, and
			// failing here names the offending flag.
			return req, errors.New("unknown flag: " + name)
		}
		req.Flags[name] = v
	}
	if len(body.Args) > 0 {
		req.Args = body.Args
	}
	return req, requireArgs(d, req.Args)
}

// requireArgs checks that every declared-required positional argument
// was supplied. Cobra would catch this too, but catching it here
// turns a command's usage error into a 400 that names the argument.
func requireArgs(d CommandDescriptor, args []string) error {
	need := 0
	for _, a := range d.Args {
		if a.Required {
			need++
		}
	}
	if len(args) < need {
		return errors.New("missing required argument: " + d.Args[len(args)].Name)
	}
	return nil
}

// coerceFlag converts a query-string value to the flag's declared
// type. Query strings are untyped, and handing cobra the string
// "true" for a bool flag works only by accident of pflag's parser;
// converting here keeps the executor's Flags map honestly typed.
func coerceFlag(f CommandFlag, raw string) (any, error) {
	switch f.Type {
	case "bool":
		// A bare "?--force" with no value means true, matching the
		// CLI spelling of the same flag.
		if raw == "" {
			return true, nil
		}
		b, err := strconv.ParseBool(raw)
		if err != nil {
			return nil, errors.New(f.Name + ": invalid boolean: " + raw)
		}
		return b, nil
	case "int", "int8", "int16", "int32", "int64",
		"uint", "uint8", "uint16", "uint32", "uint64", "count":
		n, err := strconv.ParseInt(raw, 10, 64)
		if err != nil {
			return nil, errors.New(f.Name + ": invalid integer: " + raw)
		}
		return n, nil
	case "float32", "float64":
		fl, err := strconv.ParseFloat(raw, 64)
		if err != nil {
			return nil, errors.New(f.Name + ": invalid number: " + raw)
		}
		return fl, nil
	case "stringSlice", "stringArray":
		return strings.Split(raw, ","), nil
	}
	return raw, nil
}
