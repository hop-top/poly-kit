package llm_test

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"hop.top/kit/go/ai/llm"
)

// ---------------------------------------------------------------------------
// InlineSource
// ---------------------------------------------------------------------------

func TestInlineSource_URL(t *testing.T) {
	src := llm.InlineSource([]byte("hello"), "text/plain")
	if got := src.URL(); got != "" {
		t.Fatalf("URL() = %q; want empty", got)
	}
}

func TestInlineSource_MimeType(t *testing.T) {
	src := llm.InlineSource([]byte("hello"), "image/png")
	if got := src.MimeType(); got != "image/png" {
		t.Fatalf("MimeType() = %q; want image/png", got)
	}
}

func TestInlineSource_Reader(t *testing.T) {
	payload := []byte("test payload")
	src := llm.InlineSource(payload, "application/octet-stream")

	rc, err := src.Reader(context.Background())
	if err != nil {
		t.Fatalf("Reader() error: %v", err)
	}
	defer rc.Close()

	got, err := io.ReadAll(rc)
	if err != nil {
		t.Fatalf("ReadAll error: %v", err)
	}
	if string(got) != string(payload) {
		t.Fatalf("got %q; want %q", got, payload)
	}
}

// ---------------------------------------------------------------------------
// URLSource
// ---------------------------------------------------------------------------

func TestURLSource_URL(t *testing.T) {
	u := "https://example.com/file.mp4"
	src := llm.URLSource(u)
	if got := src.URL(); got != u {
		t.Fatalf("URL() = %q; want %q", got, u)
	}
}

func TestURLSource_MimeType(t *testing.T) {
	src := llm.URLSource("https://example.com/file.mp4")
	if got := src.MimeType(); got != "" {
		t.Fatalf("MimeType() = %q; want empty", got)
	}
}

func TestURLSource_Reader(t *testing.T) {
	body := []byte("url content")
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write(body)
	}))
	defer srv.Close()

	src := llm.URLSource(srv.URL)
	rc, err := src.Reader(context.Background())
	if err != nil {
		t.Fatalf("Reader() error: %v", err)
	}
	defer rc.Close()

	got, err := io.ReadAll(rc)
	if err != nil {
		t.Fatalf("ReadAll error: %v", err)
	}
	if string(got) != string(body) {
		t.Fatalf("got %q; want %q", got, body)
	}
}

func TestURLSource_Reader_contextCancelled(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	src := llm.URLSource("http://192.0.2.1/unreachable")
	_, err := src.Reader(ctx)
	if err == nil {
		t.Fatal("expected error for canceled context")
	}
}

// ---------------------------------------------------------------------------
// FileSource
// ---------------------------------------------------------------------------

func TestFileSource_URL(t *testing.T) {
	src := llm.FileSource("/tmp/test.png")
	if got := src.URL(); got != "" {
		t.Fatalf("URL() = %q; want empty", got)
	}
}

func TestFileSource_MimeType(t *testing.T) {
	tests := []struct {
		path string
		want string
	}{
		{"/tmp/image.png", "image/png"},
		{"/tmp/audio.mp3", "audio/mpeg"},
		{"/tmp/video.mp4", "video/mp4"},
		{"/tmp/noext", ""},
	}
	for _, tt := range tests {
		src := llm.FileSource(tt.path)
		got := src.MimeType()
		if got != tt.want {
			t.Errorf("FileSource(%q).MimeType() = %q; want %q", tt.path, got, tt.want)
		}
	}
}

func TestFileSource_Reader(t *testing.T) {
	content := []byte("file content test")
	tmp := filepath.Join(t.TempDir(), "test.txt")
	if err := os.WriteFile(tmp, content, 0o600); err != nil {
		t.Fatalf("write temp file: %v", err)
	}

	src := llm.FileSource(tmp)
	rc, err := src.Reader(context.Background())
	if err != nil {
		t.Fatalf("Reader() error: %v", err)
	}
	defer rc.Close()

	got, err := io.ReadAll(rc)
	if err != nil {
		t.Fatalf("ReadAll error: %v", err)
	}
	if string(got) != string(content) {
		t.Fatalf("got %q; want %q", got, content)
	}
}

func TestFileSource_Reader_missing(t *testing.T) {
	src := llm.FileSource("/nonexistent/path/file.png")
	_, err := src.Reader(context.Background())
	if err == nil {
		t.Fatal("expected error for missing file")
	}
}
