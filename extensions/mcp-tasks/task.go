// Copyright 2026 The Model Context Protocol Authors. All rights reserved.
// Use of this source code is governed by the license
// that can be found in the LICENSE file.

package tasks

import (
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"time"

	"github.com/modelcontextprotocol/go-sdk/jsonrpc"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// ExtensionID is the identifier of the tasks extension
// (SEP-2663). Servers declare it under capabilities.extensions and
// clients declare it per request in the
// "io.modelcontextprotocol/clientCapabilities" _meta field.
const ExtensionID = "io.modelcontextprotocol/tasks"

// MinProtocolVersion is the earliest protocol version under which the
// extension is defined. SEP-2663 requires servers to treat clients on
// older protocol versions as non-declaring, even when they send the
// capability.
const MinProtocolVersion = "2026-06-30"

// CodeMissingClientCapability is the JSON-RPC error code SEP-2663
// mandates for requests that cannot be serviced without the client
// declaring the tasks extension ("Missing Required Client
// Capability"). Note that this is the SEP's code, not the -32021 code
// the core specification reserves via SEP-2575.
const CodeMissingClientCapability = -32003

// Method names owned by the extension. The "tasks/" prefix is
// reserved by SEP-2663; tasks/list and tasks/result deliberately do
// not exist and must answer -32601.
const (
	MethodGet    = "tasks/get"
	MethodUpdate = "tasks/update"
	MethodCancel = "tasks/cancel"
)

// Status is a task lifecycle status. completed, failed and cancelled
// are terminal; a task never leaves a terminal status.
type Status string

const (
	StatusWorking       Status = "working"
	StatusInputRequired Status = "input_required"
	StatusCompleted     Status = "completed"
	StatusFailed        Status = "failed"
	StatusCancelled     Status = "cancelled"
)

// terminal reports whether s is a terminal status.
func (s Status) terminal() bool {
	return s == StatusCompleted || s == StatusFailed || s == StatusCancelled
}

// Task is the wire task object (SEP-2663 "DetailedTask"): the base
// fields plus the status-specific ones, which marshal only when set —
// InputRequests for input_required, Result for completed, Error for
// failed. TTLMs is required on the wire and null means unlimited,
// hence the pointer without omitempty.
type Task struct {
	TaskID         string              `json:"taskId"`
	Status         Status              `json:"status"`
	StatusMessage  string              `json:"statusMessage,omitempty"`
	CreatedAt      time.Time           `json:"createdAt"`
	LastUpdatedAt  time.Time           `json:"lastUpdatedAt"`
	TTLMs          *int64              `json:"ttlMs"`
	PollIntervalMs int64               `json:"pollIntervalMs,omitempty"`
	InputRequests  mcp.InputRequestMap `json:"inputRequests,omitempty"`
	Result         json.RawMessage     `json:"result,omitempty"`
	Error          json.RawMessage     `json:"error,omitempty"`
}

// Record is what a Store persists per task: the wire Task plus the
// principal the task is bound to. Principal never reaches the wire —
// task responses marshal the embedded Task only — but stores must
// persist it, since task visibility is authorized against it on every
// tasks/* request.
type Record struct {
	Task
	Principal string `json:"principal,omitempty"`
}

// expired reports whether the record's TTL has elapsed at time now.
func (r *Record) expired(now time.Time) bool {
	if r.TTLMs == nil {
		return false
	}
	return now.After(r.CreatedAt.Add(time.Duration(*r.TTLMs) * time.Millisecond))
}

// createTaskResult is the SEP-2663 CreateTaskResult: Result & Task,
// flat, with resultType "task". It implements the SDK's Result
// interface via ResultBase so receiving middleware can return it in
// lieu of a CallToolResult and the SDK marshals it onto the wire.
type createTaskResult struct {
	mcp.ResultBase
	ResultType string `json:"resultType"`
	Task
}

// detailedTaskResult is the tasks/get result: Result & DetailedTask,
// flat, with resultType "complete" (the standard result shape for the
// tasks/get request).
type detailedTaskResult struct {
	ResultType string `json:"resultType"`
	Task
}

// emptyAckResult is the tasks/update and tasks/cancel result: an
// empty acknowledgement with resultType "complete".
type emptyAckResult struct {
	ResultType string `json:"resultType"`
}

// NewTaskID returns a fresh unguessable task identifier: 128 bits
// from crypto/rand, base64url-encoded. SEP-2663 allows task IDs to
// act as bearer tokens, so they must never be guessable or ordered.
func NewTaskID() string {
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		panic(fmt.Sprintf("tasks: crypto/rand unavailable: %v", err))
	}
	return "task_" + base64.RawURLEncoding.EncodeToString(b[:])
}

// MissingClientCapabilityError returns the -32003 JSON-RPC error
// SEP-2663 mandates when a request cannot be serviced without the
// client declaring the tasks extension. Hosts whose operations cannot
// run synchronously may return it from a tool handler for
// non-declaring clients; the extension's HTTP handler returns it for
// undeclared tasks/* requests.
func MissingClientCapabilityError() *jsonrpc.Error {
	return &jsonrpc.Error{
		Code:    CodeMissingClientCapability,
		Message: "Missing required client capability",
		Data:    json.RawMessage(requiredCapabilitiesJSON),
	}
}

// requiredCapabilitiesJSON is the error data shape identifying the
// tasks extension as the missing capability.
const requiredCapabilitiesJSON = `{"requiredCapabilities":{"extensions":{"` + ExtensionID + `":{}}}}`
