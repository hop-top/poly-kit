package mcpsdk

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"hop.top/kit/go/transport/cmdsurface"
)

// elicitClient dials with a client whose user answers every
// elicitation with action, counting the questions asked.
func elicitClient(t *testing.T, endpoint, action string, asked *int) *mcp.ClientSession {
	t.Helper()
	return connectOpts(t, endpoint, &mcp.ClientOptions{
		ElicitationHandler: func(_ context.Context, req *mcp.ElicitRequest) (*mcp.ElicitResult, error) {
			*asked++
			if !strings.Contains(req.Params.Message, `"deploy"`) {
				t.Errorf("question = %q, want it to name the tool", req.Params.Message)
			}
			return &mcp.ElicitResult{Action: action}, nil
		},
	})
}

func callText(t *testing.T, sess *mcp.ClientSession, name string) (string, bool) {
	t.Helper()
	res, err := sess.CallTool(t.Context(), &mcp.CallToolParams{Name: name, Arguments: map[string]any{}})
	if err != nil {
		t.Fatalf("CallTool(%s): %v", name, err)
	}
	return textOf(res), res.IsError
}

func TestConfirmationElicitationAcceptRunsTheCall(t *testing.T) {
	srv, _, _ := newSurfaceHarness(t, newTestTree(), nil, WithConfirmationElicitation(nil))
	asked := 0
	sess := elicitClient(t, srv.URL+"/mcp", "accept", &asked)

	text, isErr := callText(t, sess, "deploy")
	if isErr || !strings.Contains(text, "deployed") {
		t.Fatalf("accepted call: isError=%t text=%q, want it to run", isErr, text)
	}
	if asked != 1 {
		t.Fatalf("questions asked = %d, want exactly one per call", asked)
	}

	// The next call is a new act: it is asked again.
	if _, isErr := callText(t, sess, "deploy"); isErr || asked != 2 {
		t.Fatalf("second call: isError=%t asked=%d, want a second question", isErr, asked)
	}
}

func TestConfirmationElicitationDeclineRefuses(t *testing.T) {
	for _, action := range []string{"decline", "cancel"} {
		t.Run(action, func(t *testing.T) {
			sink := &recordingSink{}
			srv, _, _ := newSurfaceHarness(t, newTestTree(),
				[]cmdsurface.Option{cmdsurface.WithSinks(cmdsurface.SinkSpec{Sink: sink, OnError: true, OnOK: true})},
				WithConfirmationElicitation(nil))
			asked := 0
			sess := elicitClient(t, srv.URL+"/mcp", action, &asked)

			text, isErr := callText(t, sess, "deploy")
			if !isErr || text != "confirmation declined" || asked != 1 {
				t.Fatalf("isError=%t text=%q asked=%d, want one question then a refusal", isErr, text, asked)
			}
			recs := sink.snapshot()
			if len(recs) != 1 || !errors.Is(recs[0].err, ErrConfirmationDeclined) {
				t.Fatalf("audit = %+v, want one declined record", recs)
			}
		})
	}
}

func TestConfirmationElicitationWithoutClientSupportRefusesClearly(t *testing.T) {
	sink := &recordingSink{}
	srv, _, _ := newSurfaceHarness(t, newTestTree(),
		[]cmdsurface.Option{cmdsurface.WithSinks(cmdsurface.SinkSpec{Sink: sink, OnError: true, OnOK: true})},
		WithConfirmationElicitation(nil))
	sess := connectOpts(t, srv.URL+"/mcp", nil)

	text, isErr := callText(t, sess, "deploy")
	if !isErr || strings.Contains(text, "deployed") {
		t.Fatalf("isError=%t text=%q, want a refusal", isErr, text)
	}
	for _, want := range []string{"confirmation required", "elicitation", "X-Confirm-Token"} {
		if !strings.Contains(text, want) {
			t.Errorf("refusal %q does not name %q", text, want)
		}
	}
	recs := sink.snapshot()
	if len(recs) != 1 || !errors.Is(recs[0].err, ErrConfirmationRequired) {
		t.Fatalf("audit = %+v, want one confirmation-required record", recs)
	}
}

func TestConfirmationHeaderStillSatisfiesTheGate(t *testing.T) {
	srv, _, _ := newSurfaceHarness(t, newTestTree(), nil, WithConfirmationElicitation(nil))
	sess := connect(t, srv.URL+"/mcp", map[string]string{"X-Confirm-Token": "yes"})
	if text, isErr := callText(t, sess, "deploy"); isErr || !strings.Contains(text, "deployed") {
		t.Fatalf("isError=%t text=%q, want the header to satisfy the gate", isErr, text)
	}
}

