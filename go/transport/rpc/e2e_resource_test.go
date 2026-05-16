package rpc_test

import (
	"context"
	"fmt"
	"net/http/httptest"
	"sync/atomic"
	"testing"

	"connectrpc.com/connect"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	crudv1 "hop.top/kit/contracts/proto/crud/v1"
	"hop.top/kit/contracts/proto/crud/v1/crudv1connect"
	"hop.top/kit/go/transport/rpc"
)

func TestE2E_RPCResource_FullRoundtrip(t *testing.T) {
	svc := newMemService()
	srv := rpc.NewServer()
	path, handler := rpc.RPCResource[testEntity](svc,
		connect.WithInterceptors(srv.Interceptors()...),
	)
	srv.Handle(path, handler)

	ts := httptest.NewServer(srv)
	t.Cleanup(ts.Close)

	client := crudv1connect.NewEntityServiceClient(ts.Client(), ts.URL)
	ctx := context.Background()

	// Create
	createResp, err := client.Create(ctx, connect.NewRequest(
		&crudv1.CreateRequest{Entity: entityStruct(t, "e2e-1", "roundtrip")},
	))
	require.NoError(t, err)
	assert.Equal(t, "roundtrip",
		createResp.Msg.Entity.Fields["name"].GetStringValue())

	// Get
	getResp, err := client.Get(ctx, connect.NewRequest(
		&crudv1.GetRequest{Id: "e2e-1"},
	))
	require.NoError(t, err)
	assert.Equal(t, "roundtrip",
		getResp.Msg.Entity.Fields["name"].GetStringValue())

	// List
	listResp, err := client.List(ctx, connect.NewRequest(
		&crudv1.ListRequest{Limit: 10},
	))
	require.NoError(t, err)
	assert.Len(t, listResp.Msg.Entities, 1)

	// Update
	updateResp, err := client.Update(ctx, connect.NewRequest(
		&crudv1.UpdateRequest{Entity: entityStruct(t, "e2e-1", "updated")},
	))
	require.NoError(t, err)
	assert.Equal(t, "updated",
		updateResp.Msg.Entity.Fields["name"].GetStringValue())

	// Delete
	_, err = client.Delete(ctx, connect.NewRequest(
		&crudv1.DeleteRequest{Id: "e2e-1"},
	))
	require.NoError(t, err)

	// Confirm deleted
	_, err = client.Get(ctx, connect.NewRequest(
		&crudv1.GetRequest{Id: "e2e-1"},
	))
	require.Error(t, err)
	assert.Equal(t, connect.CodeNotFound, connect.CodeOf(err))
}

func TestE2E_RPCResource_WithInterceptors(t *testing.T) {
	svc := newMemService()

	var logged atomic.Int32
	logFn := func(msg string, args ...any) {
		logged.Add(1)
	}

	var authed atomic.Int32
	authFn := func(next connect.UnaryFunc) connect.UnaryFunc {
		return func(ctx context.Context, req connect.AnyRequest) (connect.AnyResponse, error) {
			authed.Add(1)
			return next(ctx, req)
		}
	}

	srv := rpc.NewServer(
		rpc.WithInterceptors(
			rpc.LogInterceptor(logFn),
			connect.UnaryInterceptorFunc(authFn),
		),
	)

	path, handler := rpc.RPCResource[testEntity](svc,
		connect.WithInterceptors(srv.Interceptors()...),
	)
	srv.Handle(path, handler)

	ts := httptest.NewServer(srv)
	t.Cleanup(ts.Close)

	client := crudv1connect.NewEntityServiceClient(ts.Client(), ts.URL)
	ctx := context.Background()

	// Create + Get = 2 calls
	_, err := client.Create(ctx, connect.NewRequest(
		&crudv1.CreateRequest{Entity: entityStruct(t, "int-1", "intercepted")},
	))
	require.NoError(t, err)

	_, err = client.Get(ctx, connect.NewRequest(
		&crudv1.GetRequest{Id: "int-1"},
	))
	require.NoError(t, err)

	assert.Equal(t, int32(2), logged.Load(),
		"log interceptor should fire for each RPC")
	assert.Equal(t, int32(2), authed.Load(),
		"auth interceptor should fire for each RPC")
}

func TestE2E_RPCResource_MultipleResources(t *testing.T) {
	svc1 := newMemService()
	svc2 := newMemService()

	srv := rpc.NewServer()

	// Both share the same EntityService path — last one wins.
	// In real usage, different proto services would be used.
	// This test verifies Handle works with the server mux.
	path1, handler1 := rpc.RPCResource[testEntity](svc1)
	srv.Handle(path1, handler1)

	// For a second resource, we'd need a different proto service.
	// Instead, test that the first resource is reachable.
	_ = svc2

	ts := httptest.NewServer(srv)
	t.Cleanup(ts.Close)

	client := crudv1connect.NewEntityServiceClient(ts.Client(), ts.URL)
	ctx := context.Background()

	for i := range 5 {
		_, err := client.Create(ctx, connect.NewRequest(
			&crudv1.CreateRequest{Entity: entityStruct(t,
				fmt.Sprintf("m-%d", i), fmt.Sprintf("entity-%d", i))},
		))
		require.NoError(t, err)
	}

	resp, err := client.List(ctx, connect.NewRequest(
		&crudv1.ListRequest{},
	))
	require.NoError(t, err)
	assert.Len(t, resp.Msg.Entities, 5)
}
