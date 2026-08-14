// Copyright 2026 The Model Context Protocol Authors. All rights reserved.
// Use of this source code is governed by the license
// that can be found in the LICENSE file.

package tasks_test

import (
	"context"
	"fmt"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"mcpext.example/tasks"
)

// elicit is shorthand for an elicitation input request.
func elicit(msg string) mcp.InputRequest { return &mcp.ElicitParams{Message: msg} }

// acceptJSON is an accepting ElicitResult for tasks/update bodies.
func acceptJSON(field, value string) string {
	return fmt.Sprintf(`{"action":"accept","content":{%q:%q}}`, field, value)
}

// TestInputRequiredRoundTrip drives the full tasks/update contract:
// input_required exposes the outstanding requests in MRTR wire shape,
// partial responses are accepted and shrink the outstanding set,
// responses to unknown and already-answered keys are ignored, and a
// second generation of requests keeps key uniqueness per lifetime
// while ignoring responses to superseded-phase keys.
func TestInputRequiredRoundTrip(t *testing.T) {
	got := make(chan mcp.InputResponseMap, 2)
	exec := func(ctx context.Context, h *tasks.Handle) (*mcp.CallToolResult, error) {
		first, err := h.RequestInput(ctx, mcp.InputRequestMap{
			"q1": elicit("first question"),
			"q2": elicit("second question"),
		})
		if err != nil {
			return nil, err
		}
		got <- first
		second, err := h.RequestInput(ctx, mcp.InputRequestMap{"q3": elicit("third question")})
		if err != nil {
			return nil, err
		}
		got <- second
		return textResult("answered"), nil
	}
	var ext *tasks.Extension
	ts, ext := newHarness(t, nil, taskToolHandlerVar(&ext, exec))

	taskID := createTask(t, ts, nil)["taskId"].(string)

	sendUpdate := func(id int, inputResponses string) map[string]any {
		t.Helper()
		env := post(t, ts.URL, tasksHeaders(tasks.MethodUpdate, taskID),
			tasksBody(id, tasks.MethodUpdate, taskID, true, inputResponses))
		ack := resultMap(t, env)
		if ack["resultType"] != "complete" || len(ack) != 1 {
			t.Fatalf("update ack = %v, want empty ack with resultType complete", ack)
		}
		return ack
	}
	outstanding := func(res map[string]any) map[string]any {
		t.Helper()
		reqs, _ := res["inputRequests"].(map[string]any)
		return reqs
	}

	// First generation becomes visible with the MRTR wire shape.
	res := pollUntil(t, ts, taskID, nil, "input_required")
	reqs := outstanding(res)
	if len(reqs) != 2 || reqs["q1"] == nil || reqs["q2"] == nil {
		t.Fatalf("inputRequests = %v, want q1+q2", reqs)
	}
	q1, _ := reqs["q1"].(map[string]any)
	if q1["method"] != "elicitation/create" {
		t.Errorf("q1.method = %v, want elicitation/create", q1["method"])
	}
	params, _ := q1["params"].(map[string]any)
	if params["message"] != "first question" {
		t.Errorf("q1.params = %v, want the elicitation message", params)
	}

	// Responses to keys that were never issued are ignored.
	sendUpdate(10, `{"nope":`+acceptJSON("x", "y")+`}`)
	res = resultMap(t, getTask(t, ts, taskID, nil))
	if res["status"] != "input_required" || len(outstanding(res)) != 2 {
		t.Fatalf("after unknown-key update: %v, want both keys still outstanding", res)
	}

	// A partial set (strict subset) is accepted and shrinks the
	// visible outstanding set to the remainder.
	sendUpdate(11, `{"q1":`+acceptJSON("a", "1")+`}`)
	res = pollUntilFunc(t, ts, taskID, func(m map[string]any) bool {
		return m["status"] == "input_required" && len(outstanding(m)) == 1
	})
	if outstanding(res)["q2"] == nil {
		t.Fatalf("remaining inputRequests = %v, want only q2", outstanding(res))
	}

	// Re-answering q1 (already answered) is ignored.
	sendUpdate(12, `{"q1":`+acceptJSON("a", "overwrite")+`}`)

	// Completing the set resumes the executor.
	sendUpdate(13, `{"q2":`+acceptJSON("b", "2")+`}`)
	first := <-got
	if len(first) != 2 {
		t.Fatalf("executor received %d responses, want 2", len(first))
	}
	er, ok := first["q1"].(*mcp.ElicitResult)
	if !ok || er.Action != "accept" || er.Content["a"] != "1" {
		t.Errorf("q1 response = %#v, want the first accept (not the ignored overwrite)", first["q1"])
	}

	// Second generation: q3 outstanding; the superseded-phase key q1
	// is ignored, then q3 completes the task.
	res = pollUntilFunc(t, ts, taskID, func(m map[string]any) bool {
		return m["status"] == "input_required" && outstanding(m)["q3"] != nil
	})
	if len(outstanding(res)) != 1 {
		t.Fatalf("second generation inputRequests = %v, want only q3", outstanding(res))
	}
	sendUpdate(14, `{"q1":`+acceptJSON("a", "stale")+`}`)
	sendUpdate(15, `{"q3":`+acceptJSON("c", "3")+`}`)
	second := <-got
	if len(second) != 1 || second["q3"] == nil {
		t.Fatalf("second generation responses = %v, want q3 only", second)
	}
	pollUntil(t, ts, taskID, nil, "completed")
}

