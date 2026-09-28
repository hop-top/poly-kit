package mcpsdk

// SEP-2663 tasks support. The wire behavior — tasks/get, tasks/update,
// tasks/cancel, CreateTaskResult, capability negotiation, principal
// isolation — lives in the standalone extension module
// (hop.top/mcp-tasks); this file is kit's binding of that module to
// the bridge: which leaves are task-eligible, kit's safety gates
// enforced at creation, principal derivation, and detached execution
// of the creation's Admission (no second execution path).

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"maps"
	"net/http"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	taskext "hop.top/mcp-tasks"

	"hop.top/kit/go/transport/cmdsurface"
)

// confirmStateTTL bounds how long an issued MRTR confirmation
// exchange stays answerable.
const confirmStateTTL = 10 * time.Minute

// TasksConfig configures SEP-2663 task support for the surface.
type TasksConfig struct {
	// Tools names the task-eligible leaves by their dotted MCP tool
	// name (e.g. "widget.add"). Validated at mount: every name must
	// resolve to a bridge leaf. Task creation stays server-directed —
	// an eligible leaf called by a declaring client becomes a task;
	// every other call returns its result inline as before.
	Tools []string

	// TTL and PollInterval tune the advertised ttlMs and
	// pollIntervalMs. Zero applies the extension's defaults
	// (15m / 5s); a negative TTL means unlimited (ttlMs null); a
	// negative PollInterval omits the field.
	TTL          time.Duration
	PollInterval time.Duration

	// Store overrides the extension's in-memory task store. Leave nil
	// for single-instance deployments; supply a shared implementation
	// (or route tasks/* by the Mcp-Name header) behind load
	// balancers.
	Store taskext.Store
}

// WithTasks enables the io.modelcontextprotocol/tasks extension
// (SEP-2663) on the surface for the named leaves. See the README's
// tasks section for the contract, the safety posture, and the
// experimental status.
func WithTasks(cfg TasksConfig) Option {
	return func(c *config) { c.tasks = &cfg }
}

// taskBinding ties the tasks extension to the bridge.
type taskBinding struct {
	ext      *taskext.Extension
	eligible map[string]bool
	confirm  *confirmer
}

// newTaskBinding validates cfg against the bridge, declares the
// server capability on so (never mutating adopter-owned capability
// structs), and builds the extension with kit's principal derivation.
func newTaskBinding(b *cmdsurface.Bridge, cfg *TasksConfig, so *mcp.ServerOptions) (*taskBinding, error) {
	if cfg == nil {
		return nil, nil
	}
	if len(cfg.Tools) == 0 {
		return nil, errors.New("mcpsdk: WithTasks: no task-eligible tools named")
	}
	known := make(map[string]bool)
	for _, leaf := range b.Leaves() {
		known[toolName(leaf.Path)] = true
	}
	eligible := make(map[string]bool, len(cfg.Tools))
	for _, name := range cfg.Tools {
		if !known[name] {
			return nil, fmt.Errorf("mcpsdk: WithTasks: unknown tool %q", name)
		}
		eligible[name] = true
	}
	conf, err := newConfirmer(nil, confirmStateTTL)
	if err != nil {
		return nil, fmt.Errorf("mcpsdk: WithTasks: %w", err)
	}

	// Declare capabilities.extensions on a copy so an adopter-supplied
	// ServerOptions.Capabilities is never mutated in place.
	if so.Capabilities != nil {
		capsCopy := *so.Capabilities
		capsCopy.Extensions = maps.Clone(capsCopy.Extensions)
		so.Capabilities = &capsCopy
	}
	taskext.DeclareServerCapability(so)

	return &taskBinding{
		ext: taskext.New(&taskext.Options{
			Store:        cfg.Store,
			TTL:          cfg.TTL,
			PollInterval: cfg.PollInterval,
			Principal:    taskPrincipal,
		}),
		eligible: eligible,
		confirm:  conf,
	}, nil
}

// taskPrincipal derives the principal a task is bound to: the SHA-256
// of the Authorization header, hex-encoded, so the credential itself
// is never retained (the same derivation kit's confirm-gate tooling
// uses for header-bound identities). Requests without Authorization
// share the empty principal — meaningful isolation therefore requires
// authentication in front of the surface, which auth-required leaves
// already demand; unauthenticated deployments share tasks exactly as
// they share everything else.
func taskPrincipal(hdr http.Header) string {
	auth := hdr.Get("Authorization")
	if auth == "" {
		return ""
	}
	sum := sha256.Sum256([]byte(auth))
	return hex.EncodeToString(sum[:])
}

