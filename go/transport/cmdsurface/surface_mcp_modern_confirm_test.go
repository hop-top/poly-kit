package cmdsurface

// Coverage for the MRTR elicitation confirmation flow
// (surface_mcp_modern_confirm.go): the full input_required → retry →
// execution round-trip, tamper rejection with audit, expiry
// re-prompt, argument / principal binding, capability gating with the
// X-Confirm-Token fallback, the never-cacheable rule for interim
// results, cross-instance state verification under a shared key, the
// destructive-ceiling independence, and the WithMCPConfirmationKey
// mount-time contract.
//
// This file defines its own fixture tree (confirmTestTree) per
// project convention — it does not reuse modernTestTree or the legacy
// lock trees.

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/spf13/cobra"

	"hop.top/kit/go/transport/api"
)

// mcpConfirmTestKey is the shared HMAC key test mounts use.
var mcpConfirmTestKey = []byte("0123456789abcdef0123456789abcdef")

// confirmTestTree builds the fixture tree for confirmation-flow
// tests, returning the root and a counter incremented by every leaf
// execution (so tests can assert a gated call never ran):
//
//	root
//	├── echo         (read)
//	├── purge        (requires-confirmation, write; flag: target str)
//	└── vault
//	    └── burn     (destructive + requires-confirmation)
func confirmTestTree() (*cobra.Command, *atomic.Int64) {
	execs := &atomic.Int64{}
	root := &cobra.Command{Use: "root"}

	echo := &cobra.Command{
		Use:   "echo",
		Short: "Echo back",
		RunE: func(cmd *cobra.Command, _ []string) error {
			execs.Add(1)
			cmd.Println("echoed")
			return nil
		},
		Annotations: map[string]string{"kit/side-effect": "read"},
	}
	purge := &cobra.Command{
		Use:   "purge",
		Short: "Purge a target",
		RunE: func(cmd *cobra.Command, _ []string) error {
			execs.Add(1)
			target, _ := cmd.Flags().GetString("target")
			cmd.Printf("purged %s\n", target)
			return nil
		},
		Annotations: map[string]string{
			"kit/side-effect":           "write",
			"kit/requires-confirmation": "true",
		},
	}
	purge.Flags().String("target", "", "what to purge")

	vault := &cobra.Command{Use: "vault"}
	burn := &cobra.Command{
		Use:   "burn",
		Short: "Burn the vault",
		RunE: func(cmd *cobra.Command, _ []string) error {
			execs.Add(1)
			cmd.Println("burned")
			return nil
		},
		Annotations: map[string]string{
			"kit/side-effect":           "destructive",
			"kit/requires-confirmation": "true",
		},
	}
	vault.AddCommand(burn)

	root.AddCommand(echo, purge, vault)
	return root, execs
}

// confirmServer mounts a fresh confirmTestTree bridge with the given
// options and returns the server, the execution counter, and the
// bridge (for sink registration).
func confirmServer(t *testing.T, opts ...MCPOption) (*httptest.Server, *atomic.Int64, *Bridge) {
	t.Helper()
	root, execs := confirmTestTree()
	b := New(root)
	return modernServerFor(t, b, opts...), execs, b
}

// elicitMeta returns a complete reserved _meta declaring form-mode
// elicitation support (the empty object form: {} ≡ {"form": {}}).
func elicitMeta() map[string]any {
	return map[string]any{
		metaKeyProtocolVersion:    mcpModernProtocolVersion,
		metaKeyClientCapabilities: map[string]any{"elicitation": map[string]any{}},
	}
}

// confirmBody renders a modern tools/call body with an explicit id
// (MRTR retries must use a fresh id) and optional MRTR retry members.
func confirmBody(t *testing.T, id int, name string, args map[string]any, meta map[string]any, state, action string) string {
	t.Helper()
	params := map[string]any{"name": name}
	if args != nil {
		params["arguments"] = args
	}
	if state != "" {
		params["requestState"] = state
	}
	if action != "" {
		params["inputResponses"] = map[string]any{
			mcpConfirmInputRequestKey: map[string]any{"action": action},
		}
	}
	if meta != nil {
		params["_meta"] = meta
	}
	enc, err := json.Marshal(map[string]any{
		"jsonrpc": "2.0", "id": id, "method": "tools/call", "params": params,
	})
	if err != nil {
		t.Fatalf("marshal body: %v", err)
	}
	return string(enc)
}

