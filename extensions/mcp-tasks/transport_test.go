// Copyright 2026 The Model Context Protocol Authors. All rights reserved.
// Use of this source code is governed by the license
// that can be found in the LICENSE file.

package tasks_test

// Transport-boundary tests. The extension registers its methods on the
// server rather than wrapping the SDK's HTTP handler, so a tasks/*
// request is dispatched by that handler exactly like a standard method
// and cannot reach task state without clearing the same checks. These
// tests pin that equivalence from the outside: for every rejection the
// SDK performs, a tasks/* request and a standard request must get the
// identical answer, byte for byte, from a server with the extension
// attached and from a bare one without it.

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"mcpext.example/tasks"
)

// newBareServer builds the same server the harness does but WITHOUT
// the extension: the differential baseline.
func newBareServer(t *testing.T) *httptest.Server {
	t.Helper()
	srv := mcp.NewServer(&mcp.Implementation{Name: "tasks-test", Version: "0.0.1"}, &mcp.ServerOptions{})
	srv.AddTool(&mcp.Tool{
		Name:        "op",
		Description: "test operation",
		InputSchema: map[string]any{"type": "object"},
	}, func(context.Context, *mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		return textResult("x"), nil
	})
	ts := httptest.NewServer(mcp.NewStreamableHTTPHandler(
		func(*http.Request) *mcp.Server { return srv },
		&mcp.StreamableHTTPOptions{Stateless: true, JSONResponse: true},
	))
	t.Cleanup(ts.Close)
	return ts
}

// newAttachedServer builds a server with the extension attached.
func newAttachedServer(t *testing.T) *httptest.Server {
	t.Helper()
	ts, _ := newHarness(t, nil, func(context.Context, *mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		return textResult("x"), nil
	})
	return ts
}

// rawRequest is one probe: the request to send and how to mutate it.
type rawRequest struct {
	body   string
	hdr    map[string]string
	mutate func(*http.Request)
}

