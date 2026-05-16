package bus_test

import (
	"context"
	"fmt"
	"sync"
	"time"

	"hop.top/kit/go/runtime/bus"
)

func ExampleNew() {
	b := bus.New()
	defer b.Close(context.Background())

	var wg sync.WaitGroup
	wg.Add(1)

	b.Subscribe("order.created", func(ctx context.Context, e bus.Event) error {
		fmt.Printf("topic=%s source=%s payload=%v\n", e.Topic, e.Source, e.Payload)
		wg.Done()
		return nil
	})

	_ = b.Publish(context.Background(), bus.NewEvent("order.created", "checkout", "item-42"))
	wg.Wait()

	// Output:
	// topic=order.created source=checkout payload=item-42
}

func ExampleTopicFilter() {
	f := bus.TopicFilter{
		Allow: []string{"order.#", "user.*"},
		Deny:  []string{"order.internal"},
	}

	fmt.Println(f.Match("order.created"))  // allowed by order.#
	fmt.Println(f.Match("order.internal")) // denied explicitly
	fmt.Println(f.Match("user.signup"))    // allowed by user.*
	fmt.Println(f.Match("billing.charge")) // not in allow list

	// Output:
	// true
	// false
	// true
	// false
}

func ExampleWithNetwork() {
	_ = bus.New(
		bus.WithNetwork("ws://peer-a:9090/bus", "ws://peer-b:9090/bus"),
		bus.WithNetworkOption(
			bus.WithFilter(bus.TopicFilter{Allow: []string{"cluster.#"}}),
			bus.WithOriginID("node-1"),
			bus.WithBackoff(500*time.Millisecond, 30*time.Second),
		),
	)
	// Setup-only: a real deployment would publish/subscribe and
	// eventually call Close.
}