// inputRequiredResult asserts the decoded response is an
// input_required result and returns it plus its requestState.
func inputRequiredResult(t *testing.T, m map[string]any) (map[string]any, string) {
	t.Helper()
	res, ok := m["result"].(map[string]any)
	if !ok {
		t.Fatalf("no result object: %v", m)
	}
	if res["resultType"] != "input_required" {
		t.Fatalf("resultType=%v want=input_required: %v", res["resultType"], res)
	}
	state, _ := res["requestState"].(string)
	if state == "" {
		t.Fatalf("missing requestState: %v", res)
	}
	return res, state
}

// recordingSink captures every emission for audit assertions.
type recordingSink struct {
	mu     sync.Mutex
	events []recordedEmit
}

type recordedEmit struct {
	inv Invocation
	err error
}

func (s *recordingSink) Emit(_ context.Context, inv Invocation, _ Result, err error) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.events = append(s.events, recordedEmit{inv: inv, err: err})
	return nil
}

func (s *recordingSink) snapshot() []recordedEmit {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]recordedEmit(nil), s.events...)
}

// bindingFor recomputes the state binding the server derives for a
// purge call with the given arguments and no Authorization header,
// using the same helpers the gate uses.
func bindingFor(t *testing.T, tool string, args map[string]any) mcpConfirmBinding {
	t.Helper()
	raw, err := json.Marshal(map[string]any{"arguments": args})
	if err != nil {
		t.Fatalf("marshal args: %v", err)
	}
	return mcpConfirmBinding{
		tool:       strings.ReplaceAll(tool, ".", " "),
		argsDigest: mcpConfirmArgsDigest(raw),
		principal:  "",
	}
}

func TestModernConfirm_FullRoundTrip(t *testing.T) {
	srv, execs, _ := confirmServer(t, WithMCPConfirmationKey(mcpConfirmTestKey))
	args := map[string]any{"target": "data"}

	// Round 1: no confirmation yet → input_required prompt.
	status, m := postJSON(t, srv, "/mcp", modernHeaders("tools/call", "purge"),
		confirmBody(t, 1, "purge", args, elicitMeta(), "", ""))
	if status != http.StatusOK {
		t.Fatalf("round1 status=%d want=200: %v", status, m)
	}
	res, state := inputRequiredResult(t, m)
	if execs.Load() != 0 {
		t.Fatalf("leaf executed before confirmation")
	}
	// The prompt is a single elicitation/create form request under
	// the reserved "confirm" key.
	reqs, ok := res["inputRequests"].(map[string]any)
	if !ok || len(reqs) != 1 {
		t.Fatalf("inputRequests=%v want single confirm entry", res["inputRequests"])
	}
	confirm, ok := reqs[mcpConfirmInputRequestKey].(map[string]any)
	if !ok {
		t.Fatalf("no %q inputRequest: %v", mcpConfirmInputRequestKey, reqs)
	}
	if confirm["method"] != "elicitation/create" {
		t.Errorf("inputRequest method=%v want=elicitation/create", confirm["method"])
	}
	cp, _ := confirm["params"].(map[string]any)
	if cp["mode"] != "form" {
		t.Errorf("elicit mode=%v want=form", cp["mode"])
	}
	if msg, _ := cp["message"].(string); !strings.Contains(msg, "purge") {
		t.Errorf("elicit message %q does not name the tool", msg)
	}
	if _, ok := cp["requestedSchema"].(map[string]any); !ok {
		t.Errorf("form elicitation missing requestedSchema: %v", cp)
	}
	// Modern result envelope members still stamped.
	meta, _ := res["_meta"].(map[string]any)
	if _, ok := meta[metaKeyServerInfo].(map[string]any); !ok {
		t.Errorf("_meta serverInfo missing on input_required: %v", res)
	}
	// Interim results carry no execution members.
	if _, ok := res["content"]; ok {
		t.Errorf("input_required must not carry content: %v", res)
	}
	if _, ok := res["isError"]; ok {
		t.Errorf("input_required must not carry isError: %v", res)
	}

	// Round 2: retry (fresh id) echoing the state, user accepted.
	status, m = postJSON(t, srv, "/mcp", modernHeaders("tools/call", "purge"),
		confirmBody(t, 2, "purge", args, elicitMeta(), state, "accept"))
	if status != http.StatusOK {
		t.Fatalf("round2 status=%d want=200: %v", status, m)
	}
	res, _ = m["result"].(map[string]any)
	if res["resultType"] != "complete" {
		t.Fatalf("retry resultType=%v want=complete: %v", res["resultType"], res)
	}
	if res["isError"] != false {
		t.Errorf("retry isError=%v want=false", res["isError"])
	}
	content, _ := res["content"].([]any)
	if len(content) == 0 {
		t.Fatalf("no content blocks: %v", res)
	}
	if text := content[0].(map[string]any)["text"]; text != "purged data\n" {
		t.Errorf("content[0].text=%q want=%q", text, "purged data\n")
	}
	if execs.Load() != 1 {
		t.Errorf("executions=%d want=1", execs.Load())
	}
}

