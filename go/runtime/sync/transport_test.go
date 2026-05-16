package sync

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestMemoryTransport_Roundtrip(t *testing.T) {
	mt := NewMemoryTransport()
	ctx := context.Background()

	ts1 := Timestamp{Physical: 100, Logical: 1, NodeID: "n1"}
	ts2 := Timestamp{Physical: 200, Logical: 0, NodeID: "n1"}
	diffs := []Diff{
		{EntityID: "e1", Operation: OpCreate, Timestamp: ts1},
		{EntityID: "e2", Operation: OpUpdate, Timestamp: ts2},
	}

	require.NoError(t, mt.Push(ctx, diffs))

	// Pull since before first diff
	got, err := mt.Pull(ctx, Timestamp{Physical: 50, Logical: 0, NodeID: "n1"})
	require.NoError(t, err)
	assert.Len(t, got, 2)

	// Pull since after first diff
	got, err = mt.Pull(ctx, Timestamp{Physical: 100, Logical: 1, NodeID: "n1"})
	require.NoError(t, err)
	assert.Len(t, got, 1)
	assert.Equal(t, "e2", got[0].EntityID)
}

func TestMemoryTransport_Ping(t *testing.T) {
	mt := NewMemoryTransport()
	ctx := context.Background()

	assert.NoError(t, mt.Ping(ctx))
	mt.SetAlive(false)
	assert.Error(t, mt.Ping(ctx))
}

func TestHTTPTransport_Push(t *testing.T) {
	var received []Diff
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/sync/push" && r.Method == http.MethodPost {
			_ = json.NewDecoder(r.Body).Decode(&received)
			w.WriteHeader(http.StatusOK)
			return
		}
		w.WriteHeader(http.StatusNotFound)
	}))
	defer srv.Close()

	tr := NewHTTPTransport(srv.URL, WithAuthToken("secret"))
	ctx := context.Background()

	diffs := []Diff{{EntityID: "e1", Operation: OpCreate, Timestamp: Timestamp{Physical: 1, NodeID: "n1"}}}
	require.NoError(t, tr.Push(ctx, diffs))
	assert.Len(t, received, 1)
	assert.Equal(t, "e1", received[0].EntityID)
}

func TestHTTPTransport_Pull(t *testing.T) {
	diffs := []Diff{
		{EntityID: "e1", Operation: OpCreate, Timestamp: Timestamp{Physical: 100, NodeID: "n1"}},
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/sync/pull" && r.Method == http.MethodGet {
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(diffs)
			return
		}
		w.WriteHeader(http.StatusNotFound)
	}))
	defer srv.Close()

	tr := NewHTTPTransport(srv.URL)
	ctx := context.Background()

	got, err := tr.Pull(ctx, Timestamp{})
	require.NoError(t, err)
	assert.Len(t, got, 1)
	assert.Equal(t, "e1", got[0].EntityID)
}

func TestHTTPTransport_Ping(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/sync/ping" {
			w.WriteHeader(http.StatusOK)
			return
		}
		w.WriteHeader(http.StatusNotFound)
	}))
	defer srv.Close()

	tr := NewHTTPTransport(srv.URL)
	assert.NoError(t, tr.Ping(context.Background()))
}

func TestHTTPTransport_PushError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer srv.Close()

	tr := NewHTTPTransport(srv.URL)
	err := tr.Push(context.Background(), []Diff{{EntityID: "e1"}})
	assert.Error(t, err)
}
