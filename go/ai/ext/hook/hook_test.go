package hook

import (
	"context"
	"errors"
	"sync"
	"testing"

	"hop.top/kit/go/runtime/bus"
)

func TestSubscribeAndDispatch(t *testing.T) {
	bus := NewBus()

	var called bool
	bus.Subscribe(BeforeInit, func(_ context.Context, payload any) error {
		called = true
		if payload != "hello" {
			t.Errorf("unexpected payload: %v", payload)
		}
		return nil
	}, 0)

	if err := bus.Dispatch(context.Background(), BeforeInit, "hello"); err != nil {
		t.Fatalf("dispatch error: %v", err)
	}
	if !called {
		t.Fatal("handler was not called")
	}
}

func TestPriorityOrdering(t *testing.T) {
	bus := NewBus()

	var order []int

	bus.Subscribe(AfterInit, func(_ context.Context, _ any) error {
		order = append(order, 2)
		return nil
	}, 10)

	bus.Subscribe(AfterInit, func(_ context.Context, _ any) error {
		order = append(order, 1)
		return nil
	}, 1)

	bus.Subscribe(AfterInit, func(_ context.Context, _ any) error {
		order = append(order, 3)
		return nil
	}, 20)

	if err := bus.Dispatch(context.Background(), AfterInit, nil); err != nil {
		t.Fatalf("dispatch error: %v", err)
	}

	if len(order) != 3 || order[0] != 1 || order[1] != 2 || order[2] != 3 {
		t.Fatalf("wrong order: %v, want [1 2 3]", order)
	}
}

func TestDispatchStopsOnError(t *testing.T) {
	bus := NewBus()
	errBoom := errors.New("boom")

	bus.Subscribe(BeforeRun, func(_ context.Context, _ any) error {
		return errBoom
	}, 0)

	bus.Subscribe(BeforeRun, func(_ context.Context, _ any) error {
		t.Fatal("second handler should not run")
		return nil
	}, 10)

	err := bus.Dispatch(context.Background(), BeforeRun, nil)
	if !errors.Is(err, errBoom) {
		t.Fatalf("expected errBoom, got: %v", err)
	}
}

func TestDispatchAllCollectsErrors(t *testing.T) {
	bus := NewBus()
	err1 := errors.New("err1")
	err2 := errors.New("err2")

	bus.Subscribe(AfterRun, func(_ context.Context, _ any) error {
		return err1
	}, 0)

	bus.Subscribe(AfterRun, func(_ context.Context, _ any) error {
		return nil
	}, 5)

	bus.Subscribe(AfterRun, func(_ context.Context, _ any) error {
		return err2
	}, 10)

	errs := bus.DispatchAll(context.Background(), AfterRun, nil)
	if len(errs) != 2 {
		t.Fatalf("expected 2 errors, got %d: %v", len(errs), errs)
	}
	if !errors.Is(errs[0], err1) || !errors.Is(errs[1], err2) {
		t.Fatalf("unexpected errors: %v", errs)
	}
}

func TestContextCancellation(t *testing.T) {
	bus := NewBus()
	ctx, cancel := context.WithCancel(context.Background())
	cancel() // already canceled

	bus.Subscribe(BeforeClose, func(_ context.Context, _ any) error {
		t.Fatal("handler should not run with canceled context")
		return nil
	}, 0)

	err := bus.Dispatch(ctx, BeforeClose, nil)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("expected context.Canceled, got: %v", err)
	}

	errs := bus.DispatchAll(ctx, BeforeClose, nil)
	if len(errs) != 1 || !errors.Is(errs[0], context.Canceled) {
		t.Fatalf("expected [context.Canceled], got: %v", errs)
	}
}

func TestContextCancelledMidDispatch(t *testing.T) {
	bus := NewBus()
	ctx, cancel := context.WithCancel(context.Background())

	bus.Subscribe(BeforeRun, func(_ context.Context, _ any) error {
		cancel()
		return nil
	}, 0)

	bus.Subscribe(BeforeRun, func(_ context.Context, _ any) error {
		t.Fatal("should not run after cancel")
		return nil
	}, 10)

	err := bus.Dispatch(ctx, BeforeRun, nil)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("expected context.Canceled, got: %v", err)
	}
}

