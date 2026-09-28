package cli

import (
	"context"
	"os"
	"sync/atomic"
	"time"

	"charm.land/log/v2"
	"golang.org/x/time/rate"

	kitlog "hop.top/kit/go/console/log"
	"hop.top/kit/go/transport/api"
)

// ServeHTTPRefusals is implemented by a [ServeObservability] that
// counts a refusal an HTTP listener makes before any request exists —
// a TLS handshake that fails, code [api.CodeTLSHandshake] — under the
// same series as the refusals its HTTP middleware records. kit's
// provider in hop.top/kit/go/transport/observability does; with a
// provider that does not, such refusals are logged and not counted.
//
// It is a separate interface so a provider written against
// ServeObservability before it existed keeps compiling.
type ServeHTTPRefusals interface {
	// RecordHTTPRefusal counts one refusal with code on service's
	// listener. It is called after Start, on the connection's
	// goroutine, and must not block.
	RecordHTTPRefusal(ctx context.Context, service, code string)
}

// Handshake failure log rate: a burst, then one line a second. A
// scanner or a client with the wrong certificate fails a handshake per
// connection; every failure is counted, only this many are logged,
// and the next line logged reports how many were not.
const (
	handshakeLogBurst = 5
	handshakeLogEvery = time.Second
)

// handshakeLog logs and counts one listener's failed TLS handshakes.
type handshakeLog struct {
	root       *Root
	svc        string
	logger     *log.Logger
	limit      *rate.Limiter
	suppressed atomic.Int64
}

func newHandshakeLog(r *Root, svc string, logger *log.Logger) *handshakeLog {
	return &handshakeLog{
		root:   r,
		svc:    svc,
		logger: logger,
		limit:  rate.NewLimiter(rate.Every(handshakeLogEvery), handshakeLogBurst),
	}
}

// failed is the listener's [api.HandshakeErrorLog] hook: count the
// failure, and log it at debug unless the rate is spent.
func (h *handshakeLog) failed(remoteAddr, reason string) {
	if c, ok := h.root.serveObs.(ServeHTTPRefusals); ok {
		c.RecordHTTPRefusal(context.Background(), h.svc, api.CodeTLSHandshake)
	}
	if !h.limit.Allow() {
		h.suppressed.Add(1)
		return
	}
	kv := []any{"service", h.svc, "remote", remoteAddr, "reason", reason}
	if n := h.suppressed.Swap(0); n > 0 {
		kv = append(kv, "suppressed", n)
	}
	h.logger.Debug("tls: handshake failed", kv...)
}

// serveListenerLogger is the logger a kit HTTP listener reports what
// happens below the HTTP plane with — failed handshakes, certificate
// reloads — at the verbosity -V selects, on the command's stderr.
func serveListenerLogger(r *Root) *log.Logger {
	if r == nil || r.Viper == nil {
		return log.New(os.Stderr)
	}
	l := kitlog.WithVerbose(r.Viper, r.VerboseCount())
	if r.Cmd != nil {
		l.SetOutput(r.Cmd.ErrOrStderr())
	}
	return l
}