func TestModernConfirm_DeclineAndCancelRefuse(t *testing.T) {
	for _, action := range []string{"decline", "cancel"} {
		t.Run(action, func(t *testing.T) {
			srv, execs, _ := confirmServer(t, WithMCPConfirmationKey(mcpConfirmTestKey))
			_, m := postJSON(t, srv, "/mcp", modernHeaders("tools/call", "purge"),
				confirmBody(t, 1, "purge", nil, elicitMeta(), "", ""))
			_, state := inputRequiredResult(t, m)

			status, m := postJSON(t, srv, "/mcp", modernHeaders("tools/call", "purge"),
				confirmBody(t, 2, "purge", nil, elicitMeta(), state, action))
			if status != http.StatusOK {
				t.Fatalf("status=%d want=200: %v", status, m)
			}
			res, _ := m["result"].(map[string]any)
			if res["resultType"] != "complete" {
				t.Errorf("resultType=%v want=complete", res["resultType"])
			}
			if res["isError"] != true {
				t.Errorf("isError=%v want=true", res["isError"])
			}
			content, _ := res["content"].([]any)
			if text := content[0].(map[string]any)["text"]; text != "confirmation declined" {
				t.Errorf("text=%q want=%q", text, "confirmation declined")
			}
			if execs.Load() != 0 {
				t.Errorf("declined call executed")
			}
		})
	}
}

func TestModernConfirm_TamperedStateAuditedAndReprompted(t *testing.T) {
	srv, execs, b := confirmServer(t, WithMCPConfirmationKey(mcpConfirmTestKey))
	sink := &recordingSink{}
	b.appendSink(SinkSpec{Sink: sink, OnError: true})

	_, m := postJSON(t, srv, "/mcp", modernHeaders("tools/call", "purge"),
		confirmBody(t, 1, "purge", nil, elicitMeta(), "", ""))
	_, state := inputRequiredResult(t, m)

	// Flip the final MAC character.
	last := state[len(state)-1]
	flip := byte('A')
	if last == 'A' {
		flip = 'B'
	}
	tampered := state[:len(state)-1] + string(flip)

	status, m := postJSON(t, srv, "/mcp", modernHeaders("tools/call", "purge"),
		confirmBody(t, 2, "purge", nil, elicitMeta(), tampered, "accept"))
	if status != http.StatusOK {
		t.Fatalf("status=%d want=200: %v", status, m)
	}
	// Never honored: not executed, answered with a fresh prompt whose
	// state is newly minted (not the tampered value).
	res, fresh := inputRequiredResult(t, m)
	if fresh == tampered {
		t.Errorf("re-prompt echoed the tampered state")
	}
	if _, ok := res["inputRequests"]; !ok {
		t.Errorf("re-prompt missing inputRequests: %v", res)
	}
	if execs.Load() != 0 {
		t.Fatalf("tampered confirmation executed the leaf")
	}
	// Audited as a security-relevant event.
	events := sink.snapshot()
	if len(events) != 1 {
		t.Fatalf("audit events=%d want=1: %+v", len(events), events)
	}
	if events[0].err != errMCPConfirmStateRejected {
		t.Errorf("audit err=%v want=%v", events[0].err, errMCPConfirmStateRejected)
	}
	if got := events[0].inv.Meta.Extra["mcp_confirm_rejection"]; got != "request_state_verification_failed" {
		t.Errorf("audit extra=%q", got)
	}
	if events[0].inv.Meta.Surface != SurfaceMCP || strings.Join(events[0].inv.Path, " ") != "purge" {
		t.Errorf("audit inv=%+v", events[0].inv)
	}

	// The fresh state from the re-prompt is honorable: accept runs.
	status, m = postJSON(t, srv, "/mcp", modernHeaders("tools/call", "purge"),
		confirmBody(t, 3, "purge", nil, elicitMeta(), fresh, "accept"))
	if status != http.StatusOK {
		t.Fatalf("status=%d want=200: %v", status, m)
	}
	if res, _ := m["result"].(map[string]any); res["resultType"] != "complete" {
		t.Fatalf("post-reprompt resultType=%v want=complete", res["resultType"])
	}
	if execs.Load() != 1 {
		t.Errorf("executions=%d want=1", execs.Load())
	}
}

