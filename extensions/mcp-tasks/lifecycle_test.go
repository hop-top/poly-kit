// Copyright 2026 The Model Context Protocol Authors. All rights reserved.
// Use of this source code is governed by the license
// that can be found in the LICENSE file.

package tasks_test

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/modelcontextprotocol/go-sdk/jsonrpc"
	"github.com/modelcontextprotocol/go-sdk/mcp"

	"mcpext.example/tasks"
)

// TestCreatePollComplete pins the required core: server-directed
// creation on tools/call for a declaring client, durable before the
// response (tasks/get resolves while the executor is still blocked),
// then polling to completed with the CallToolResult structure in
// result.
func TestCreatePollComplete(t *testing.T) {
	release := make(chan struct{})
	exec := func(ctx context.Context, h *tasks.Handle) (*mcp.CallToolResult, error) {
		select {
		case <-release:
		case <-ctx.Done():
			return nil, ctx.Err()
		}
		return textResult("done"), nil
	}
	var ext *tasks.Extension
	ts, ext := newHarness(t, &tasks.Options{TTL: time.Minute, PollInterval: 100 * time.Millisecond},
		func(ctx context.Context, req *mcp.CallToolRequest) (*mcp.CallToolResult, error) {
			if tasks.ClientDeclares(req) {
				return ext.StartTask(ctx, req, exec)
			}
			return textResult("inline"), nil
		})

	created := createTask(t, ts, nil)
	if created["status"] != "working" {
		t.Errorf("seed status = %v, want working", created["status"])
	}
	if v, ok := created["ttlMs"].(float64); !ok || v != 60000 {
		t.Errorf("ttlMs = %v, want 60000", created["ttlMs"])
	}
	if v, ok := created["pollIntervalMs"].(float64); !ok || v != 100 {
		t.Errorf("pollIntervalMs = %v, want 100", created["pollIntervalMs"])
	}
	taskID := created["taskId"].(string)

	// Durable before respond: the executor has not finished (it is
	// blocked), yet the task must already resolve.
	res := resultMap(t, getTask(t, ts, taskID, nil))
	if res["status"] != "working" {
		t.Fatalf("immediate tasks/get status = %v, want working", res["status"])
	}
	if res["resultType"] != "complete" {
		t.Errorf("tasks/get resultType = %v, want complete", res["resultType"])
	}

	close(release)
	final := pollUntil(t, ts, taskID, nil, "completed")
	result, ok := final["result"].(map[string]any)
	if !ok {
		t.Fatalf("completed task has no result object: %v", final)
	}
	content, _ := result["content"].([]any)
	if len(content) != 1 {
		t.Fatalf("result.content = %v, want one text block", result["content"])
	}
	block := content[0].(map[string]any)
	if block["type"] != "text" || block["text"] != "done" {
		t.Errorf("result content = %v, want text done", block)
	}
	if _, hasErr := result["isError"]; hasErr {
		t.Errorf("result.isError present on success: %v", result)
	}
}

// TestNonDeclaringClientInline pins the server-directed rule: without
// the per-request declaration the same call returns the standard
// inline result, never a CreateTaskResult. It also pins the protocol
// version gate: a request that declares the capability but negotiates
// an older protocol version is treated as non-declaring per SEP-2663.
func TestNonDeclaringClientInline(t *testing.T) {
	var ext *tasks.Extension
	ts, ext := newHarness(t, nil,
		func(ctx context.Context, req *mcp.CallToolRequest) (*mcp.CallToolResult, error) {
			if tasks.ClientDeclares(req) {
				return ext.StartTask(ctx, req, func(context.Context, *tasks.Handle) (*mcp.CallToolResult, error) {
					return textResult("task"), nil
				})
			}
			return textResult("inline"), nil
		})

	assertInline := func(env *rpcEnvelope, label string) {
		t.Helper()
		res := resultMap(t, env)
		if res["resultType"] == "task" {
			t.Fatalf("%s: got CreateTaskResult for non-declaring client", label)
		}
		if _, ok := res["taskId"]; ok {
			t.Fatalf("%s: taskId leaked into inline result: %v", label, res)
		}
		content, _ := res["content"].([]any)
		if len(content) != 1 || content[0].(map[string]any)["text"] != "inline" {
			t.Errorf("%s: content = %v, want inline text", label, res["content"])
		}
	}

	// 2026-07-28 client, no extension declared.
	assertInline(post(t, ts.URL, callToolHeaders(nil), callToolBody(1, false, "")), "undeclared")

	// Old-protocol request that does declare the extension: no
	// per-request protocolVersion _meta, so the stateless session
	// negotiates a pre-2026 version — the declaration must not count.
	body := `{"jsonrpc":"2.0","id":2,"method":"tools/call","params":{"name":"op","arguments":{},` +
		`"_meta":{"io.modelcontextprotocol/clientCapabilities":{"extensions":{"` + tasks.ExtensionID + `":{}}}}}}`
	assertInline(post(t, ts.URL, nil, body), "old protocol")
}

