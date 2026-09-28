package api

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"time"
)

// StreamSuffix is the path segment that turns a projected command
// route into its streaming twin:
//
//	/v1/commands/widget/add → /v1/commands/widget/add/stream
//
// It cannot collide with a projected command. Only leaves are
// projected, and a leaf has no children, so no command lives at
// <leaf>/stream.
const StreamSuffix = "/stream"

// DefaultStreamHeartbeat is the keep-alive interval for a stream
// whose [ProjectionConfig.StreamHeartbeat] is zero. It is short
// enough to stay under the idle timeouts of common proxies.
const DefaultStreamHeartbeat = 15 * time.Second

// Server-sent event names. They are the frame vocabulary the
// cmdsurface SSE surface already speaks, so a client written for one
// reads the other.
const (
	// SSEEventEvent carries one [CommandEvent]: an output line or a
	// progress payload.
	SSEEventEvent = "event"
	// SSEEventResult is the terminal frame of a command that ran to
	// completion, carrying a [StreamResult].
	SSEEventResult = "result"
	// SSEEventError is the terminal frame of a stream whose run
	// failed without a result, carrying an [APIError].
	SSEEventError = "error"
)

// CodeShuttingDown is the terminal error frame's code for a stream
// the server ended because it is stopping. Its status is 503: the
// same call succeeds against a server that is up.
const CodeShuttingDown = "shutting_down"

// StreamRouteFor returns the streaming route for a command path.
func StreamRouteFor(path []string) string { return RouteFor(path) + StreamSuffix }

// StreamOperationIDFor returns the OpenAPI operationId of a command's
// streaming operation.
//
//	["widget","add"] → "stream_commands_widget_add"
//
// It is a prefix rather than a suffix because every command operation
// starts with "commands_": a suffix would let a command named
// add-stream collide with add's stream.
func StreamOperationIDFor(path []string) string { return "stream_" + OperationIDFor(path) }

// StreamRoute returns the streaming route for this descriptor.
func (d CommandDescriptor) StreamRoute() string { return StreamRouteFor(d.Path) }

// CommandEvent is one frame of a streamed command's output. It
// mirrors the cmdsurface Event: Kind is the channel ("stdout",
// "stderr", "progress"), Data the line or payload, At when it was
// produced.
type CommandEvent struct {
	Kind string    `json:"kind"`
	Data any       `json:"data,omitempty"`
	At   time.Time `json:"at"`
}

// StreamResult is the terminal "result" frame: the command's result,
// and the HTTP status the request/reply route would have answered
// with, so a streaming caller classifies the outcome the same way.
type StreamResult struct {
	// Status is [StatusForExitCode] of ExitCode.
	Status int `json:"status"`
	CommandResult
}

// CommandStreamer is implemented by a [CommandExecutor] that can
// also stream. The projection mounts streaming routes only when the
// executor implements it.
//
// Streaming is split in two so a refusal is answered before the
// response commits to a stream: OpenStream applies every gate
// Execute applies — returning the same errors, which map to the same
// statuses — and runs nothing; the returned CommandStream runs the
// admitted command.
type CommandStreamer interface {
	OpenStream(ctx context.Context, req CommandRequest) (CommandStream, error)
}

// CommandStream is one admitted, not yet run, streaming invocation.
type CommandStream interface {
	// Run executes the command, sending each output event on
	// events, and returns the final result when it ends. It must
	// honor ctx, which is canceled when the client disconnects, and
	// must not close events.
	Run(ctx context.Context, events chan<- CommandEvent) (CommandResult, error)
}

// WriteSSEFrame writes one "event: <name>" frame whose data line is
// data encoded as JSON. The caller flushes.
func WriteSSEFrame(w io.Writer, name string, data any) error {
	payload, err := json.Marshal(data)
	if err != nil {
		return err
	}
	_, err = fmt.Fprintf(w, "event: %s\ndata: %s\n\n", name, payload)
	return err
}

// WriteSSEComment writes the ": ping" keep-alive comment frame. SSE
// clients ignore comment lines. The caller flushes.
func WriteSSEComment(w io.Writer) error {
	_, err := io.WriteString(w, ": ping\n\n")
	return err
}

// streams reports whether the projection mounts streaming routes.
func (cfg ProjectionConfig) streams() bool {
	_, ok := cfg.Executor.(CommandStreamer)
	return ok
}

// heartbeat returns the configured keep-alive interval.
func (cfg ProjectionConfig) heartbeat() time.Duration {
	if cfg.StreamHeartbeat > 0 {
		return cfg.StreamHeartbeat
	}
	return DefaultStreamHeartbeat
}

