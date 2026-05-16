package domain

import "context"

// Repository defines generic CRUD operations for any Entity type.
type Repository[T Entity] interface {
	// Create persists a new entity. Returns ErrConflict if the ID exists.
	Create(ctx context.Context, entity *T) error

	// Get retrieves an entity by ID. Returns ErrNotFound if absent.
	Get(ctx context.Context, id string) (*T, error)

	// List returns entities matching the query parameters.
	List(ctx context.Context, q Query) ([]T, error)

	// Update replaces an existing entity. Returns ErrNotFound if absent.
	Update(ctx context.Context, entity *T) error

	// Delete removes an entity by ID. Returns ErrNotFound if absent.
	Delete(ctx context.Context, id string) error
}