// TestCancelCooperative pins tasks/cancel: empty ack with resultType
// complete, cooperative context cancellation, terminal cancelled
// state, idempotent re-cancel, and cancel-after-completion leaving
// the completed state untouched.
func TestCancelCooperative(t *testing.T) {
	exec := func(ctx context.Context, h *tasks.Handle) (*mcp.CallToolResult, error) {
		<-ctx.Done()
		return nil, ctx.Err()
	}
	var ext *tasks.Extension
	ts, ext := newHarness(t, nil, taskToolHandlerVar(&ext, exec))

	taskID := createTask(t, ts, nil)["taskId"].(string)

	cancel := func(id int) map[string]any {
		env := post(t, ts.URL, tasksHeaders(tasks.MethodCancel, taskID),
			tasksBody(id, tasks.MethodCancel, taskID, true, ""))
		return resultMap(t, env)
	}
	ack := cancel(3)
	if ack["resultType"] != "complete" || len(ack) != 1 {
		t.Errorf("cancel ack = %v, want empty ack with resultType complete", ack)
	}
	final := pollUntil(t, ts, taskID, nil, "cancelled")
	if _, ok := final["result"]; ok {
		t.Errorf("cancelled task carries result: %v", final)
	}

	// Idempotent: cancelling a terminal task still acks.
	if ack := cancel(4); ack["resultType"] != "complete" {
		t.Errorf("re-cancel ack = %v", ack)
	}
	if res := resultMap(t, getTask(t, ts, taskID, nil)); res["status"] != "cancelled" {
		t.Errorf("status after re-cancel = %v, want cancelled", res["status"])
	}
}

// TestCancelAfterCompletion pins that a terminal completed task stays
// completed through a later cancel.
func TestCancelAfterCompletion(t *testing.T) {
	var ext *tasks.Extension
	ts, ext := newHarness(t, nil, taskToolHandlerVar(&ext, func(context.Context, *tasks.Handle) (*mcp.CallToolResult, error) {
		return textResult("fast"), nil
	}))

	taskID := createTask(t, ts, nil)["taskId"].(string)
	pollUntil(t, ts, taskID, nil, "completed")

	env := post(t, ts.URL, tasksHeaders(tasks.MethodCancel, taskID),
		tasksBody(3, tasks.MethodCancel, taskID, true, ""))
	if ack := resultMap(t, env); ack["resultType"] != "complete" {
		t.Errorf("cancel ack = %v", ack)
	}
	if res := resultMap(t, getTask(t, ts, taskID, nil)); res["status"] != "completed" {
		t.Errorf("status after cancel = %v, want completed (work finished first)", res["status"])
	}
}

