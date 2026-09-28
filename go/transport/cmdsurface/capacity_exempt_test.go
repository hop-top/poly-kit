package cmdsurface_test

import (
	"context"
	"testing"
	"time"

	"github.com/spf13/cobra"

	"hop.top/kit/go/console/cli/idemstore"
	"hop.top/kit/go/storage/kv/memory"
	"hop.top/kit/go/transport/cmdsurface"
)

// A replay and a result-cache hit run nothing, so neither takes a
// capacity slot: with the only slot held and no queue, both are still
// answered, through Reserve as through Run.
func TestCapacity_ReplayAndCacheHitTakeNoSlot(t *testing.T) {
	root := &cobra.Command{Use: "tool"}
	for _, c := range []*cobra.Command{
		{Use: "open", Annotations: map[string]string{"kit/side-effect": "write"}},
		{Use: "look", Annotations: map[string]string{"kit/side-effect": "read", cmdsurface.AnnotationCacheTTL: "1m"}},
	} {
		c.RunE = func(*cobra.Command, []string) error { return nil }
		root.AddCommand(c)
	}
	store := memory.New()
	t.Cleanup(func() { _ = store.Close() })
	run := newBlockRunner()
	b := cmdsurface.New(root,
		cmdsurface.WithRunner(run),
		cmdsurface.WithConcurrency(cmdsurface.Concurrency{MaxInflight: 1, MaxQueue: 0}),
		cmdsurface.WithIdempotency(cmdsurface.NewIdempotencyLedger(idemstore.Memory()), time.Hour),
		cmdsurface.WithResultCache(store))
	b.Expose("*", cmdsurface.SurfaceREST)
	ctx := context.Background()
	call := func(id, leaf, key string) cmdsurface.Invocation {
		return cmdsurface.Invocation{Path: []string{leaf}, Meta: cmdsurface.Meta{
			Surface: cmdsurface.SurfaceREST, RequestID: id, IdempotencyKey: key}}
	}
	ranThrough := func(id, leaf, key string) {
		t.Helper()
		done := make(chan error, 1)
		go func() { _, err := b.Invoke(ctx, call(id, leaf, key)); done <- err }()
		awaitStart(t, run, id)
		run.proceed <- struct{}{}
		if err := <-done; err != nil {
			t.Fatalf("%s: %v", id, err)
		}
	}
	// Record a keyed write, and fill the cache.
	ranThrough("first", "open", "k1")
	ranThrough("fill", "look", "")

	// Hold the only slot.
	hold := invokeAsync(ctx, b, "hold")
	awaitStart(t, run, "hold")
	waitLoad(t, b, 1, 0)
	defer func() { run.proceed <- struct{}{}; awaitOutcome(t, hold) }()

	for _, c := range []struct{ name, leaf, key string }{
		{"replay", "open", "k1"},
		{"cache hit", "look", ""},
	} {
		adm, err := b.Admit(ctx, call(c.name, c.leaf, c.key))
		if err != nil {
			t.Fatalf("%s: Admit: %v", c.name, err)
		}
		if err := adm.Reserve(ctx); err != nil {
			t.Fatalf("%s: Reserve took a slot: %v", c.name, err)
		}
		out := make(chan cmdsurface.Event, 8)
		if err := adm.Stream(ctx, out); err != nil {
			t.Fatalf("%s: Stream: %v", c.name, err)
		}
		if _, err := b.Invoke(ctx, call(c.name+"-run", c.leaf, c.key)); err != nil {
			t.Fatalf("%s: Run took a slot: %v", c.name, err)
		}
		waitLoad(t, b, 1, 0)
	}
}
