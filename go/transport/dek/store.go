package dek

import (
	"context"
	"encoding/json"
	"sort"
	"sync"
)

// Outcome is what an apply answered, kept so the same key replays it.
type Outcome struct {
	Body json.RawMessage `json:"body"`
}

// StoredPlan is a plan with its owner and what has been applied.
// It is plain JSON, so a persistent [PlanStore] can write it as is.
type StoredPlan struct {
	Plan Plan `json:"plan"`
	// Owner is the principal that planned it; only the owner applies.
	Owner string `json:"owner"`
	// Applied holds the outcome of each idempotency key that applied
	// the plan. A plan applies once; a repeat with the same key
	// replays the outcome.
	Applied map[string]Outcome `json:"applied,omitempty"`
}

// PlanStore keeps plans between a dry run and its apply. The default
// is in memory and lost on restart; a tool that must survive one
// supplies its own through [Config.Plans].
type PlanStore interface {
	// Put stores or replaces a plan by its id.
	Put(ctx context.Context, p StoredPlan) error
	// Get returns the plan with that id.
	Get(ctx context.Context, id string) (StoredPlan, bool, error)
	// PlanForKey returns the id of the plan that owner applied with
	// key, if any: the index behind "key used on another plan".
	PlanForKey(ctx context.Context, owner, key string) (string, bool, error)
}

const maxMemoryPlans = 4096

type memoryPlans struct {
	mu    sync.Mutex
	plans map[string]StoredPlan
	order []string
	keys  map[[2]string]string
}

// NewMemoryPlanStore is the default [PlanStore]: in memory, the
// oldest plans dropped past a fixed bound.
func NewMemoryPlanStore() PlanStore {
	return &memoryPlans{plans: map[string]StoredPlan{}, keys: map[[2]string]string{}}
}

func (m *memoryPlans) Put(_ context.Context, p StoredPlan) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if _, ok := m.plans[p.Plan.ID]; !ok {
		m.order = append(m.order, p.Plan.ID)
	}
	m.plans[p.Plan.ID] = p
	for k := range p.Applied {
		m.keys[[2]string{p.Owner, k}] = p.Plan.ID
	}
	for len(m.order) > maxMemoryPlans {
		old := m.order[0]
		m.order = m.order[1:]
		if o, ok := m.plans[old]; ok {
			for k := range o.Applied {
				delete(m.keys, [2]string{o.Owner, k})
			}
			delete(m.plans, old)
		}
	}
	return nil
}

func (m *memoryPlans) Get(_ context.Context, id string) (StoredPlan, bool, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	p, ok := m.plans[id]
	return p, ok, nil
}

func (m *memoryPlans) PlanForKey(_ context.Context, owner, key string) (string, bool, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	id, ok := m.keys[[2]string{owner, key}]
	return id, ok, nil
}

// JobStore keeps jobs by owner. The default is in memory.
type JobStore interface {
	Put(ctx context.Context, owner string, j Job) error
	Get(ctx context.Context, owner, id string) (Job, bool, error)
	// Active lists the owner's jobs that have not reached a final
	// status.
	Active(ctx context.Context, owner string) ([]Job, error)
}

const maxMemoryJobs = 1024

type memoryJobs struct {
	mu    sync.Mutex
	jobs  map[string]storedJob
	order []string
}

type storedJob struct {
	owner string
	job   Job
}

// NewMemoryJobStore is the default [JobStore]: in memory, the oldest
// finished jobs dropped past a fixed bound.
func NewMemoryJobStore() JobStore { return &memoryJobs{jobs: map[string]storedJob{}} }

func (m *memoryJobs) Put(_ context.Context, owner string, j Job) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if _, ok := m.jobs[j.ID]; !ok {
		m.order = append(m.order, j.ID)
	}
	m.jobs[j.ID] = storedJob{owner, j}
	if len(m.order) > maxMemoryJobs {
		keep := m.order[:0]
		drop := len(m.order) - maxMemoryJobs
		for _, id := range m.order {
			if drop > 0 && m.jobs[id].job.final() {
				delete(m.jobs, id)
				drop--
				continue
			}
			keep = append(keep, id)
		}
		m.order = keep
	}
	return nil
}

func (m *memoryJobs) Get(_ context.Context, owner, id string) (Job, bool, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	s, ok := m.jobs[id]
	if !ok || s.owner != owner {
		return Job{}, false, nil
	}
	return s.job, true, nil
}

func (m *memoryJobs) Active(_ context.Context, owner string) ([]Job, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := []Job{}
	for _, s := range m.jobs {
		if s.owner == owner && !s.job.final() {
			out = append(out, s.job)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return out, nil
}
