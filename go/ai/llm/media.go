// Package llm — media source abstractions for multimodal inputs.
package llm

import (
	"context"
	"io"
	"mime"
	"os"
	"path/filepath"
)

// MediaSource is the common interface for binary media inputs.
//
// Implementations: [InlineSource], [URLSource], [FileSource].
type MediaSource interface {
	// Reader opens a read-only stream of the media bytes.
	Reader(ctx context.Context) (io.ReadCloser, error)
	// URL returns a non-empty string when the source is URL-backed.
	URL() string
	// MimeType returns the inferred MIME type, or empty string.
	MimeType() string
}

// ---------------------------------------------------------------------------
// InlineSource
// ---------------------------------------------------------------------------

type inlineSource struct {
	data     []byte
	mimeType string
}

// InlineSource wraps raw bytes and an explicit MIME type.
func InlineSource(data []byte, mimeType string) MediaSource {
	return &inlineSource{data: data, mimeType: mimeType}
}

func (s *inlineSource) Reader(_ context.Context) (io.ReadCloser, error) {
	return io.NopCloser(newBytesReader(s.data)), nil
}

func (s *inlineSource) URL() string      { return "" }
func (s *inlineSource) MimeType() string { return s.mimeType }

// ---------------------------------------------------------------------------
// URLSource
// ---------------------------------------------------------------------------

type urlSource struct {
	url      string
	maxBytes int64
}

// URLSourceOption configures a [URLSource].
type URLSourceOption func(*urlSource)

// MediaMaxBytes caps the download at n bytes (default
// [DefaultMaxMediaBytes]); a body over the cap fails with
// [llmerrors.ErrMediaTooLarge]. n <= 0 keeps the default.
func MediaMaxBytes(n int64) URLSourceOption {
	return func(s *urlSource) {
		if n > 0 {
			s.maxBytes = n
		}
	}
}

// URLSource provides a URL-backed source. Reader() downloads lazily.
//
// The download is guarded against server-side request forgery (see
// media_fetch.go): http or https only, public addresses only (checked
// on the address dialed, so DNS rebinding cannot redirect it), no
// redirects, no proxy, at most [DefaultMaxMediaBytes] (or
// [MediaMaxBytes]), 30s, and the --offline policy. Refusals match
// [ErrMediaURLRefused]. Adapters whose provider accepts a URL (OpenAI
// images, Anthropic images and PDFs) send the URL and do not download.
func URLSource(url string, opts ...URLSourceOption) MediaSource {
	s := &urlSource{url: url, maxBytes: DefaultMaxMediaBytes}
	for _, o := range opts {
		o(s)
	}
	return s
}

func (s *urlSource) URL() string      { return s.url }
func (s *urlSource) MimeType() string { return "" }

func (s *urlSource) Reader(ctx context.Context) (io.ReadCloser, error) {
	// The URL may be presigned: errors name it without query or userinfo.
	return fetchMedia(ctx, s.url, s.maxBytes)
}

// ---------------------------------------------------------------------------
// FileSource
// ---------------------------------------------------------------------------

type fileSource struct {
	path string
}

// FileSource reads a file from disk on Reader(); infers MIME from extension.
func FileSource(path string) MediaSource {
	return &fileSource{path: path}
}

func (s *fileSource) URL() string { return "" }

func (s *fileSource) MimeType() string {
	ext := filepath.Ext(s.path)
	if ext == "" {
		return ""
	}
	t := mime.TypeByExtension(ext)
	return t
}

func (s *fileSource) Reader(_ context.Context) (io.ReadCloser, error) {
	return os.Open(s.path)
}

// ---------------------------------------------------------------------------
// helpers
// ---------------------------------------------------------------------------

// newBytesReader creates an io.Reader from a byte slice.
// Using bytes.NewReader directly to avoid importing "bytes" elsewhere.
func newBytesReader(b []byte) io.Reader {
	return &bytesReader{buf: b}
}

type bytesReader struct {
	buf []byte
	pos int
}

func (r *bytesReader) Read(p []byte) (int, error) {
	if r.pos >= len(r.buf) {
		return 0, io.EOF
	}
	n := copy(p, r.buf[r.pos:])
	r.pos += n
	return n, nil
}
