package api

import (
	"mime"
	"net/http"
	"strings"

	"github.com/klauspost/compress/gzhttp"
)

// DefaultCompressMinBytes is the response size below which [Compress]
// leaves a body alone. A body that already fits one packet gains
// nothing on the wire from encoding and still costs CPU at both ends.
const DefaultCompressMinBytes = gzhttp.DefaultMinSize

// CompressOption configures [Compress].
type CompressOption func(*compressConfig)

type compressConfig struct {
	minBytes int
	filter   func(contentType string) bool
}

// WithCompressMinBytes sets the size threshold below which a response
// is sent unencoded. Zero or less compresses every eligible response
// regardless of size. The default is [DefaultCompressMinBytes].
func WithCompressMinBytes(n int) CompressOption {
	return func(c *compressConfig) { c.minBytes = max(n, 0) }
}

// WithCompressFilter replaces the content-type allowlist. fn receives
// the response Content-Type header verbatim, parameters included, and
// reports whether that body may be encoded. The default is
// [CompressibleContentType].
//
// An event stream is never encoded, whatever fn answers: it is
// recognized and passed through before the filter is consulted.
func WithCompressFilter(fn func(contentType string) bool) CompressOption {
	return func(c *compressConfig) {
		if fn != nil {
			c.filter = fn
		}
	}
}

// CompressibleContentType is the default allowlist for [Compress]:
// JSON, YAML and XML, including structured-suffix types such as
// application/problem+json and the OpenAPI media types, and every
// text/* type except text/event-stream.
//
// Types that are already compressed, binary, or streamed record by
// record (application/x-ndjson, application/grpc) are left alone.
func CompressibleContentType(contentType string) bool {
	mt, _, err := mime.ParseMediaType(contentType)
	if err != nil {
		return false
	}
	switch {
	case isEventStream(mt):
		return false
	case strings.HasPrefix(mt, "text/"):
		return true
	case mt == "application/json",
		mt == "application/yaml",
		mt == "application/x-yaml",
		mt == "application/xml",
		mt == "application/javascript":
		return true
	case strings.HasSuffix(mt, "+json"),
		strings.HasSuffix(mt, "+yaml"),
		strings.HasSuffix(mt, "+xml"):
		return true
	}
	return false
}

// Compress returns a middleware that encodes response bodies with zstd
// or gzip, whichever the client's Accept-Encoding ranks higher (zstd
// on a tie), and sends identity when it accepts neither.
//
// A response is encoded only when all of these hold:
//
//   - its Content-Type passes the allowlist ([CompressibleContentType]
//     unless [WithCompressFilter] replaces it);
//   - it is at least the threshold ([WithCompressMinBytes]) — known
//     from Content-Length or from what the handler has written;
//   - the handler did not set Content-Encoding (or Content-Range)
//     itself;
//   - the request is not HEAD. A HEAD response carries identity
//     headers, as nginx and most caches expect.
//
// Every response it considers carries Vary: Accept-Encoding, and an
// encoded response drops the handler's Content-Length (the length it
// had no longer describes the body) and Accept-Ranges.
//
// Some traffic is passed through entirely — no wrapping, no Vary:
//
//   - protocol upgrades (WebSocket) and CONNECT, whose connection is
//     hijacked rather than written;
//   - event streams: requests that Accept text/event-stream, and any
//     response whose Content-Type turns out to be text/event-stream.
//     Those are handed the underlying writer at the first write or
//     flush, so every flush — including one before the first event,
//     which sends the headers — reaches the client at once;
//   - Connect, gRPC and gRPC-Web requests, which negotiate their own
//     compression per message and must not be encoded twice.
//
// Streaming responses of an allowlisted type are not held back:
// Flush settles the encode-or-not decision early and flushes the
// encoder and the connection. The writer handed to the handler
// implements http.Flusher and Unwrap, so http.ResponseController
// reaches the connection's deadlines and hijacker.
func Compress(opts ...CompressOption) Middleware {
	cfg := compressConfig{
		minBytes: DefaultCompressMinBytes,
		filter:   CompressibleContentType,
	}
	for _, o := range opts {
		o(&cfg)
	}
	filter := func(ct string) bool {
		if mt, _, err := mime.ParseMediaType(ct); err == nil && isEventStream(mt) {
			return false
		}
		return cfg.filter(ct)
	}
	wrap, err := gzhttp.NewWrapper(
		gzhttp.MinSize(cfg.minBytes),
		gzhttp.ContentTypeFilter(filter),
	)
	if err != nil {
		// Unreachable: every setting above is in range by construction.
		panic("api: Compress: " + err.Error())
	}

	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if !compressEligible(r) {
				next.ServeHTTP(w, r)
				return
			}
			cw := &compressWriter{raw: w, filter: filter}
			wrap(http.HandlerFunc(func(ew http.ResponseWriter, r *http.Request) {
				cw.encoder = ew
				next.ServeHTTP(cw, r)
			})).ServeHTTP(w, r)
		})
	}
}