// streamHandler returns the http.HandlerFunc for one command's
// streaming route.
//
// Everything the transport and the bridge refuse is answered before
// the response commits to a stream, with the request/reply route's
// status and JSON body: a malformed request (400), a failed
// authentication (401, from the router's middleware), a withheld
// command (404), the destructive ceiling and the permission gate
// (403). Once admitted, the stream opens at once and the command's
// outcome travels in the terminal frame — every outcome, including
// the command's own confirmation refusal, which kit deliberately
// leaves to the command: its exit code exists only once it has run,
// and the frame's status is the one the request/reply route would
// have answered.
//
// A stream also ends when stopping closes, so a draining server is
// not held open by a command that has no end.
func streamHandler(
	st CommandStreamer, d CommandDescriptor, heartbeat time.Duration, stopping <-chan struct{},
) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		req, err := decodeCommandRequest(r, d)
		if err != nil {
			Error(w, http.StatusBadRequest, &APIError{
				Status:  http.StatusBadRequest,
				Code:    "bad_request",
				Message: err.Error(),
			})
			return
		}
		req.Meta = RequestMetaFrom(r)

		// Refuse before admission: a stream that cannot be flushed
		// would buffer until the command ends, which is a
		// request/reply call pretending otherwise.
		if !canFlush(w) {
			Error(w, http.StatusInternalServerError, &APIError{
				Status:  http.StatusInternalServerError,
				Code:    "server_error",
				Message: "streaming not supported: the response writer cannot flush",
			})
			return
		}

		ctx, cancel := context.WithCancel(r.Context())
		defer cancel()

		stream, err := st.OpenStream(ctx, req)
		if err != nil {
			writeProjectionError(w, d, err)
			return
		}

		// The server's write deadline is sized for request/reply, and
		// a stream outlives it by design: once it expires the next
		// frame fails and the stream is cut. It is lifted for this
		// response only; every other route keeps it. (The read
		// deadline needs nothing: net/http clears it once the request
		// body is consumed, before it starts watching for the client
		// to go away.)
		rc := http.NewResponseController(w)
		_ = rc.SetWriteDeadline(time.Time{})

		events := make(chan CommandEvent, 16)
		type outcome struct {
			res CommandResult
			err error
		}
		done := make(chan outcome, 1)
		go func() {
			res, err := stream.Run(ctx, events)
			close(events)
			done <- outcome{res, err}
		}()

		// abandon stops the command and waits for it, draining
		// events so Run is never blocked on a send.
		abandon := func() {
			cancel()
			for range events {
			}
			<-done
		}

		sw := &sseWriter{w: w, rc: rc}
		if sw.open() != nil {
			abandon()
			return
		}
		tick := time.NewTicker(heartbeat)
		defer tick.Stop()

	loop:
		for {
			select {
			case <-r.Context().Done():
				abandon()
				return
			case <-stopping:
				abandon()
				_ = sw.frame(SSEEventError, &APIError{
					Status:  http.StatusServiceUnavailable,
					Code:    CodeShuttingDown,
					Message: "the server is shutting down",
				})
				return
			case <-tick.C:
				if sw.comment() != nil {
					abandon()
					return
				}
			case ev, ok := <-events:
				if !ok {
					break loop
				}
				if sw.frame(SSEEventEvent, ev) != nil {
					abandon()
					return
				}
			}
		}

		out := <-done
		if r.Context().Err() != nil {
			// The client is gone; there is no one to tell.
			return
		}
		if out.err != nil {
			_ = sw.frame(SSEEventError, projectionError(d, out.err))
			return
		}
		_ = sw.frame(SSEEventResult, StreamResult{
			Status:        StatusForExitCode(out.res.ExitCode),
			CommandResult: out.res,
		})
	}
}

// sseWriter writes a text/event-stream response, flushing every
// frame.
type sseWriter struct {
	w  http.ResponseWriter
	rc *http.ResponseController
}

func (s *sseWriter) open() error {
	h := s.w.Header()
	// Set, not Add: a content-type middleware may already have
	// defaulted the header to JSON.
	h.Set("Content-Type", "text/event-stream")
	h.Set("Cache-Control", "no-cache")
	// Proxies that buffer responses (nginx) would hold frames back.
	h.Set("X-Accel-Buffering", "no")
	s.w.WriteHeader(http.StatusOK)
	return s.rc.Flush()
}

func (s *sseWriter) frame(name string, data any) error {
	if err := WriteSSEFrame(s.w, name, data); err != nil {
		return err
	}
	return s.rc.Flush()
}

func (s *sseWriter) comment() error {
	if err := WriteSSEComment(s.w); err != nil {
		return err
	}
	return s.rc.Flush()
}

// canFlush reports whether w, or a writer it wraps, can flush. It
// follows the Unwrap chain [http.ResponseController] follows.
func canFlush(w http.ResponseWriter) bool {
	for w != nil {
		if _, ok := w.(http.Flusher); ok {
			return true
		}
		u, ok := w.(interface{ Unwrap() http.ResponseWriter })
		if !ok {
			return false
		}
		w = u.Unwrap()
	}
	return false
}
