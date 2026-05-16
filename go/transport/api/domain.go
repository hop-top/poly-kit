package api

import (
	"context"

	"hop.top/kit/go/runtime/domain"
)

// Entity is an alias for domain.Entity.
type Entity = domain.Entity

// Query is an alias for domain.Query.
type Query = domain.Query

// Service is the generic CRUD service interface that ResourceRouter
// operates on. domain.Service is a concrete struct (not an interface),
// so this interface is kept for HTTP-layer decoupling: callers can
// supply any implementation without importing domain internals.
type Service[T Entity] interface {
	Create(ctx context.Context, entity T) (T, error)
	Get(ctx context.Context, id string) (T, error)
	List(ctx context.Context, q Query) ([]T, error)
	Update(ctx context.Context, entity T) (T, error)
	Delete(ctx context.Context, id string) error
}