// compressEligible reports whether a request may reach the encoder at
// all; see [Compress] for what is passed through and why.
func compressEligible(r *http.Request) bool {
	if r.Method == http.MethodConnect || r.Header.Get("Upgrade") != "" {
		return false
	}
	for _, a := range r.Header.Values("Accept") {
		if strings.Contains(strings.ToLower(a), "text/event-stream") {
			return false
		}
	}
	if r.Header.Get("Connect-Protocol-Version") != "" {
		return false
	}
	ct := strings.ToLower(r.Header.Get("Content-Type"))
	if strings.HasPrefix(ct, "application/grpc") || strings.HasPrefix(ct, "application/connect+") {
		return false
	}
	// Connect unary GET carries its protocol marker in the query.
	if r.Method == http.MethodGet && r.URL.Query().Get("connect") != "" {
		return false
	}
	return true
}

func isEventStream(mediaType string) bool {
	return strings.EqualFold(mediaType, "text/event-stream")
}

// compressWriter defers the choice between the encoder and the
// underlying writer to the handler's first WriteHeader, Write or
// Flush, when the response headers are known. A response the encoder
// would never encode — an event stream, a type off the allowlist, one
// the handler encoded itself — goes straight to the underlying writer,
// so its flushes are never absorbed by the encoder's buffering.
type compressWriter struct {
	encoder http.ResponseWriter
	raw     http.ResponseWriter
	filter  func(string) bool
	target  http.ResponseWriter
}

func (c *compressWriter) Header() http.Header { return c.raw.Header() }

func (c *compressWriter) decide() http.ResponseWriter {
	if c.target != nil {
		return c.target
	}
	h := c.raw.Header()
	ct := h.Get("Content-Type")
	switch {
	case h.Get("Content-Encoding") != "",
		ct != "" && !c.filter(ct):
		c.target = c.raw
	default:
		// No Content-Type yet: the encoder sniffs one from the body
		// and applies the same filter to it.
		c.target = c.encoder
	}
	return c.target
}

func (c *compressWriter) WriteHeader(code int) {
	if code >= 100 && code < 200 {
		// Informational responses precede the real one and carry no
		// body; they do not settle anything.
		c.raw.WriteHeader(code)
		return
	}
	c.decide().WriteHeader(code)
}

func (c *compressWriter) Write(b []byte) (int, error) {
	return c.decide().Write(b)
}

// FlushError is the form http.ResponseController prefers; it reports
// a flush the underlying writer cannot perform.
func (c *compressWriter) FlushError() error {
	return http.NewResponseController(c.decide()).Flush()
}

// Flush implements http.Flusher.
func (c *compressWriter) Flush() { _ = c.FlushError() }

// Unwrap exposes the underlying writer to http.ResponseController for
// deadlines, full duplex and hijacking. Writing to it directly skips
// the encoder.
func (c *compressWriter) Unwrap() http.ResponseWriter { return c.raw }