func TestModernConfirm_ExpiredStateRepromptsWithoutAudit(t *testing.T) {
	srv, execs, b := confirmServer(t, WithMCPConfirmationKey(mcpConfirmTestKey))
	sink := &recordingSink{}
	b.appendSink(SinkSpec{Sink: sink, OnError: true})

	args := map[string]any{"target": "data"}
	expired := mintMCPConfirmState(mcpConfirmTestKey, bindingFor(t, "purge", args),
		time.Now().Add(-time.Minute))

	status, m := postJSON(t, srv, "/mcp", modernHeaders("tools/call", "purge"),
		confirmBody(t, 1, "purge", args, elicitMeta(), expired, "accept"))
	if status != http.StatusOK {
		t.Fatalf("status=%d want=200: %v", status, m)
	}
	_, fresh := inputRequiredResult(t, m)
	if fresh == expired {
		t.Errorf("re-prompt echoed the expired state")
	}
	if execs.Load() != 0 {
		t.Fatalf("expired confirmation executed the leaf")
	}
	// Routine re-prompt: no security audit event.
	if events := sink.snapshot(); len(events) != 0 {
		t.Errorf("expiry produced audit events: %+v", events)
	}

	// The re-minted state completes the flow.
	status, m = postJSON(t, srv, "/mcp", modernHeaders("tools/call", "purge"),
		confirmBody(t, 2, "purge", args, elicitMeta(), fresh, "accept"))
	if status != http.StatusOK {
		t.Fatalf("status=%d want=200: %v", status, m)
	}
	if execs.Load() != 1 {
		t.Errorf("executions=%d want=1", execs.Load())
	}
}

func TestModernConfirm_ArgumentBinding(t *testing.T) {
	// A state minted for one argument set must not confirm another:
	// the retry's mutated arguments change the binding, so the MAC
	// fails and the presentation is audited.
	srv, execs, b := confirmServer(t, WithMCPConfirmationKey(mcpConfirmTestKey))
	sink := &recordingSink{}
	b.appendSink(SinkSpec{Sink: sink, OnError: true})

	_, m := postJSON(t, srv, "/mcp", modernHeaders("tools/call", "purge"),
		confirmBody(t, 1, "purge", map[string]any{"target": "cache"}, elicitMeta(), "", ""))
	_, state := inputRequiredResult(t, m)

	_, m = postJSON(t, srv, "/mcp", modernHeaders("tools/call", "purge"),
		confirmBody(t, 2, "purge", map[string]any{"target": "everything"}, elicitMeta(), state, "accept"))
	inputRequiredResult(t, m)
	if execs.Load() != 0 {
		t.Fatalf("cross-argument state presentation executed the leaf")
	}
	if events := sink.snapshot(); len(events) != 1 {
		t.Errorf("audit events=%d want=1", len(events))
	}
}