func TestEmptyHookDispatch(t *testing.T) {
	bus := NewBus()

	if err := bus.Dispatch(context.Background(), "nonexistent", nil); err != nil {
		t.Fatalf("expected nil error for empty hook, got: %v", err)
	}

	errs := bus.DispatchAll(context.Background(), "nonexistent", nil)
	if len(errs) != 0 {
		t.Fatalf("expected no errors for empty hook, got: %v", errs)
	}
}

func TestHandlersCount(t *testing.T) {
	bus := NewBus()

	if n := bus.Handlers(BeforeInit); n != 0 {
		t.Fatalf("expected 0, got %d", n)
	}

	bus.Subscribe(BeforeInit, func(_ context.Context, _ any) error { return nil }, 0)
	bus.Subscribe(BeforeInit, func(_ context.Context, _ any) error { return nil }, 1)

	if n := bus.Handlers(BeforeInit); n != 2 {
		t.Fatalf("expected 2, got %d", n)
	}

	if n := bus.Handlers(AfterInit); n != 0 {
		t.Fatalf("expected 0 for AfterInit, got %d", n)
	}
}

func TestConcurrentSubscribeDispatch(t *testing.T) {
	bus := NewBus()
	var wg sync.WaitGroup

	for i := 0; i < 50; i++ {
		wg.Add(1)
		go func(p int) {
			defer wg.Done()
			bus.Subscribe(BeforeRun, func(_ context.Context, _ any) error {
				return nil
			}, p)
		}(i)
	}

	for i := 0; i < 50; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_ = bus.Dispatch(context.Background(), BeforeRun, nil)
		}()
	}

	wg.Wait()
}

func TestDefaultBus(t *testing.T) {
	d := Default()
	if d == nil {
		t.Fatal("default bus is nil")
	}

	// Verify package-level functions compile and route to default bus.
	Subscribe(Hook("test_default"), func(_ context.Context, _ any) error {
		return nil
	}, 0)

	if n := Handlers(Hook("test_default")); n != 1 {
		t.Fatalf("expected 1 handler on default bus, got %d", n)
	}

	if err := Dispatch(context.Background(), Hook("test_default"), nil); err != nil {
		t.Fatalf("default dispatch error: %v", err)
	}

	errs := DispatchAll(context.Background(), Hook("test_default"), nil)
	if len(errs) != 0 {
		t.Fatalf("default dispatchAll errors: %v", errs)
	}
}

func TestInnerBusReceivesHookEvents(t *testing.T) {
	b := NewBus()

	ch := make(chan bus.Event, 1)
	b.Inner().SubscribeAsync("kit.ext.hook.#", func(_ context.Context, e bus.Event) {
		ch <- e
	})

	b.Subscribe(BeforeRun, func(_ context.Context, _ any) error {
		return nil
	}, 0)

	if err := b.Dispatch(context.Background(), BeforeRun, "test-payload"); err != nil {
		t.Fatalf("dispatch error: %v", err)
	}

	received := <-ch
	if string(received.Topic) != "kit.ext.hook.before_run" {
		t.Fatalf("expected topic kit.ext.hook.before_run, got %s", received.Topic)
	}
	if received.Source != "kit.ext.hook" {
		t.Fatalf("expected source kit.ext.hook, got %s", received.Source)
	}
	if received.Payload != "test-payload" {
		t.Fatalf("expected payload test-payload, got %v", received.Payload)
	}
}

func TestInnerBusGlobPattern(t *testing.T) {
	b := NewBus()

	ch := make(chan string, 2)
	b.Inner().SubscribeAsync("kit.ext.hook.*", func(_ context.Context, e bus.Event) {
		ch <- string(e.Topic)
	})

	_ = b.Dispatch(context.Background(), BeforeInit, nil)
	_ = b.Dispatch(context.Background(), AfterInit, nil)

	got := map[string]bool{<-ch: true, <-ch: true}
	if !got["kit.ext.hook.before_init"] || !got["kit.ext.hook.after_init"] {
		t.Fatalf("expected both hook topics, got %v", got)
	}
}
