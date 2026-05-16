package cmdsurface

import (
	"context"
	"encoding/json"
	"io"
	"sync"
	"time"
)

// FileSink appends JSON Lines (one per invocation) to W. Safe for
// concurrent use; calls are serialized by an internal mutex so
// records do not interleave.
//
// Caller owns W's lifecycle (open/close). W is any io.Writer: an
// *os.File, a buffered writer, a pipe, a test buffer.
type FileSink struct {
	// W receives the JSON-Lines stream. Required.
	W io.Writer
	// Format produces the bytes written for one invocation. If nil,
	// the default formatter is used (see DefaultFileSinkFormat). The
	// returned bytes are written verbatim and followed by '\n'; the
	// formatter must not include a trailing newline.
	Format func(inv Invocation, res Result, err error) ([]byte, error)

	mu sync.Mutex
}

// Emit appends one JSON-Lines record for inv/res/err. Returns the
// first non-nil error encountered (formatter, writer).
func (f *FileSink) Emit(_ context.Context, inv Invocation, res Result, err error) error {
	format := f.Format
	if format == nil {
		format = DefaultFileSinkFormat
	}
	line, ferr := format(inv, res, err)
	if ferr != nil {
		return ferr
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	if _, werr := f.W.Write(line); werr != nil {
		return werr
	}
	_, werr := f.W.Write(newline)
	return werr
}

// newline is shared across Emit calls to avoid allocating on each
// write.
var newline = []byte("\n")

// fileSinkRecord is the default JSON payload schema. Surfaces wanting
// richer envelopes can supply their own Format.
type fileSinkRecord struct {
	At       time.Time `json:"at"`
	Path     string    `json:"path"`
	Surface  string    `json:"surface"`
	ExitCode int       `json:"exit_code"`
	Error    string    `json:"error,omitempty"`
	TraceID  string    `json:"trace_id,omitempty"`
	Caller   string    `json:"caller,omitempty"`
}

// DefaultFileSinkFormat marshals inv/res/err to a fileSinkRecord
// JSON document. It is the formatter used when FileSink.Format is
// nil; exported so callers can wrap it.
func DefaultFileSinkFormat(inv Invocation, res Result, err error) ([]byte, error) {
	r := fileSinkRecord{
		At:       time.Now().UTC(),
		Path:     joinPath(inv.Path),
		Surface:  string(inv.Meta.Surface),
		ExitCode: res.ExitCode,
		TraceID:  inv.Meta.TraceID,
		Caller:   inv.Meta.Caller,
	}
	if err != nil {
		r.Error = err.Error()
	}
	return json.Marshal(r)
}
