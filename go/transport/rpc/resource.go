package rpc

import (
	"context"
	"errors"
	"fmt"
	"net/http"

	"connectrpc.com/connect"
	"google.golang.org/protobuf/types/known/structpb"

	crudv1 "hop.top/kit/contracts/proto/crud/v1"
	"hop.top/kit/contracts/proto/crud/v1/crudv1connect"
	"hop.top/kit/go/runtime/domain"
	"hop.top/kit/go/transport/api"
)

// RPCResource bridges an api.Service[T] to a ConnectRPC
// EntityService handler. Returns the mount path and handler,
// matching the NewEntityServiceHandler signature.
func RPCResource[T api.Entity](
	svc api.Service[T],
	opts ...connect.HandlerOption,
) (string, http.Handler) {
	adapter := &serviceAdapter[T]{svc: svc}
	return crudv1connect.NewEntityServiceHandler(adapter, opts...)
}

// serviceAdapter implements crudv1connect.EntityServiceHandler
// by delegating to an api.Service[T].
type serviceAdapter[T api.Entity] struct {
	svc api.Service[T]
}

func (a *serviceAdapter[T]) Create(
	ctx context.Context,
	req *connect.Request[crudv1.CreateRequest],
) (*connect.Response[crudv1.CreateResponse], error) {
	entity, err := StructToEntity[T](req.Msg.GetEntity())
	if err != nil {
		return nil, connect.NewError(connect.CodeInvalidArgument, err)
	}
	created, err := a.svc.Create(ctx, entity)
	if err != nil {
		return nil, mapDomainError(err)
	}
	s, err := EntityToStruct(created)
	if err != nil {
		return nil, connect.NewError(connect.CodeInternal, err)
	}
	return connect.NewResponse(&crudv1.CreateResponse{Entity: s}), nil
}

func (a *serviceAdapter[T]) Get(
	ctx context.Context,
	req *connect.Request[crudv1.GetRequest],
) (*connect.Response[crudv1.GetResponse], error) {
	entity, err := a.svc.Get(ctx, req.Msg.GetId())
	if err != nil {
		return nil, mapDomainError(err)
	}
	s, err := EntityToStruct(entity)
	if err != nil {
		return nil, connect.NewError(connect.CodeInternal, err)
	}
	return connect.NewResponse(&crudv1.GetResponse{Entity: s}), nil
}

func (a *serviceAdapter[T]) List(
	ctx context.Context,
	req *connect.Request[crudv1.ListRequest],
) (*connect.Response[crudv1.ListResponse], error) {
	q := domain.Query{
		Limit:  int(req.Msg.GetLimit()),
		Offset: int(req.Msg.GetOffset()),
		Sort:   req.Msg.GetSort(),
		Search: req.Msg.GetSearch(),
	}
	items, err := a.svc.List(ctx, q)
	if err != nil {
		return nil, mapDomainError(err)
	}
	structs := make([]*structpb.Struct, len(items))
	for i, item := range items {
		s, err := EntityToStruct(item)
		if err != nil {
			return nil, connect.NewError(connect.CodeInternal, err)
		}
		structs[i] = s
	}
	return connect.NewResponse(&crudv1.ListResponse{Entities: structs}), nil
}

func (a *serviceAdapter[T]) Update(
	ctx context.Context,
	req *connect.Request[crudv1.UpdateRequest],
) (*connect.Response[crudv1.UpdateResponse], error) {
	entity, err := StructToEntity[T](req.Msg.GetEntity())
	if err != nil {
		return nil, connect.NewError(connect.CodeInvalidArgument, err)
	}
	updated, err := a.svc.Update(ctx, entity)
	if err != nil {
		return nil, mapDomainError(err)
	}
	s, err := EntityToStruct(updated)
	if err != nil {
		return nil, connect.NewError(connect.CodeInternal, err)
	}
	return connect.NewResponse(&crudv1.UpdateResponse{Entity: s}), nil
}

func (a *serviceAdapter[T]) Delete(
	ctx context.Context,
	req *connect.Request[crudv1.DeleteRequest],
) (*connect.Response[crudv1.DeleteResponse], error) {
	if err := a.svc.Delete(ctx, req.Msg.GetId()); err != nil {
		return nil, mapDomainError(err)
	}
	return connect.NewResponse(&crudv1.DeleteResponse{}), nil
}

// mapDomainError converts domain errors to Connect error codes.
func mapDomainError(err error) *connect.Error {
	switch {
	case errors.Is(err, domain.ErrNotFound):
		return connect.NewError(connect.CodeNotFound, err)
	case errors.Is(err, domain.ErrConflict):
		return connect.NewError(connect.CodeAlreadyExists, err)
	case errors.Is(err, domain.ErrValidation):
		return connect.NewError(connect.CodeInvalidArgument, err)
	case errors.Is(err, domain.ErrInvalidTransition):
		return connect.NewError(connect.CodeFailedPrecondition, err)
	default:
		return connect.NewError(connect.CodeInternal, fmt.Errorf("internal error"))
	}
}
