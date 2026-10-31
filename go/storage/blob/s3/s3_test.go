package s3

import (
	"testing"

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
