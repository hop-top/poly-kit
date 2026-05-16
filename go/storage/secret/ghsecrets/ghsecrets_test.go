package ghsecrets

import (
	"context"
	"errors"
	"testing"

	"hop.top/kit/go/storage/secret"
)

func TestGetFallsBackToEnv(t *testing.T) {
	t.Setenv("MY_TEST_SECRET", "abc123")

	s := New("")
	got, err := s.Get(context.Background(), "MY_TEST_SECRET")
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if string(got.Value) != "abc123" {
		t.Errorf("Get value: got %q, want %q", got.Value, "abc123")
	}
}

func TestGetMissingReturnsSentinel(t *testing.T) {
	s := New("")
	_, err := s.Get(context.Background(), "DEFINITELY_NOT_SET_xyzzy")
	if !errors.Is(err, secret.ErrNotFound) {
		t.Errorf("expected ErrNotFound, got %v", err)
	}
}

func TestStoreImplementsMutableInterface(t *testing.T) {
	var _ secret.Store = (*Store)(nil)
	var _ secret.MutableStore = (*Store)(nil)
}