// TestInputRequestKeyReuseRejected pins lifetime key uniqueness: the
// library refuses a reused key, and the executor surfacing that error
// fails the task.
func TestInputRequestKeyReuseRejected(t *testing.T) {
	responses := make(chan struct{})
	exec := func(ctx context.Context, h *tasks.Handle) (*mcp.CallToolResult, error) {
		if _, err := h.RequestInput(ctx, mcp.InputRequestMap{"k": elicit("once")}); err != nil {
			return nil, err
		}
		close(responses)
		_, err := h.RequestInput(ctx, mcp.InputRequestMap{"k": elicit("again")})
		if err == nil {
			return textResult("reuse allowed"), nil
		}
		return nil, err
	}
	var ext *tasks.Extension
	ts, ext := newHarness(t, nil, taskToolHandlerVar(&ext, exec))

	taskID := createTask(t, ts, nil)["taskId"].(string)
	pollUntil(t, ts, taskID, nil, "input_required")
	post(t, ts.URL, tasksHeaders(tasks.MethodUpdate, taskID),
		tasksBody(3, tasks.MethodUpdate, taskID, true, `{"k":`+acceptJSON("x", "1")+`}`))
	<-responses

	final := pollUntil(t, ts, taskID, nil, "failed")
	if msg, _ := final["statusMessage"].(string); !strings.Contains(msg, "reused") {
		t.Errorf("statusMessage = %q, want key-reuse diagnostic", msg)
	}
}

// TestMRTRThenTaskComposition pins SEP-2663's composition rule at the
// wire: a tool resolves an MRTR exchange (SEP-2322) synchronously —
// input_required result, client retry with inputResponses — and only
// then responds with CreateTaskResult. The task phase afterwards uses
// its own inputRequests key namespace: reusing the MRTR-phase key is
// legal and unambiguous.
func TestMRTRThenTaskComposition(t *testing.T) {
	const mrtrKey = "confirm"
	exec := func(ctx context.Context, h *tasks.Handle) (*mcp.CallToolResult, error) {
		// Task-phase input request reusing the MRTR-phase key.
		resp, err := h.RequestInput(ctx, mcp.InputRequestMap{mrtrKey: elicit("task-phase question")})
		if err != nil {
			return nil, err
		}
		er, _ := resp[mrtrKey].(*mcp.ElicitResult)
		return textResult("task-phase answer: " + fmt.Sprint(er.Content["v"])), nil
	}

	var ext *tasks.Extension
	ts, ext := newHarness(t, nil, func(ctx context.Context, req *mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		if !tasks.ClientDeclares(req) {
			return textResult("inline"), nil
		}
		if len(req.Params.InputResponses) == 0 {
			// MRTR phase: gather input synchronously first.
			return &mcp.CallToolResult{
				InputRequests: mcp.InputRequestMap{mrtrKey: elicit("proceed as a task?")},
				RequestState:  "mrtr-state-1",
			}, nil
		}
		er, ok := req.Params.InputResponses[mrtrKey].(*mcp.ElicitResult)
		if !ok || er.Action != "accept" {
			res := textResult("declined")
			res.IsError = true
			return res, nil
		}
		// MRTR resolved; hand off to asynchronous execution.
		return ext.StartTask(ctx, req, exec)
	})

	// Phase 1: the call comes back input_required, not a task.
	env := post(t, ts.URL, callToolHeaders(nil), callToolBody(1, true, ""))
	res := resultMap(t, env)
	if res["resultType"] != "input_required" {
		t.Fatalf("MRTR phase resultType = %v, want input_required (result: %v)", res["resultType"], res)
	}
	if _, ok := res["taskId"]; ok {
		t.Fatal("task created before the MRTR exchange resolved")
	}
	reqs, _ := res["inputRequests"].(map[string]any)
	if reqs[mrtrKey] == nil {
		t.Fatalf("MRTR inputRequests = %v, want %q", reqs, mrtrKey)
	}
	state, _ := res["requestState"].(string)
	if state == "" {
		t.Fatal("MRTR phase carries no requestState")
	}

	// Phase 2: retry with the responses; only now the task appears.
	retry := fmt.Sprintf(`"inputResponses":{%q:%s},"requestState":%q`,
		mrtrKey, `{"action":"accept"}`, state)
	env = post(t, ts.URL, callToolHeaders(nil), callToolBody(2, true, retry))
	created := resultMap(t, env)
	if created["resultType"] != "task" {
		t.Fatalf("post-MRTR resultType = %v, want task (result: %v)", created["resultType"], created)
	}
	taskID := created["taskId"].(string)

	// Task phase: the same key reappears independently.
	res = pollUntil(t, ts, taskID, nil, "input_required")
	taskReqs, _ := res["inputRequests"].(map[string]any)
	if taskReqs[mrtrKey] == nil {
		t.Fatalf("task-phase inputRequests = %v, want key %q reused independently", taskReqs, mrtrKey)
	}
	post(t, ts.URL, tasksHeaders(tasks.MethodUpdate, taskID),
		tasksBody(3, tasks.MethodUpdate, taskID, true, `{"confirm":`+acceptJSON("v", "42")+`}`))
	final := pollUntil(t, ts, taskID, nil, "completed")
	result, _ := final["result"].(map[string]any)
	content, _ := result["content"].([]any)
	if len(content) != 1 || !strings.Contains(content[0].(map[string]any)["text"].(string), "42") {
		t.Errorf("final result = %v, want the task-phase answer", result)
	}
}

// pollUntilFunc polls tasks/get until cond holds.
func pollUntilFunc(t *testing.T, ts *httptest.Server, taskID string, cond func(map[string]any) bool) map[string]any {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		res := resultMap(t, getTask(t, ts, taskID, nil))
		if cond(res) {
			return res
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("timed out waiting for task %s condition", taskID)
	return nil
}