// TestFailedVersusCompleted pins the SEP's strict fault separation:
// executor errors are protocol faults (failed + JSON-RPC error, with
// *jsonrpc.Error preserved verbatim), while tool-level errors
// (isError results) complete the task with the error-carrying result.
func TestFailedVersusCompleted(t *testing.T) {
	t.Run("executor error becomes failed", func(t *testing.T) {
		var ext *tasks.Extension
		ts, ext := newHarness(t, nil, taskToolHandlerVar(&ext, func(context.Context, *tasks.Handle) (*mcp.CallToolResult, error) {
			return nil, errors.New("boom")
		}))
		taskID := createTask(t, ts, nil)["taskId"].(string)
		final := pollUntil(t, ts, taskID, nil, "failed")
		jerr, ok := final["error"].(map[string]any)
		if !ok {
			t.Fatalf("failed task has no error object: %v", final)
		}
		if jerr["code"] != float64(-32603) || jerr["message"] != "boom" {
			t.Errorf("error = %v, want -32603 boom", jerr)
		}
		if final["statusMessage"] != "boom" {
			t.Errorf("statusMessage = %v, want boom", final["statusMessage"])
		}
		if _, ok := final["result"]; ok {
			t.Errorf("failed task carries result: %v", final)
		}
	})

	t.Run("jsonrpc error preserved verbatim", func(t *testing.T) {
		var ext *tasks.Extension
		ts, ext := newHarness(t, nil, taskToolHandlerVar(&ext, func(context.Context, *tasks.Handle) (*mcp.CallToolResult, error) {
			return nil, &jsonrpc.Error{Code: -32001, Message: "upstream unavailable"}
		}))
		taskID := createTask(t, ts, nil)["taskId"].(string)
		final := pollUntil(t, ts, taskID, nil, "failed")
		jerr, _ := final["error"].(map[string]any)
		if jerr["code"] != float64(-32001) || jerr["message"] != "upstream unavailable" {
			t.Errorf("error = %v, want verbatim -32001", jerr)
		}
	})

	t.Run("isError result becomes completed", func(t *testing.T) {
		var ext *tasks.Extension
		ts, ext := newHarness(t, nil, taskToolHandlerVar(&ext, func(context.Context, *tasks.Handle) (*mcp.CallToolResult, error) {
			res := textResult("tool failed: bad input")
			res.IsError = true
			return res, nil
		}))
		taskID := createTask(t, ts, nil)["taskId"].(string)
		final := pollUntil(t, ts, taskID, nil, "completed")
		result, ok := final["result"].(map[string]any)
		if !ok {
			t.Fatalf("completed task has no result: %v", final)
		}
		if result["isError"] != true {
			t.Errorf("result.isError = %v, want true", result["isError"])
		}
		if _, ok := final["error"]; ok {
			t.Errorf("completed task carries protocol error: %v", final)
		}
	})
}

// TestTTLExpiry pins TTL semantics: an expired task is
// indistinguishable from an unknown one, and a negative TTL option
// advertises ttlMs null (unlimited).
func TestTTLExpiry(t *testing.T) {
	var ext *tasks.Extension
	ts, ext := newHarness(t, &tasks.Options{TTL: 30 * time.Millisecond},
		taskToolHandlerVar(&ext, func(context.Context, *tasks.Handle) (*mcp.CallToolResult, error) {
			return textResult("fast"), nil
		}))

	created := createTask(t, ts, nil)
	if v, ok := created["ttlMs"].(float64); !ok || v != 30 {
		t.Errorf("ttlMs = %v, want 30", created["ttlMs"])
	}
	taskID := created["taskId"].(string)
	time.Sleep(80 * time.Millisecond)
	env := getTask(t, ts, taskID, nil)
	if env.Error == nil || env.Error.Code != -32602 {
		t.Fatalf("expired tasks/get = %+v, want -32602", env)
	}
}

// TestUnlimitedTTL pins ttlMs null on the wire for unlimited tasks.
func TestUnlimitedTTL(t *testing.T) {
	var ext *tasks.Extension
	ts, ext := newHarness(t, &tasks.Options{TTL: -1},
		taskToolHandlerVar(&ext, func(context.Context, *tasks.Handle) (*mcp.CallToolResult, error) {
			return textResult("fast"), nil
		}))

	env := post(t, ts.URL, callToolHeaders(nil), callToolBody(1, true, ""))
	if env.Error != nil {
		t.Fatalf("tools/call error: %+v", env.Error)
	}
	if !strings.Contains(string(env.Result), `"ttlMs":null`) {
		t.Errorf("result = %s, want explicit ttlMs null", env.Result)
	}
}

// taskToolHandlerVar defers the extension reference so harness
// construction can close the loop (the handler needs the extension
// built by newHarness).
func taskToolHandlerVar(ext **tasks.Extension, exec tasks.ExecuteFunc) mcp.ToolHandler {
	return func(ctx context.Context, req *mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		if tasks.ClientDeclares(req) {
			return (*ext).StartTask(ctx, req, exec)
		}
		return textResult("inline"), nil
	}
}
