package rpc_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"connectrpc.com/connect"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	crudv1 "hop.top/kit/contracts/proto/crud/v1"
	"hop.top/kit/contracts/proto/crud/v1/crudv1connect"
	"hop.top/kit/go/transport/api"
	"hop.top/kit/go/transport/rpc"
)

func TestRPCResourceCapsMessagesByDefault(t *testing.T) {
	client, _ := setupClient(t, newMemService())
	big := strings.Repeat("x", int(api.DefaultMaxBodyBytes))
	_, err := client.Create(context.Background(), connect.NewRequest(
		&crudv1.CreateRequest{Entity: entityStruct(t, "1", big)},
	))
	assert.Equal(t, connect.CodeResourceExhausted, connect.CodeOf(err), "%v", err)
}

func TestRPCResourceReadMaxBytesOptionWins(t *testing.T) {
	// An adopter's own connect.WithReadMaxBytes replaces the default.
	mux := http.NewServeMux()
	path, handler := rpc.RPCResource[testEntity](newMemService(), connect.WithReadMaxBytes(0))
	mux.Handle(path, handler)
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	client := crudv1connect.NewEntityServiceClient(srv.Client(), srv.URL)

	big := strings.Repeat("x", int(api.DefaultMaxBodyBytes))
	resp, err := client.Create(context.Background(), connect.NewRequest(
		&crudv1.CreateRequest{Entity: entityStruct(t, "1", big)},
	))
	require.NoError(t, err)
	assert.Len(t, resp.Msg.Entity.Fields["name"].GetStringValue(), len(big))
}
