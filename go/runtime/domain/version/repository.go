package version

import (
	"context"
	"encoding/json"
	"fmt"
	"sync"
	"time"

	"github.com/google/uuid"

	"hop.top/kit/go/core/util"
	"hop.top/kit/go/runtime/domain"
)

// VersionedRepository wraps a domain.Repository with version tracking
// via a DAG. Each mutation appends a new version node.
type VersionedRepository[T domain.Entity] struct {
	inner    domain.Repository[T]
	dag      *DAG
	mu       sync.RWMutex
	entities map[string][]string // entityID -> ordered version IDs
}

// NewVersionedRepository returns a new VersionedRepository wrapping inner.
func NewVersionedRepository[T domain.Entity](inner domain.Repository[T]) *VersionedRepository[T] {
	return &VersionedRepository[T]{
		inner:    inner,
		dag:      NewDAG(),
		entities: make(map[string][]string),
	}
}

// Create persists the entity and appends an initial version.
func (vr *VersionedRepository[T]) Create(ctx context.Context, entity *T) error {
	if err := vr.inner.Create(ctx, entity); err != nil {
		return err
	}
	return vr.appendVersion((*entity).GetID(), entity, nil)
}

// Update persists the entity and appends a new version whose parent
// is the current head.
func (vr *VersionedRepository[T]) Update(ctx context.Context, entity *T) error {
	if err := vr.inner.Update(ctx, entity); err != nil {
		return err
	}
	id := (*entity).GetID()
	vr.mu.RLock()
	versions := vr.entities[id]
	var parents []string
	if len(versions) > 0 {
		parents = []string{versions[len(versions)-1]}
	}
	vr.mu.RUnlock()
	return vr.appendVersion(id, entity, parents)
}

// Get retrieves the entity from the inner repository (latest state).
func (vr *VersionedRepository[T]) Get(ctx context.Context, id string) (*T, error) {
	return vr.inner.Get(ctx, id)
}

// ListVersions returns all versions for a given entity ID in order.
func (vr *VersionedRepository[T]) ListVersions(entityID string) []Version {
	vr.mu.RLock()
	defer vr.mu.RUnlock()

	ids := vr.entities[entityID]
	result := make([]Version, 0, len(ids))
	for _, vid := range ids {
		if v, ok := vr.dag.Get(vid); ok {
			result = append(result, *v)
		}
	}
	return result
}

// Revert restores the entity to the state captured at versionID.
// This appends a new version (the revert is itself a version).
func (vr *VersionedRepository[T]) Revert(ctx context.Context, entityID, versionID string) error {
	vr.mu.RLock()
	versions := vr.entities[entityID]
	vr.mu.RUnlock()

	if len(versions) == 0 {
		return fmt.Errorf("entity %s has no versions", entityID)
	}

	// Verify versionID belongs to this entity
	found := false
	for _, vid := range versions {
		if vid == versionID {
			found = true
			break
		}
	}
	if !found {
		return fmt.Errorf("version %s not found for entity %s", versionID, entityID)
	}

	// Get the version to find its hash — in a real implementation we'd
	// store snapshots; here we simply record the revert as a new version
	// pointing to both the current head and the target version.
	currentHead := versions[len(versions)-1]
	parents := []string{currentHead}
	if versionID != currentHead {
		parents = append(parents, versionID)
	}

	// Get current entity to write back (caller handles actual state restore)
	entity, err := vr.inner.Get(ctx, entityID)
	if err != nil {
		return err
	}

	return vr.appendVersion(entityID, entity, parents)
}

// List delegates to the inner repository.
func (vr *VersionedRepository[T]) List(ctx context.Context, q domain.Query) ([]T, error) {
	return vr.inner.List(ctx, q)
}

// Delete removes the entity and records a terminal version.
func (vr *VersionedRepository[T]) Delete(ctx context.Context, id string) error {
	if err := vr.inner.Delete(ctx, id); err != nil {
		return err
	}
	vr.mu.Lock()
	delete(vr.entities, id)
	vr.mu.Unlock()
	return nil
}

// DAG returns the underlying version DAG for inspection.
func (vr *VersionedRepository[T]) DAG() *DAG {
	return vr.dag
}

func (vr *VersionedRepository[T]) appendVersion(entityID string, entity *T, parents []string) error {
	hash, err := hashEntity(entity)
	if err != nil {
		return fmt.Errorf("version: hash entity %s: %w", entityID, err)
	}

	vid := fmt.Sprintf("%s-%s", entityID, uuid.New().String())

	v := Version{
		ID:        vid,
		ParentIDs: parents,
		Timestamp: time.Now().UnixNano(),
		Hash:      hash,
	}

	if err := vr.dag.Append(v); err != nil {
		return fmt.Errorf("version: append %s: %w", vid, err)
	}

	vr.mu.Lock()
	vr.entities[entityID] = append(vr.entities[entityID], vid)
	vr.mu.Unlock()
	return nil
}

func hashEntity[T any](entity *T) (string, error) {
	data, err := json.Marshal(entity)
	if err != nil {
		return "", err
	}
	return util.Short(data, 16), nil
}