func TestModernConfirm_PrincipalBinding(t *testing.T) {
	// A state minted under one Authorization value fails verification
	// when presented under another.
	srv, execs, b := confirmServer(t, WithMCPConfirmationKey(mcpConfirmTestKey))
	sink := &recordingSink{}
	b.appendSink(SinkSpec{Sink: sink, OnError: true})

	aliceHeaders := modernHeaders("tools/call", "purge")
	aliceHeaders["Authorization"] = "Bearer alice"
	_, m := postJSON(t, srv, "/mcp", aliceHeaders,
		confirmBody(t, 1, "purge", nil, elicitMeta(), "", ""))
	_, state := inputRequiredResult(t, m)

	bobHeaders := modernHeaders("tools/call", "purge")
	bobHeaders["Authorization"] = "Bearer bob"
	_, m = postJSON(t, srv, "/mcp", bobHeaders,
		confirmBody(t, 2, "purge", nil, elicitMeta(), state, "accept"))
	inputRequiredResult(t, m)
	if execs.Load() != 0 {
		t.Fatalf("cross-principal state presentation executed the leaf")
	}
	if events := sink.snapshot(); len(events) != 1 {
		t.Errorf("audit events=%d want=1", len(events))
	}

	// Same principal completes normally.
	_, m = postJSON(t, srv, "/mcp", aliceHeaders,
		confirmBody(t, 3, "purge", nil, elicitMeta(), state, "accept"))
	if res, _ := m["result"].(map[string]any); res["resultType"] != "complete" {
		t.Fatalf("same-principal retry resultType=%v want=complete", res["resultType"])
	}
	if execs.Load() != 1 {
		t.Errorf("executions=%d want=1", execs.Load())
	}
}

func TestModernConfirm_MissingAnswerReprompts(t *testing.T) {
	// A retry with authentic state but no usable confirm answer
	// re-requests the missing information instead of erroring.
	srv, execs, _ := confirmServer(t, WithMCPConfirmationKey(mcpConfirmTestKey))
	_, m := postJSON(t, srv, "/mcp", modernHeaders("tools/call", "purge"),
		confirmBody(t, 1, "purge", nil, elicitMeta(), "", ""))
	_, state := inputRequiredResult(t, m)

	_, m = postJSON(t, srv, "/mcp", modernHeaders("tools/call", "purge"),
		confirmBody(t, 2, "purge", nil, elicitMeta(), state, ""))
	inputRequiredResult(t, m)
	if execs.Load() != 0 {
		t.Errorf("answerless retry executed the leaf")
	}
}

func TestModernConfirm_NoCapability_HeaderFallback(t *testing.T) {
	// Clients that did not declare form-mode elicitation keep the
	// X-Confirm-Token gate even on a key-configured mount: no
	// inputRequests may be sent to them (spec MUST NOT).
	metas := map[string]map[string]any{
		"no elicitation":   modernMeta(),
		"url mode only":    {metaKeyProtocolVersion: mcpModernProtocolVersion, metaKeyClientCapabilities: map[string]any{"elicitation": map[string]any{"url": map[string]any{}}}},
		"null declaration": {metaKeyProtocolVersion: mcpModernProtocolVersion, metaKeyClientCapabilities: map[string]any{"elicitation": nil}},
	}
	for name, meta := range metas {
		t.Run(name, func(t *testing.T) {
			srv, execs, _ := confirmServer(t, WithMCPConfirmationKey(mcpConfirmTestKey))
			status, m := postJSON(t, srv, "/mcp", modernHeaders("tools/call", "purge"),
				confirmBody(t, 1, "purge", nil, meta, "", ""))
			if status != http.StatusPreconditionRequired {
				t.Fatalf("status=%d want=428: %v", status, m)
			}
			res, _ := m["result"].(map[string]any)
			if res["resultType"] != "complete" {
				t.Errorf("resultType=%v want=complete", res["resultType"])
			}
			if res["isError"] != true {
				t.Errorf("isError=%v want=true", res["isError"])
			}
			if _, ok := res["inputRequests"]; ok {
				t.Errorf("inputRequests sent to a client without the capability: %v", res)
			}
			if _, ok := res["requestState"]; ok {
				t.Errorf("requestState sent on the header-gate path: %v", res)
			}

			// The header still satisfies confirmation for these clients.
			headers := modernHeaders("tools/call", "purge")
			headers["X-Confirm-Token"] = "yes"
			status, m = postJSON(t, srv, "/mcp", headers,
				confirmBody(t, 2, "purge", nil, meta, "", ""))
			if status != http.StatusOK {
				t.Fatalf("with header status=%d want=200: %v", status, m)
			}
			if res, _ := m["result"].(map[string]any); res["isError"] != false {
				t.Errorf("with header isError=%v want=false", res["isError"])
			}
			if execs.Load() != 1 {
				t.Errorf("executions=%d want=1", execs.Load())
			}
		})
	}
}

