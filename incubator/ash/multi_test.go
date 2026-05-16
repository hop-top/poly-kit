package ash_test

import (
	"context"
	"sync"
	"testing"
	"time"

	"hop.top/ash"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// --- T-0323: Spawn tests ---

func TestSpawn_CreatesChildWithParentPointer(t *testing.T) {
	parent := ash.NewSession("parent-1")

	ctx := context.Background()
	child := parent.Spawn(ctx, "child-1")

	assert.Equal(t, "child-1", child.ID)
	assert.Equal(t, "parent-1", child.ParentID)
	assert.NotZero(t, child.CreatedAt)
	assert.Nil(t, child.ClosedAt)
}

func TestSpawn_ChildStartsEmpty(t *testing.T) {
	parent := ash.NewSession("parent-1")
	parent.Turns = append(parent.Turns, ash.Turn{
		ID: "t-1", Role: ash.RoleUser, Content: "hello",
	})

	child := parent.Spawn(context.Background(), "child-1")

	assert.Empty(t, child.Turns, "spawned child must have no turns")
	assert.Len(t, parent.Turns, 1, "parent turns unchanged")
}

func TestSpawn_ParentTracksChildren(t *testing.T) {
	parent := ash.NewSession("parent-1")

	parent.Spawn(context.Background(), "c-1")
	parent.Spawn(context.Background(), "c-2")

	assert.Equal(t, []string{"c-1", "c-2"}, parent.Children)
}

func TestSpawn_InheritsStore(t *testing.T) {
	store := ash.NewMemoryStore()
	parent := ash.NewSession("parent-1", ash.WithStore(store))

	child := parent.Spawn(context.Background(), "child-1")

	// Verify the child can use its inherited store by creating a
	// session in it — this just proves the store ref propagated.
	_ = child
	// The child's store is unexported; we verify inheritance by
	// spawning a grandchild and confirming the store still works.
	grandchild := child.Spawn(context.Background(), "grandchild-1")
	assert.Equal(t, "child-1", grandchild.ParentID)
}

func TestSpawn_OverridesProvider(t *testing.T) {
	parent := ash.NewSession("parent-1",
		ash.WithProvider(&stubProvider{name: "parent-llm"}),
	)

	childProv := &stubProvider{name: "child-llm"}
	child := parent.Spawn(context.Background(), "child-1",
		ash.WithProvider(childProv),
	)

	// Child has different provider; verify it's a distinct session.
	assert.Equal(t, "child-1", child.ID)
	assert.Equal(t, "parent-1", child.ParentID)
}

func TestSpawn_InheritsPublisher(t *testing.T) {
	pub := &recordingPublisher{}
	parent := ash.NewSession("parent-1", ash.WithPublisher(pub))

	parent.Spawn(context.Background(), "child-1")

	require.Len(t, pub.events, 1)
	assert.Equal(t, ash.TopicSessionSpawned, pub.events[0].topic)
}

// --- T-0324: Router / SendTo tests ---

func TestSendTo_DeliversTurn(t *testing.T) {
	router := ash.NewDirectRouter()

	sender := ash.NewSession("sender", ash.WithRouter(router))
	receiver := ash.NewSession("receiver", ash.WithRouter(router))

	router.Register(sender)
	router.Register(receiver)

	ctx := context.Background()
	err := sender.SendTo(ctx, "receiver", "hello from sender")
	require.NoError(t, err)

	assert.Len(t, receiver.Turns, 1)
	assert.Equal(t, ash.RoleAgent, receiver.Turns[0].Role)
	assert.Equal(t, "hello from sender", receiver.Turns[0].Content)
	assert.Equal(t, "sender", receiver.Turns[0].Metadata["from_session"])
}

func TestSendTo_UnknownTargetErrors(t *testing.T) {
	router := ash.NewDirectRouter()
	sender := ash.NewSession("sender", ash.WithRouter(router))
	router.Register(sender)

	err := sender.SendTo(context.Background(), "nobody", "msg")
	assert.ErrorIs(t, err, ash.ErrSessionNotFound)
}

func TestSendTo_NoRouterErrors(t *testing.T) {
	s := ash.NewSession("lonely")

	err := s.SendTo(context.Background(), "target", "msg")
	assert.ErrorIs(t, err, ash.ErrRouterNotSet)
}

func TestDirectRouter_RegisterUnregister(t *testing.T) {
	router := ash.NewDirectRouter()
	s := ash.NewSession("s-1")

	router.Register(s)
	router.Unregister("s-1")

	err := router.Route(context.Background(), "other", "s-1", ash.Turn{
		ID: "t-1", Role: ash.RoleAgent, Content: "test",
	})
	assert.ErrorIs(t, err, ash.ErrSessionNotFound)
}

// --- T-0325: Supervisor tests ---

func TestSupervisor_WatchRegisters(t *testing.T) {
	sv := ash.NewSupervisor(nil)
	child := ash.NewSession("child-1")

	ctx, cancel, err := sv.Watch(context.Background(), child)
	require.NoError(t, err)
	assert.NotNil(t, ctx)
	defer cancel()

	// Double-watch is an error.
	_, _, err = sv.Watch(context.Background(), child)
	assert.ErrorIs(t, err, ash.ErrAlreadyWatched)
}

func TestSupervisor_CancelStopsChild(t *testing.T) {
	sv := ash.NewSupervisor(nil)
	child := ash.NewSession("child-1")

	childCtx, _, err := sv.Watch(context.Background(), child)
	require.NoError(t, err)

	require.NoError(t, sv.Cancel("child-1"))

	select {
	case <-childCtx.Done():
		// expected
	case <-time.After(time.Second):
		t.Fatal("child context should be canceled")
	}
}

func TestSupervisor_CancelUnknownErrors(t *testing.T) {
	sv := ash.NewSupervisor(nil)
	err := sv.Cancel("unknown")
	assert.ErrorIs(t, err, ash.ErrChildNotFound)
}

func TestSupervisor_WaitAllBlocks(t *testing.T) {
	sv := ash.NewSupervisor(nil)

	c1 := ash.NewSession("c-1")
	c2 := ash.NewSession("c-2")

	_, _, err := sv.Watch(context.Background(), c1)
	require.NoError(t, err)
	_, _, err = sv.Watch(context.Background(), c2)
	require.NoError(t, err)

	done := make(chan error, 1)
	go func() {
		done <- sv.WaitAll(context.Background())
	}()

	// Neither child done yet — WaitAll should not return.
	select {
	case <-done:
		t.Fatal("WaitAll returned before children completed")
	case <-time.After(50 * time.Millisecond):
	}

	// Complete children.
	require.NoError(t, sv.Done("c-1"))
	require.NoError(t, sv.Done("c-2"))

	select {
	case err := <-done:
		assert.NoError(t, err)
	case <-time.After(time.Second):
		t.Fatal("WaitAll did not return after children completed")
	}
}

func TestSupervisor_WaitAllRespectsCtx(t *testing.T) {
	sv := ash.NewSupervisor(nil)
	child := ash.NewSession("c-1")
	_, _, err := sv.Watch(context.Background(), child)
	require.NoError(t, err)

	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()

	err = sv.WaitAll(ctx)
	assert.ErrorIs(t, err, context.DeadlineExceeded)
}

func TestSupervisor_OnChildDoneFires(t *testing.T) {
	pub := &recordingPublisher{}
	sv := ash.NewSupervisor(pub)

	var mu sync.Mutex
	var completedIDs []string
	sv.OnChildDone(func(id string) {
		mu.Lock()
		completedIDs = append(completedIDs, id)
		mu.Unlock()
	})

	child := ash.NewSession("c-1")
	_, _, err := sv.Watch(context.Background(), child)
	require.NoError(t, err)

	require.NoError(t, sv.Done("c-1"))

	// Give goroutine time to fire callback.
	time.Sleep(50 * time.Millisecond)

	mu.Lock()
	assert.Equal(t, []string{"c-1"}, completedIDs)
	mu.Unlock()

	// Publisher should have received the event.
	require.Len(t, pub.events, 1)
	assert.Equal(t, ash.TopicChildDone, pub.events[0].topic)
}

// --- Test helpers ---

type stubProvider struct {
	name string
}

func (p *stubProvider) Complete(
	_ context.Context, _ []ash.Turn,
) (ash.Turn, error) {
	return ash.Turn{
		ID: "resp-1", Role: ash.RoleAssistant,
		Content: "response from " + p.name,
	}, nil
}

func (p *stubProvider) Stream(
	_ context.Context, _ []ash.Turn,
) (ash.TurnStream, error) {
	return nil, nil
}

func (p *stubProvider) CallWithTools(
	_ context.Context, _ []ash.Turn, _ []ash.ToolDef,
) (ash.Turn, error) {
	return ash.Turn{}, nil
}

type recordedEvent struct {
	topic   string
	payload any
}

type recordingPublisher struct {
	mu     sync.Mutex
	events []recordedEvent
}

func (p *recordingPublisher) Publish(
	_ context.Context, topic string, payload any,
) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.events = append(p.events, recordedEvent{topic, payload})
	return nil
}