// call2026 posts one 2026-07-28 tools/call for deploy, carrying the
// per-request envelope and extra params members, and returns the
// decoded result.
func call2026(t *testing.T, url, args, extra string) map[string]any {
	t.Helper()
	params := `{"name":"deploy","arguments":` + args
	if extra != "" {
		params += "," + extra
	}
	params += `,"_meta":{"io.modelcontextprotocol/protocolVersion":"2026-07-28",` +
		`"io.modelcontextprotocol/clientCapabilities":{"elicitation":{}},` +
		`"io.modelcontextprotocol/clientInfo":{"name":"mrtr-probe","version":"0"}}}`
	body := `{"jsonrpc":"2.0","id":1,"method":"tools/call","params":` + params + `}`
	req, err := http.NewRequest(http.MethodPost, url, strings.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json, text/event-stream")
	req.Header.Set("Mcp-Protocol-Version", "2026-07-28")
	req.Header.Set("Mcp-Method", "tools/call")
	req.Header.Set("Mcp-Name", "deploy")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	raw, _ := io.ReadAll(resp.Body)
	_ = resp.Body.Close()
	payload := string(raw)
	for _, line := range strings.Split(payload, "\n") {
		if strings.HasPrefix(line, "data:") {
			payload = strings.TrimSpace(strings.TrimPrefix(line, "data:"))
		}
	}
	var env struct {
		Result map[string]any  `json:"result"`
		Error  json.RawMessage `json:"error"`
	}
	if err := json.Unmarshal([]byte(payload), &env); err != nil || env.Result == nil {
		t.Fatalf("status %d, payload %q: %v", resp.StatusCode, payload, err)
	}
	return env.Result
}

// TestConfirmationStateIsBoundAndVerified drives the 2026-07-28 round
// trip by hand against a stateless handler (the only one that serves
// that protocol): a forged state is audited and asked again, an
// approval for other arguments does not transfer, and only the
// genuine answer runs the call.
func TestConfirmationStateIsBoundAndVerified(t *testing.T) {
	sink := &recordingSink{}
	srv, _, _ := newSurfaceHarness(t, newTestTree(),
		[]cmdsurface.Option{cmdsurface.WithSinks(cmdsurface.SinkSpec{Sink: sink, OnError: true, OnOK: true})},
		// 2026-07-28 is served only by a stateless handler.
		WithConfirmationElicitation(nil), WithStateless(), WithJSONResponse())
	url := srv.URL + "/mcp"

	first := call2026(t, url, `{}`, "")
	if first["resultType"] != "input_required" {
		t.Fatalf("first call = %v, want input_required", first)
	}
	reqs, _ := first["inputRequests"].(map[string]any)
	state, _ := first["requestState"].(string)
	if len(reqs) != 1 || state == "" {
		t.Fatalf("first call = %v, want one input request and a state", first)
	}
	var key string
	for k, v := range reqs {
		key = k
		ask, _ := json.Marshal(v)
		if !strings.Contains(string(ask), "elicitation/create") || !strings.Contains(string(ask), `Approve execution of \"deploy\"?`) {
			t.Fatalf("input request = %s, want the approval question", ask)
		}
	}
	accept := `"inputResponses":{"` + key + `":{"action":"accept"}}`

	forged := state[:len(state)-2] + "00"
	res := call2026(t, url, `{}`, accept+`,"requestState":"`+forged+`"`)
	if res["resultType"] != "input_required" {
		t.Fatalf("forged retry = %v, want a fresh question", res)
	}
	if recs := sink.snapshot(); len(recs) != 1 || !errors.Is(recs[0].err, ErrConfirmationStateRejected) {
		t.Fatalf("audit = %+v, want the rejected state recorded", recs)
	}

	res = call2026(t, url, `{"x":2}`, accept+`,"requestState":"`+state+`"`)
	if res["resultType"] != "input_required" {
		t.Fatalf("an approval for other arguments ran the call: %v", res)
	}

	res = call2026(t, url, `{}`, `"inputResponses":{"`+key+`":{"action":"decline"}},"requestState":"`+state+`"`)
	if res["isError"] != true {
		t.Fatalf("declined retry = %v, want a refusal", res)
	}

	res = call2026(t, url, `{}`, accept+`,"requestState":"`+state+`"`)
	raw, _ := json.Marshal(res)
	if res["isError"] == true || !strings.Contains(string(raw), "deployed") {
		t.Fatalf("genuine retry = %s, want it to run", raw)
	}
}

func TestWithAuthenticatedDecidesTheAuthGate(t *testing.T) {
	sink := &recordingSink{}
	allow := false
	srv, _, _ := newSurfaceHarness(t, newTestTree(),
		[]cmdsurface.Option{cmdsurface.WithSinks(cmdsurface.SinkSpec{Sink: sink, OnError: true, OnOK: true})},
		WithAuthenticated(func(context.Context, *mcp.CallToolRequest) bool { return allow }))

	// A bare Authorization header no longer passes once the host
	// answers the question.
	sess := connect(t, srv.URL+"/mcp", map[string]string{"Authorization": "Bearer anything"})
	if text, isErr := callText(t, sess, "secret"); !isErr || text != "authentication required" {
		t.Fatalf("isError=%t text=%q, want a refusal", isErr, text)
	}
	recs := sink.snapshot()
	if len(recs) != 1 || !errors.Is(recs[0].err, cmdsurface.ErrAuthRefused) {
		t.Fatalf("audit = %+v, want one ErrAuthRefused record", recs)
	}

	allow = true
	plain := connect(t, srv.URL+"/mcp", nil)
	if text, isErr := callText(t, plain, "secret"); isErr || !strings.Contains(text, "unlocked") {
		t.Fatalf("isError=%t text=%q, want the host's verdict to admit the call", isErr, text)
	}
}

func TestWithCallMetaReachesTheBridge(t *testing.T) {
	var seen cmdsurface.Meta
	perm := func(_ context.Context, meta cmdsurface.Meta, _ *cmdsurface.Leaf) cmdsurface.PermissionDecision {
		seen = meta
		return cmdsurface.PermissionDecision{Allowed: true}
	}
	srv, _, _ := newSurfaceHarness(t, newTestTree(),
		[]cmdsurface.Option{cmdsurface.WithPermission(perm)},
		WithCallMeta(func(context.Context, *mcp.CallToolRequest) cmdsurface.Meta {
			return cmdsurface.Meta{Caller: "alice", Surface: cmdsurface.SurfaceREST, RequestID: "r-1"}
		}))
	sess := connect(t, srv.URL+"/mcp", nil)
	if _, isErr := callText(t, sess, "ping"); isErr {
		t.Fatal("ping refused")
	}
	if seen.Caller != "alice" || seen.RequestID != "r-1" {
		t.Fatalf("meta = %+v, want the host's provenance", seen)
	}
	if seen.Surface != cmdsurface.SurfaceMCP {
		t.Fatalf("surface = %q, want it pinned to mcp", seen.Surface)
	}
	if seen.RequestedAt.IsZero() {
		t.Fatal("RequestedAt not stamped")
	}
}

// TestConfirmationElicitationLegacyClientOverStdio speaks a pre-2026
// protocol by hand over a pipe pair: the server must ask with a
// server-initiated elicitation/create and resume the call on accept.
func TestConfirmationElicitationLegacyClientOverStdio(t *testing.T) {
	b := cmdsurface.New(newTestTree())
	s, err := New(b, WithConfirmationElicitation(nil),
		WithAuthenticated(func(context.Context, *mcp.CallToolRequest) bool { return true }))
	if err != nil {
		t.Fatal(err)
	}
	cr, sw := io.Pipe() // server -> client
	sr, cw := io.Pipe() // client -> server
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	go func() { _ = s.Server().Run(ctx, &mcp.IOTransport{Reader: sr, Writer: sw}) }()

	lines := bufio.NewScanner(cr)
	send := func(v string) {
		if _, err := io.WriteString(cw, v+"\n"); err != nil {
			t.Fatalf("write: %v", err)
		}
	}
	next := func() map[string]any {
		done := make(chan bool, 1)
		go func() { done <- lines.Scan() }()
		select {
		case ok := <-done:
			if !ok {
				t.Fatalf("stream ended: %v", lines.Err())
			}
		case <-time.After(5 * time.Second):
			t.Fatal("no message from the server")
		}
		var m map[string]any
		if err := json.Unmarshal(lines.Bytes(), &m); err != nil {
			t.Fatalf("server wrote a non-JSON line %q: %v", lines.Text(), err)
		}
		return m
	}

	send(`{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2025-06-18","capabilities":{"elicitation":{}},"clientInfo":{"name":"legacy","version":"1"}}}`)
	if m := next(); m["error"] != nil {
		t.Fatalf("initialize: %v", m)
	}
	send(`{"jsonrpc":"2.0","method":"notifications/initialized"}`)
	send(`{"jsonrpc":"2.0","id":2,"method":"tools/call","params":{"name":"deploy","arguments":{}}}`)

	ask := next()
	if ask["method"] != "elicitation/create" {
		t.Fatalf("want a server-initiated elicitation, got %v", ask)
	}
	idJSON, _ := json.Marshal(ask["id"])
	send(`{"jsonrpc":"2.0","id":` + string(idJSON) + `,"result":{"action":"accept"}}`)

	res := next()
	result, _ := res["result"].(map[string]any)
	raw, _ := json.Marshal(result)
	if result == nil || result["isError"] == true || !strings.Contains(string(raw), "deployed") {
		t.Fatalf("tools/call result = %s, want the resumed call to run", raw)
	}
}