func TestModernConfirm_NoKey_HeaderGateForEveryone(t *testing.T) {
	// Without WithMCPConfirmationKey the mount behaves exactly as
	// before: even capability-declaring clients get the header gate,
	// and no MRTR members ever appear.
	srv, execs, _ := confirmServer(t)
	status, m := postJSON(t, srv, "/mcp", modernHeaders("tools/call", "purge"),
		confirmBody(t, 1, "purge", nil, elicitMeta(), "", ""))
	if status != http.StatusPreconditionRequired {
		t.Fatalf("status=%d want=428: %v", status, m)
	}
	res, _ := m["result"].(map[string]any)
	if _, ok := res["inputRequests"]; ok {
		t.Errorf("keyless mount produced inputRequests: %v", res)
	}

	headers := modernHeaders("tools/call", "purge")
	headers["X-Confirm-Token"] = "yes"
	status, _ = postJSON(t, srv, "/mcp", headers,
		confirmBody(t, 2, "purge", nil, elicitMeta(), "", ""))
	if status != http.StatusOK || execs.Load() != 1 {
		t.Fatalf("header path broken: status=%d execs=%d", status, execs.Load())
	}
}

func TestModernConfirm_NonConfirmLeafUnaffected(t *testing.T) {
	// Read leaves execute directly on a key-configured mount; the
	// elicitation gate only guards RequiresConfirmation leaves.
	srv, execs, _ := confirmServer(t, WithMCPConfirmationKey(mcpConfirmTestKey))
	status, m := postJSON(t, srv, "/mcp", modernHeaders("tools/call", "echo"),
		confirmBody(t, 1, "echo", nil, elicitMeta(), "", ""))
	if status != http.StatusOK {
		t.Fatalf("status=%d want=200: %v", status, m)
	}
	res, _ := m["result"].(map[string]any)
	if res["resultType"] != "complete" {
		t.Errorf("resultType=%v want=complete", res["resultType"])
	}
	if execs.Load() != 1 {
		t.Errorf("executions=%d want=1", execs.Load())
	}
}

func TestModernConfirm_DestructiveCeilingUnaffected(t *testing.T) {
	// A fully confirmed MRTR flow never relaxes Policy.Allowed: the
	// destructive leaf stays blocked after a valid accept.
	srv, execs, _ := confirmServer(t, WithMCPConfirmationKey(mcpConfirmTestKey))
	_, m := postJSON(t, srv, "/mcp", modernHeaders("tools/call", "vault.burn"),
		confirmBody(t, 1, "vault.burn", nil, elicitMeta(), "", ""))
	_, state := inputRequiredResult(t, m)

	status, m := postJSON(t, srv, "/mcp", modernHeaders("tools/call", "vault.burn"),
		confirmBody(t, 2, "vault.burn", nil, elicitMeta(), state, "accept"))
	if status != http.StatusOK {
		t.Fatalf("status=%d want=200: %v", status, m)
	}
	res, _ := m["result"].(map[string]any)
	if res["resultType"] != "complete" {
		t.Errorf("resultType=%v want=complete", res["resultType"])
	}
	if res["isError"] != true {
		t.Fatalf("destructive leaf ran through confirmation: %v", res)
	}
	content, _ := res["content"].([]any)
	if text, _ := content[0].(map[string]any)["text"].(string); !strings.Contains(text, "destructive") {
		t.Errorf("block message=%q", text)
	}
	if execs.Load() != 0 {
		t.Errorf("destructive leaf executed")
	}
}

func TestModernConfirm_InputRequiredNeverCacheable(t *testing.T) {
	// Even on a mount with live cache hints, interim input_required
	// results carry neither ttlMs nor cacheScope — while cacheable
	// operations on the same mount do.
	srv, _, _ := confirmServer(t,
		WithMCPConfirmationKey(mcpConfirmTestKey),
		WithMCPCacheHints(30*time.Second, MCPCacheScopePublic))

	_, m := postJSON(t, srv, "/mcp", modernHeaders("tools/call", "purge"),
		confirmBody(t, 1, "purge", nil, elicitMeta(), "", ""))
	res, _ := inputRequiredResult(t, m)
	if _, ok := res["ttlMs"]; ok {
		t.Errorf("ttlMs on input_required: %v", res)
	}
	if _, ok := res["cacheScope"]; ok {
		t.Errorf("cacheScope on input_required: %v", res)
	}

	// Control: the mount's hints are active on cacheable results.
	_, m = postJSON(t, srv, "/mcp", modernHeaders("tools/list", ""),
		modernBody(t, "tools/list", nil))
	listRes, _ := m["result"].(map[string]any)
	if listRes["ttlMs"] != float64(30000) || listRes["cacheScope"] != "public" {
		t.Fatalf("cache hints not active on tools/list: %v", listRes)
	}
}

