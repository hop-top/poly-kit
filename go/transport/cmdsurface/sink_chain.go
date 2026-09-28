package cmdsurface

import (
	"context"
	"encoding/json"
	"errors"
	"time"

	"hop.top/kit/go/security"
)

// ChainSink appends one record per invocation to a tamper-evident,
// hash-chained audit log ([security.AuditLog]): editing, deleting, or
// reordering a record after the fact breaks the chain, and
// [security.VerifyAuditLog] reports where.
//
// It records who ran what and the verdict — the same fields as
// [FileSink] plus the command's args and flags — and never the
// command's output, so the audit pipeline does not scan Stdout, Stderr
// or Data on its behalf. Reached through [SinkSet.Emit], as every
// bridge audit is, the record is the redacted one: a secret flag's
// value is on disk as ***REDACTED***, never in clear.
//
// Safe for concurrent use: appends are serialized by the log, so
// several services sharing one ChainSink produce one chain. Caller owns
// the log's lifecycle (open/close).
type ChainSink struct {
	// Log receives the records. Required.
	Log *security.AuditLog
}

// chainRecord is the payload of one audit-chain record, under the
// log's "rec" key.
type chainRecord struct {
	Path        string         `json:"path"`
	Surface     string         `json:"surface"`
	ExitCode    int            `json:"exit_code"`
	Error       string         `json:"error,omitempty"`
	Caller      string         `json:"caller,omitempty"`
	Tenant      string         `json:"tenant,omitempty"`
	RequestID   string         `json:"request_id,omitempty"`
	TraceID     string         `json:"trace_id,omitempty"`
	RequestedAt time.Time      `json:"requested_at,omitzero"`
	Args        []string       `json:"args,omitempty"`
	Flags       map[string]any `json:"flags,omitempty"`
}

// errChainSinkNoLog is returned by Emit on a ChainSink without a Log.
var errChainSinkNoLog = errors.New("cmdsurface: chain sink has no log")

// Emit appends one record for inv/res/err to the log.
func (c *ChainSink) Emit(_ context.Context, inv Invocation, res Result, err error) error {
	if c.Log == nil {
		return errChainSinkNoLog
	}
	rec := chainRecord{
		Path:        joinPath(inv.Path),
		Surface:     string(inv.Meta.Surface),
		ExitCode:    res.ExitCode,
		Caller:      inv.Meta.Caller,
		Tenant:      inv.Meta.Tenant,
		RequestID:   inv.Meta.RequestID,
		TraceID:     inv.Meta.TraceID,
		RequestedAt: inv.Meta.RequestedAt.UTC(),
		Args:        inv.Args,
		Flags:       inv.Flags,
	}
	if err != nil {
		rec.Error = err.Error()
	}
	payload, merr := json.Marshal(rec)
	if merr != nil {
		return merr
	}
	_, aerr := c.Log.Append(payload)
	return aerr
}

// auditIgnoresOutput marks the chain sink output-blind: it records no
// Stdout, Stderr or Data, so SinkSet.Emit need not redact them for it.
func (c *ChainSink) auditIgnoresOutput() bool { return true }
