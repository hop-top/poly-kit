package ash

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"time"
)

// Multi-agent errors.
var (
	ErrRouterNotSet   = errors.New("ash: router not set on session")
	ErrChildNotFound  = errors.New("ash: child session not found")
	ErrAlreadyWatched = errors.New("ash: session already watched")
)

// Lifecycle event topics for multi-agent.
const (
	TopicSessionSpawned = "session.spawned"
	TopicChildDone      = "session.child.done"
)

// Option configures a Session during Spawn or NewSession.
type Option func(*Session)

// WithProvider sets the Provider on a session.
func WithProvider(p Provider) Option {
	return func(s *Session) { s.provider = p }
}

// WithStore overrides the inherited Store.
func WithStore(st Store) Option {
	return func(s *Session) { s.store = st }
}

// WithPublisher overrides the inherited Publisher.
func WithPublisher(p Publisher) Option {
	return func(s *Session) { s.pub = p }
}

// WithRouter sets the Router for inter-session messaging.
func WithRouter(r Router) Option {
	return func(s *Session) { s.router = r }
}

// WithMetadata sets metadata on a session.
func WithMetadata(m map[string]any) Option {
	return func(s *Session) { s.Metadata = m }
}

// NewSession creates a root session with options applied.
func NewSession(id string, opts ...Option) *Session {
	now := time.Now().UTC()
	s := &Session{
		ID:        id,
		CreatedAt: now,
		UpdatedAt: now,
	}
	for _, o := range opts {
		o(s)
	}
	return s
}

// Spawn creates a child session linked to this parent. The child
// starts with an empty turn history (unlike Fork which copies turns).
// It inherits the parent's Store and Publisher unless overridden.
func (s *Session) Spawn(
	ctx context.Context, id string, opts ...Option,
) *Session {
	now := time.Now().UTC()
	child := &Session{
		ID:        id,
		ParentID:  s.ID,
		CreatedAt: now,
		UpdatedAt: now,
		// Inherit parent's runtime components.
		store:  s.store,
		pub:    s.pub,
		router: s.router,
	}

	for _, o := range opts {
		o(child)
	}

	s.mu.Lock()
	s.Children = append(s.Children, id)
	s.mu.Unlock()

	if s.pub != nil {
		_ = s.pub.Publish(ctx, TopicSessionSpawned, map[string]any{
			"parent_id": s.ID,
			"child_id":  id,
		})
	}

	return child
}

// SendTo sends a message to another session via the Router.
func (s *Session) SendTo(
	ctx context.Context, targetID, content string,
) error {
	if s.router == nil {
		return ErrRouterNotSet
	}

	turn := Turn{
		ID:        fmt.Sprintf("%s->%s-%d", s.ID, targetID, time.Now().UnixNano()),
		Role:      RoleAgent,
		Content:   content,
		Timestamp: time.Now().UTC(),
		Metadata: map[string]any{
			"from_session": s.ID,
		},
	}

	return s.router.Route(ctx, s.ID, targetID, turn)
}

// --- Router ---

// DirectRouter delivers turns by looking up sessions in a registry.

type DirectRouter struct {
	mu       sync.RWMutex
	sessions map[string]*Session
}

// NewDirectRouter creates an empty DirectRouter.
func NewDirectRouter() *DirectRouter {
	return &DirectRouter{sessions: make(map[string]*Session)}
}

// Register adds a session to the router's registry.
func (r *DirectRouter) Register(s *Session) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.sessions[s.ID] = s
}

// Unregister removes a session from the router's registry.
func (r *DirectRouter) Unregister(id string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	delete(r.sessions, id)
}

// Route delivers a turn to the target session by appending it.
func (r *DirectRouter) Route(
	_ context.Context, _, toID string, turn Turn,
) error {
	r.mu.RLock()
	target, ok := r.sessions[toID]
	r.mu.RUnlock()

	if !ok {
		return fmt.Errorf("%w: %s", ErrSessionNotFound, toID)
	}

	target.mu.Lock()
	target.Turns = append(target.Turns, turn)
	target.UpdatedAt = time.Now().UTC()
	target.mu.Unlock()

	return nil
}

// --- Supervisor ---

// Supervisor tracks child sessions and observes lifecycle events.
type Supervisor struct {
	mu          sync.Mutex
	children    map[string]*supervisedChild
	onChildDone func(id string)
	pub         Publisher
}

type supervisedChild struct {
	session  *Session
	cancel   context.CancelFunc
	done     chan struct{}
	doneOnce sync.Once
}

// NewSupervisor creates a Supervisor. If pub is non-nil, the
// supervisor uses it to observe child lifecycle events.
func NewSupervisor(pub Publisher) *Supervisor {
	return &Supervisor{
		children: make(map[string]*supervisedChild),
		pub:      pub,
	}
}

// OnChildDone registers a callback fired when a watched child
// session completes.
func (sv *Supervisor) OnChildDone(fn func(id string)) {
	sv.mu.Lock()
	defer sv.mu.Unlock()
	sv.onChildDone = fn
}

// Watch registers a child session under supervision. Returns a
// derived context and cancel func for the child's lifetime.
func (sv *Supervisor) Watch(
	ctx context.Context, s *Session,
) (context.Context, context.CancelFunc, error) {
	sv.mu.Lock()
	defer sv.mu.Unlock()

	if _, exists := sv.children[s.ID]; exists {
		return nil, nil, ErrAlreadyWatched
	}

	childCtx, cancel := context.WithCancel(ctx)
	done := make(chan struct{})

	sv.children[s.ID] = &supervisedChild{
		session: s,
		cancel:  cancel,
		done:    done,
	}

	go func() {
		select {
		case <-childCtx.Done():
		case <-done:
		}

		sv.mu.Lock()
		cb := sv.onChildDone
		sv.mu.Unlock()

		if cb != nil {
			cb(s.ID)
		}

		if sv.pub != nil {
			_ = sv.pub.Publish(context.Background(), TopicChildDone, map[string]any{
				"child_id": s.ID,
			})
		}
	}()

	return childCtx, cancel, nil
}

// Cancel cancels a specific child's context and signals done so
// WaitAll unblocks.
func (sv *Supervisor) Cancel(childID string) error {
	sv.mu.Lock()
	c, ok := sv.children[childID]
	sv.mu.Unlock()

	if !ok {
		return fmt.Errorf("%w: %s", ErrChildNotFound, childID)
	}

	c.cancel()
	c.doneOnce.Do(func() { close(c.done) })
	return nil
}

// Done signals that a child session has completed its work.
// Safe to call multiple times; only the first call closes the channel.
func (sv *Supervisor) Done(childID string) error {
	sv.mu.Lock()
	c, ok := sv.children[childID]
	sv.mu.Unlock()

	if !ok {
		return fmt.Errorf("%w: %s", ErrChildNotFound, childID)
	}

	c.doneOnce.Do(func() { close(c.done) })
	return nil
}

// WaitAll blocks until all watched children complete or ctx expires.
func (sv *Supervisor) WaitAll(ctx context.Context) error {
	sv.mu.Lock()
	children := make([]*supervisedChild, 0, len(sv.children))
	for _, c := range sv.children {
		children = append(children, c)
	}
	sv.mu.Unlock()

	for _, c := range children {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-c.done:
		}
	}

	return nil
}