func TestModernConfirm_CrossInstanceVerification(t *testing.T) {
	// Two independently mounted handlers sharing key material: state
	// minted by one verifies on the other (any-instance
	// statelessness).
	srvA, execsA, _ := confirmServer(t, WithMCPConfirmationKey(mcpConfirmTestKey))
	srvB, execsB, _ := confirmServer(t, WithMCPConfirmationKey(mcpConfirmTestKey))
	args := map[string]any{"target": "data"}

	_, m := postJSON(t, srvA, "/mcp", modernHeaders("tools/call", "purge"),
		confirmBody(t, 1, "purge", args, elicitMeta(), "", ""))
	_, state := inputRequiredResult(t, m)

	status, m := postJSON(t, srvB, "/mcp", modernHeaders("tools/call", "purge"),
		confirmBody(t, 2, "purge", args, elicitMeta(), state, "accept"))
	if status != http.StatusOK {
		t.Fatalf("status=%d want=200: %v", status, m)
	}
	res, _ := m["result"].(map[string]any)
	if res["resultType"] != "complete" || res["isError"] != false {
		t.Fatalf("cross-instance retry did not execute: %v", res)
	}
	if execsA.Load() != 0 || execsB.Load() != 1 {
		t.Errorf("execs A=%d B=%d want 0/1", execsA.Load(), execsB.Load())
	}
}

func TestModernConfirm_MountRequiresNonEmptyKey(t *testing.T) {
	for name, key := range map[string][]byte{"nil": nil, "empty": {}} {
		t.Run(name, func(t *testing.T) {
			root, _ := confirmTestTree()
			err := MountMCP(New(root), api.NewRouter(), WithMCPConfirmationKey(key))
			if err == nil || !strings.Contains(err.Error(), "empty key") {
				t.Fatalf("err=%v want empty-key mount error", err)
			}
		})
	}
}

func TestVerifyMCPConfirmState_Table(t *testing.T) {
	b := mcpConfirmBinding{tool: "purge", argsDigest: "aa", principal: ""}
	now := time.Now()
	valid := mintMCPConfirmState(mcpConfirmTestKey, b, now.Add(time.Minute))
	expired := mintMCPConfirmState(mcpConfirmTestKey, b, now.Add(-time.Minute))

	otherBinding := b
	otherBinding.tool = "vault burn"

	// A tampered expiry over an otherwise authentic state must fail
	// the MAC (invalid), never read as expired/valid.
	parts := strings.Split(valid, ".")
	forgedExpiry := parts[0] + ".1." + parts[2]

	cases := map[string]struct {
		state   string
		binding mcpConfirmBinding
		key     []byte
		want    mcpConfirmStateStatus
	}{
		"valid":                 {valid, b, mcpConfirmTestKey, mcpConfirmStateValid},
		"expired but authentic": {expired, b, mcpConfirmTestKey, mcpConfirmStateExpired},
		"wrong key":             {valid, b, []byte("another-32-byte-key-............"), mcpConfirmStateInvalid},
		"wrong binding":         {valid, otherBinding, mcpConfirmTestKey, mcpConfirmStateInvalid},
		"forged expiry":         {forgedExpiry, b, mcpConfirmTestKey, mcpConfirmStateInvalid},
		"garbage":               {"not-a-state", b, mcpConfirmTestKey, mcpConfirmStateInvalid},
		"empty":                 {"", b, mcpConfirmTestKey, mcpConfirmStateInvalid},
		"unknown version":       {"v9" + valid[2:], b, mcpConfirmTestKey, mcpConfirmStateInvalid},
		"non-decimal expiry":    {parts[0] + ".soon." + parts[2], b, mcpConfirmTestKey, mcpConfirmStateInvalid},
		"undecodable tag":       {parts[0] + "." + parts[1] + ".!!!", b, mcpConfirmTestKey, mcpConfirmStateInvalid},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			if got := verifyMCPConfirmState(tc.key, tc.state, tc.binding, now); got != tc.want {
				t.Errorf("verify=%v want=%v", got, tc.want)
			}
		})
	}
}
