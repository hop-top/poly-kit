package mcpsdk

// SEP-2663 tasks support. The wire behavior — tasks/get, tasks/update,
// tasks/cancel, CreateTaskResult, capability negotiation, principal
// isolation — lives in the standalone extension module
// (mcpext.example/tasks); this file is kit's binding of that module to
// the bridge: which leaves are task-eligible, kit's safety gates
// enforced at creation, principal derivation, and detached execution
// through the Runner via Bridge.Invoke (no second execution path).

import (
	"context"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"maps"
	"net/http"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	taskext "mcpext.example/tasks"

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
	ext        *taskext.Extension
	eligible   map[string]bool
	confirmKey []byte
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
	key := make([]byte, 32)
	if _, err := rand.Read(key); err != nil {
		return nil, fmt.Errorf("mcpsdk: WithTasks: generating confirm key: %w", err)
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
		eligible:   eligible,
		confirmKey: key,
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
// enforced HERE, at creation, before any task exists: the destructive
// policy ceiling, and confirmation — via X-Confirm-Token exactly like
// the synchronous path, or resolved synchronously through an MRTR
// elicitation exchange (SEP-2663 mandates MRTR-before-
// CreateTaskResult, which makes creation the natural gate). The
// detached execution dispatches through Bridge.Invoke, which
// re-checks enablement and policy at run time; nothing on the tasks
// surface itself (get/update/cancel) can execute, re-execute, or
// amplify a leaf.
func (tb *taskBinding) invokeAsTask(ctx context.Context, b *cmdsurface.Bridge, leaf *cmdsurface.Leaf, req *mcp.CallToolRequest, hdr http.Header) (*mcp.CallToolResult, error) {
	if !b.Policy().Allowed(leaf.Class, cmdsurface.SurfaceMCP) {
		return errorResult(fmt.Sprintf("%v: %s on %s",
			cmdsurface.ErrDestructiveBlocked, leaf.PathKey(), cmdsurface.SurfaceMCP)), nil
	}
	if leaf.Class.RequiresConfirmation && hdr.Get("X-Confirm-Token") == "" {
		proceed, res := tb.confirmViaMRTR(req, leaf, hdr)
		if !proceed {
			return res, nil
		}
	}

	var flags map[string]any
	if len(req.Params.Arguments) > 0 {
		if err := json.Unmarshal(req.Params.Arguments, &flags); err != nil {
			return nil, fmt.Errorf("invalid arguments: %w", err)
		}
	}
	inv := cmdsurface.Invocation{
		Path:  append([]string(nil), leaf.Path...),
		Flags: flags,
		Meta: cmdsurface.Meta{
			Surface:     cmdsurface.SurfaceMCP,
			Caller:      taskPrincipal(hdr),
			RequestedAt: time.Now(),
		},
	}
	return tb.ext.StartTask(ctx, req, func(runCtx context.Context, _ *taskext.Handle) (*mcp.CallToolResult, error) {
		res, err := b.Invoke(runCtx, inv)
		if err != nil {
			if isUncallable(err) {
				return nil, err // protocol fault: the task fails
			}
			return errorResult(err.Error()), nil // completed, isError
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
		key, ok := tb.verifyConfirmState(p.RequestState, leaf.PathKey(), taskPrincipal(hdr))
		if !ok {
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

	key, state := tb.newConfirmState(leaf.PathKey(), taskPrincipal(hdr))
	return false, &mcp.CallToolResult{
		InputRequests: mcp.InputRequestMap{
			key: &mcp.ElicitParams{
				Message: fmt.Sprintf("Confirm running %q as a background task.", leaf.PathKey()),
			},
		},
		RequestState: state,
	}
}

// confirmClaim is the signed content of a confirmation requestState.
type confirmClaim struct {
	Nonce     string `json:"n"`
	Leaf      string `json:"l"`
	Principal string `json:"p"`
	Expiry    int64  `json:"e"`
}

// newConfirmState mints the MRTR key and its signed requestState.
func (tb *taskBinding) newConfirmState(leafKey, principal string) (key, state string) {
	var nb [8]byte
	if _, err := rand.Read(nb[:]); err != nil {
		panic(fmt.Sprintf("mcpsdk: crypto/rand unavailable: %v", err))
	}
	nonce := hex.EncodeToString(nb[:])
	claim, _ := json.Marshal(confirmClaim{
		Nonce:     nonce,
		Leaf:      leafKey,
		Principal: principal,
		Expiry:    time.Now().Add(confirmStateTTL).Unix(),
	})
	payload := base64.RawURLEncoding.EncodeToString(claim)
	return "confirm/" + nonce, payload + "." + tb.sign(payload)
}

// verifyConfirmState validates a retry's requestState and returns the
// MRTR key the confirmation response must appear under.
func (tb *taskBinding) verifyConfirmState(state, leafKey, principal string) (key string, ok bool) {
	payload, mac, found := cutLast(state)
	if !found || !hmac.Equal([]byte(mac), []byte(tb.sign(payload))) {
		return "", false
	}
	raw, err := base64.RawURLEncoding.DecodeString(payload)
	if err != nil {
		return "", false
	}
	var claim confirmClaim
	if json.Unmarshal(raw, &claim) != nil {
		return "", false
	}
	if claim.Leaf != leafKey || claim.Principal != principal || time.Now().Unix() > claim.Expiry {
		return "", false
	}
	return "confirm/" + claim.Nonce, true
}

// sign returns the hex HMAC-SHA256 of payload under the binding key.
func (tb *taskBinding) sign(payload string) string {
	m := hmac.New(sha256.New, tb.confirmKey)
	m.Write([]byte(payload))
	return hex.EncodeToString(m.Sum(nil))
}

// cutLast splits s at its final dot.
func cutLast(s string) (before, after string, found bool) {
	for i := len(s) - 1; i >= 0; i-- {
		if s[i] == '.' {
			return s[:i], s[i+1:], true
		}
	}
	return s, "", false
}