// send performs one probe and returns status and raw body.
func send(t *testing.T, url string, r rawRequest) (int, string) {
	t.Helper()
	req, err := http.NewRequest(http.MethodPost, url, strings.NewReader(r.body))
	if err != nil {
		t.Fatalf("new request: %v", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json, text/event-stream")
	for k, v := range r.hdr {
		req.Header.Set(k, v)
	}
	if r.mutate != nil {
		r.mutate(req)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("send: %v", err)
	}
	defer resp.Body.Close()
	raw, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("read body: %v", err)
	}
	return resp.StatusCode, string(raw)
}

// transportRejections are the SDK's transport-level rejections, each
// derived from mcp/streamable.go: Host (DNS-rebinding protection),
// Content-Type, Accept, and protocol-version validation. Each must
// apply to a tasks/* request exactly as it does to a standard one.
var transportRejections = []struct {
	name       string
	mutate     func(*http.Request)
	wantStatus int
	wantBody   string
}{
	{
		// StreamableHTTPHandler.ServeHTTP: loopback listener + a
		// non-loopback Host is a DNS-rebinding attempt.
		name:       "non-localhost Host",
		mutate:     func(r *http.Request) { r.Host = "evil.example.com" },
		wantStatus: http.StatusForbidden,
		wantBody:   `Forbidden: invalid Host header "evil.example.com"`,
	},
	{
		// serveStateless: Content-Type must be application/json.
		name:       "wrong Content-Type",
		mutate:     func(r *http.Request) { r.Header.Set("Content-Type", "text/plain") },
		wantStatus: http.StatusUnsupportedMediaType,
		wantBody:   "Content-Type must be 'application/json'",
	},
	{
		// serveStateless: Accept must offer both media types.
		name:       "wrong Accept",
		mutate:     func(r *http.Request) { r.Header.Set("Accept", "application/json") },
		wantStatus: http.StatusBadRequest,
		wantBody:   "Accept must contain both 'application/json' and 'text/event-stream'",
	},
	{
		// ServeHTTP: an unsupported Mcp-Protocol-Version is rejected
		// before any dispatch.
		name:       "unsupported protocol version",
		mutate:     func(r *http.Request) { r.Header.Set("Mcp-Protocol-Version", "1999-01-01") },
		wantStatus: http.StatusBadRequest,
		wantBody:   "Bad Request: Unsupported protocol version",
	},
}

// TestTransportChecksApplyToTasksMethods pins that every transport
// rejection the SDK performs also rejects a tasks/* request. Before
// the extension registered through the SDK, tasks/* was served inside
// an HTTP wrapper that returned before the SDK handler ran, so none of
// these checks applied to it.
func TestTransportChecksApplyToTasksMethods(t *testing.T) {
	ts := newAttachedServer(t)

	probes := map[string]rawRequest{
		"tasks method": {
			body: tasksBody(1, tasks.MethodGet, "task_x", true, ""),
			hdr:  tasksHeaders(tasks.MethodGet, "task_x"),
		},
		"standard method": {
			body: callToolBody(1, false, ""),
			hdr:  callToolHeaders(nil),
		},
	}

	for _, rej := range transportRejections {
		for probeName, probe := range probes {
			probe.mutate = rej.mutate
			status, body := send(t, ts.URL, probe)
			if status != rej.wantStatus {
				t.Errorf("%s / %s: status = %d, want %d (body %q)",
					rej.name, probeName, status, rej.wantStatus, body)
			}
			if !strings.Contains(body, rej.wantBody) {
				t.Errorf("%s / %s: body = %q, want it to contain %q",
					rej.name, probeName, body, rej.wantBody)
			}
		}
	}
}

// TestBodyCapAppliesToBothPaths pins the SDK's MaxRequestBodyBytes on
// both a tasks/* request and a standard one. The cap is enforced by
// http.MaxBytesReader during the read (see ServeHTTP), so an oversized
// body is rejected without ever being fully allocated — the wrapper's
// unbounded io.ReadAll defeated that for every request, tasks or not.
func TestBodyCapAppliesToBothPaths(t *testing.T) {
	const cap = 1 << 10

	srv := mcp.NewServer(&mcp.Implementation{Name: "tasks-test", Version: "0.0.1"}, &mcp.ServerOptions{})
	ext := tasks.New(nil)
	if err := ext.Attach(srv); err != nil {
		t.Fatalf("attach: %v", err)
	}
	srv.AddTool(&mcp.Tool{
		Name:        "op",
		Description: "test operation",
		InputSchema: map[string]any{"type": "object"},
	}, func(context.Context, *mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		return textResult("x"), nil
	})
	ts := httptest.NewServer(mcp.NewStreamableHTTPHandler(
		func(*http.Request) *mcp.Server { return srv },
		&mcp.StreamableHTTPOptions{
			Stateless: true, JSONResponse: true, MaxRequestBodyBytes: cap,
		},
	))
	t.Cleanup(ts.Close)

	// A taskId padded well past the cap: oversized, but structurally
	// valid JSON, so only the cap can reject it.
	huge := strings.Repeat("a", 8*cap)

	cases := []struct {
		name string
		req  rawRequest
	}{
		{"tasks method", rawRequest{
			body: tasksBody(1, tasks.MethodGet, "task_"+huge, true, ""),
			hdr:  tasksHeaders(tasks.MethodGet, "task_"+huge),
		}},
		{"standard method", rawRequest{
			body: callToolBody(1, false, `"padding":"`+huge+`"`),
			hdr:  callToolHeaders(nil),
		}},
	}
	for _, tc := range cases {
		status, body := send(t, ts.URL, tc.req)
		if status != http.StatusRequestEntityTooLarge {
			t.Errorf("%s: status = %d, want 413 (body %q)", tc.name, status, body)
		}
		if !strings.Contains(body, "request body exceeds") {
			t.Errorf("%s: body = %q, want it to report the cap", tc.name, body)
		}
	}
}

// TestNonTasksRequestsMatchBareSDK is the differential test: for every
// case below — successes and rejections alike — a server with the
// extension attached must answer a non-tasks request byte-identically
// to a bare SDK server. Attaching the extension must be invisible to
// everything outside the "tasks/" prefix.
func TestNonTasksRequestsMatchBareSDK(t *testing.T) {
	attached := newAttachedServer(t)
	bare := newBareServer(t)

	base := rawRequest{body: callToolBody(1, false, ""), hdr: callToolHeaders(nil)}

	cases := []struct {
		name   string
		mutate func(*http.Request)
	}{
		{"success", nil},
	}
	for _, rej := range transportRejections {
		cases = append(cases, struct {
			name   string
			mutate func(*http.Request)
		}{rej.name, rej.mutate})
	}

	for _, tc := range cases {
		probe := base
		probe.mutate = tc.mutate

		aStatus, aBody := send(t, attached.URL, probe)
		bStatus, bBody := send(t, bare.URL, probe)

		if aStatus != bStatus {
			t.Errorf("%s: attached status %d, bare status %d", tc.name, aStatus, bStatus)
		}
		if aBody != bBody {
			t.Errorf("%s: bodies differ:\n  attached: %s\n      bare: %s", tc.name, aBody, bBody)
		}
	}
}

// TestTasksMethodsUnreachableWithoutTransportChecks pins the security
// property directly: a request that fails a transport check must never
// reach task state. A rejected tasks/cancel leaves a live task
// untouched, proving the rejection happened before dispatch.
func TestTasksMethodsUnreachableWithoutTransportChecks(t *testing.T) {
	release := make(chan struct{})
	var ext *tasks.Extension
	ts, ext := newHarness(t, nil,
		taskToolHandlerVar(&ext, func(ctx context.Context, _ *tasks.Handle) (*mcp.CallToolResult, error) {
			select {
			case <-release:
			case <-ctx.Done():
			}
			return textResult("x"), nil
		}))
	defer close(release)

	taskID := createTask(t, ts, nil)["taskId"].(string)

	for _, rej := range transportRejections {
		status, _ := send(t, ts.URL, rawRequest{
			body:   tasksBody(2, tasks.MethodCancel, taskID, true, ""),
			hdr:    tasksHeaders(tasks.MethodCancel, taskID),
			mutate: rej.mutate,
		})
		if status == http.StatusOK {
			t.Errorf("%s: tasks/cancel was served (status 200); want rejected", rej.name)
		}
		// The task must be untouched: the cancel never reached it.
		if res := resultMap(t, getTask(t, ts, taskID, nil)); res["status"] != "working" {
			t.Fatalf("%s: task status = %v after a rejected cancel, want working",
				rej.name, res["status"])
		}
	}
}