// invokeAsTask is the tools/call task path for an eligible leaf and a
// declaring client. Everything kit's safety contract demands is
// enforced HERE, at creation, before any task exists: the bridge's
// machine gates (Bridge.Admit), then confirmation — via X-Confirm-Token exactly like
// the synchronous path, or resolved synchronously through an MRTR
// elicitation exchange (SEP-2663 mandates MRTR-before-
// CreateTaskResult, which makes creation the natural gate). The
// detached execution runs the Admission creation obtained — the gates
// answer once per call, as on the synchronous path; nothing on the
// tasks surface itself (get/update/cancel) can execute, re-execute,
// or amplify a leaf.
//
// meta is the call's provenance as the synchronous path built it
// ([WithCallMeta], the auth gate's verdict); a call without a verified
// Caller is attributed to its task principal.
func (tb *taskBinding) invokeAsTask(ctx context.Context, b *cmdsurface.Bridge, leaf *cmdsurface.Leaf, req *mcp.CallToolRequest, hdr http.Header, meta cmdsurface.Meta) (*mcp.CallToolResult, error) {
	meta.Surface = cmdsurface.SurfaceMCP
	if meta.Caller == "" {
		meta.Caller = taskPrincipal(hdr)
	}
	// Arguments are checked first, as on the synchronous path: the
	// admission carries the invocation exactly as it will run.
	flags, args, err := decodeArguments(leaf, req.Params.Arguments)
	if err != nil {
		if errors.Is(err, errMalformedArguments) {
			return nil, err
		}
		return errorResult(err.Error()), nil
	}
	// Every machine gate before the person: a task the ceiling, the
	// permission gate or the rate limit refuses is neither created nor
	// confirmed. This is the call's only admission — the detached run
	// executes it, so a task spends one rate token and its outcome is
	// audited once.
	adm, err := b.Admit(ctx, cmdsurface.Invocation{
		Path:  append([]string(nil), leaf.Path...),
		Args:  args,
		Flags: flags,
		Meta:  meta,
	})
	if err != nil {
		if isUncallable(err) {
			return nil, err
		}
		return refusalResult(err), nil
	}
	if leaf.Class.RequiresConfirmation && hdr.Get("X-Confirm-Token") == "" {
		proceed, res := tb.confirmViaMRTR(req, leaf, hdr)
		if !proceed {
			return res, nil
		}
	}

	return tb.ext.StartTask(ctx, req, func(runCtx context.Context, _ *taskext.Handle) (*mcp.CallToolResult, error) {
		// Run arms the per-command deadline when the run starts.
		res, err := adm.Run(runCtx)
		if err != nil {
			return runErrorResult(err), nil // completed, isError
		}
		return renderResult(res), nil
	})
}

// confirmViaMRTR resolves kit's confirmation gate through a
// synchronous MRTR exchange (SEP-2322): the first pass returns an
// input_required result carrying one elicitation under an unguessable
// key plus an HMAC-signed requestState; the client's retry carries
// the response, verified against the state before any task is
// created. Declines and invalid or expired state fail closed. The
// MRTR-phase key namespace is independent of any task-phase
// inputRequests keys, per the SEP. proceed reports whether creation
// may continue; otherwise res is the reply to return.
func (tb *taskBinding) confirmViaMRTR(req *mcp.CallToolRequest, leaf *cmdsurface.Leaf, hdr http.Header) (proceed bool, res *mcp.CallToolResult) {
	p := req.Params
	if len(p.InputResponses) > 0 || p.RequestState != "" {
		key, st := tb.confirm.verify(p.RequestState, confirmBinding{
			leaf: leaf.PathKey(), principal: taskPrincipal(hdr),
		})
		if st != confirmValid {
			return false, errorResult("confirmation required")
		}
		er, ok := p.InputResponses[key].(*mcp.ElicitResult)
		if !ok {
			return false, errorResult("confirmation required")
		}
		if er.Action != "accept" {
			return false, errorResult("confirmation declined")
		}
		return true, nil
	}

	key, state := tb.confirm.mint(confirmBinding{leaf: leaf.PathKey(), principal: taskPrincipal(hdr)})
	return false, &mcp.CallToolResult{
		InputRequests: mcp.InputRequestMap{
			key: &mcp.ElicitParams{
				Message: fmt.Sprintf("Confirm running %q as a background task.", leaf.PathKey()),
			},
		},
		RequestState: state,
	}
}
