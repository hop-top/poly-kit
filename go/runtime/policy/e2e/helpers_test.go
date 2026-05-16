// Helpers shared across story files. These are fixtures only — no
// shared mutable state — so each story still reads as a self-contained
// example. Keep additions to this file minimal; if a helper is only
// used by one story, inline it in that story's file.
package e2e_test

import (
	"context"

	"hop.top/kit/go/runtime/bus"
	"hop.top/kit/go/runtime/domain"
	"hop.top/kit/go/runtime/policy"
)

// busAdapter adapts bus.Bus to domain.EventPublisher. Adopters
// wiring kit/runtime/domain to kit/runtime/bus do exactly this.
type busAdapter struct{ b bus.Bus }

func (a *busAdapter) Publish(ctx context.Context, topic, source string, payload any) error {
	return a.b.Publish(ctx, bus.NewEvent(bus.Topic(topic), source, payload))
}

// staticPrincipal returns a PrincipalResolver that always returns p.
// Tests use this to avoid depending on $USER / $KIT_POLICY_ROLE env.
// Real adopters install a resolver that pulls from their auth layer.
func staticPrincipal(p policy.Principal) policy.PrincipalResolver {
	return func(_ context.Context) policy.Principal { return p }
}

// task is the fixture entity type used by entity-CRUD stories. It's
// deliberately tiny — adopters reading these tests should focus on
// the policy wiring, not the entity shape.
type task struct {
	ID     string
	Title  string
	Status string
}

// GetID satisfies domain.Entity.
func (t task) GetID() string { return t.ID }

// memTaskRepo is an in-memory domain.Repository[task]. Mutations are
// recorded so stories can assert "the entity was NOT persisted" after
// a veto.
type memTaskRepo struct {
	created []task
	updated []task
	deleted []string
}

func (r *memTaskRepo) Create(_ context.Context, e *task) error {
	r.created = append(r.created, *e)
	return nil
}

func (r *memTaskRepo) Get(_ context.Context, _ string) (*task, error) {
	return nil, nil
}

func (r *memTaskRepo) List(_ context.Context, _ domain.Query) ([]task, error) {
	return nil, nil
}

func (r *memTaskRepo) Update(_ context.Context, e *task) error {
	r.updated = append(r.updated, *e)
	return nil
}

func (r *memTaskRepo) Delete(_ context.Context, id string) error {
	r.deleted = append(r.deleted, id)
	return nil
}
