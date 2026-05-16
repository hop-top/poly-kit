// Package llm — media source abstractions for multimodal inputs.
package llm

import (
	"context"
	"fmt"
	"io"
	"mime"
	"net/http"
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
	url string
}

// URLSource provides a URL-backed source. Reader() downloads lazily.
func URLSource(url string) MediaSource {
	return &urlSource{url: url}
}

func (s *urlSource) URL() string      { return s.url }
func (s *urlSource) MimeType() string { return "" }

func (s *urlSource) Reader(ctx context.Context) (io.ReadCloser, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, s.url, nil)
	if err != nil {
		return nil, err
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, err
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		resp.Body.Close()
		return nil, fmt.Errorf("url source: unexpected status %d fetching %s", resp.StatusCode, s.url)
	}
	return resp.Body, nil
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
