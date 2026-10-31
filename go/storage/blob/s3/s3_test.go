package s3

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/credentials"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	"github.com/aws/aws-sdk-go-v2/service/s3/types"

	"hop.top/kit/go/storage/blob"
)

// Compile-time interface assertion — runs without build tags.
var _ blob.Store = (*Store)(nil)

func TestInterfaceCompliance(t *testing.T) {
	// Intentionally empty; the var _ line above is the real check.
	t.Log("Store implements blob.Store")
}

func TestPrefixHandling(t *testing.T) {
	for _, prefix := range []string{"tool-data", "tool-data/", ""} {
		s := New(nil, "b", prefix)
		want := "tool-data"
		if prefix == "" {
			want = ""
		}
		if s.prefix != want {
			t.Fatalf("New(%q).prefix = %q, want %q", prefix, s.prefix, want)
		}
		full := s.key("a/b.txt")
		if got := s.callerKey(full); got != "a/b.txt" {
			t.Fatalf("prefix %q: callerKey(%q) = %q, want a/b.txt", prefix, full, got)
		}
	}
}

func TestListPrefixKeepsTrailingSlash(t *testing.T) {
	s := New(nil, "b", "tool-data")
	if got := s.listPrefix("logs/"); got != "tool-data/logs/" {
		t.Fatalf("listPrefix(logs/) = %q, want tool-data/logs/ (slash kept so logs-old/ is excluded)", got)
	}
	if got := New(nil, "b", "").listPrefix("logs/"); got != "logs/" {
		t.Fatalf("empty store prefix: got %q", got)
	}
}

// Stater is optional but s3 opts in (HeadObject).
var _ blob.Stater = (*Store)(nil)

func fakeS3(t *testing.T, h http.HandlerFunc) *Store {
	t.Helper()
	srv := httptest.NewServer(h)
	t.Cleanup(srv.Close)
	c := s3.New(s3.Options{
		Region:       "us-east-1",
		BaseEndpoint: aws.String(srv.URL),
		UsePathStyle: true,
		Credentials:  credentials.NewStaticCredentialsProvider("k", "s", ""),
	})
	return New(c, "b", "pre")
}

func TestStatMapsHeadObject(t *testing.T) {
	var gotPath string
	s := fakeS3(t, func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("Content-Length", "42")
		w.Header().Set("Last-Modified", "Mon, 02 Jan 2006 15:04:05 GMT")
	})
	o, err := s.Stat(context.Background(), "a/b.json")
	if err != nil {
		t.Fatal(err)
	}
	if gotPath != "/b/pre/a/b.json" {
		t.Fatalf("path = %q", gotPath)
	}
	want := blob.Object{Key: "a/b.json", Size: 42, ContentType: "application/json"}
	if o != want {
		t.Fatalf("Stat = %+v, want %+v", o, want)
	}
}

func TestStatMissingErrors(t *testing.T) {
	s := fakeS3(t, func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(http.StatusNotFound) })
	_, err := s.Stat(context.Background(), "nope")
	var nf *types.NotFound
	if !errors.As(err, &nf) {
		t.Fatalf("err = %v, want wrapped *types.NotFound", err)
	}
}
