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

// ExampleValidate documents the published-topic contract enforced on
// every Publish: 4 segments, ^[a-z][a-z0-9_]*$ per segment, <= 128
// characters total, no wildcards. Mirrors docs/adopters/reference/bus-api.md.
func ExampleValidate() {
	for _, t := range []bus.Topic{
		"kit.ai.request.started",
		"kit.ai.request",
		"kit.AI.request.started",
		"kit.ai.request.*",
		"kit.9ai.request.started",
	} {
		fmt.Println(t, bus.Validate(t) == nil)
	}

	// Publish-time Validate does NOT enforce past tense.
	fmt.Println("present-tense action:", bus.Validate("kit.ai.request.start") == nil)

	// Output:
	// kit.ai.request.started true
	// kit.ai.request false
	// kit.AI.request.started false
	// kit.ai.request.* false
	// kit.9ai.request.started false
	// present-tense action: true
}

// ExampleValidateTopic documents the construction-time contract, which
// adds the past-tense action rule on top of the shape rules.
func ExampleValidateTopic() {
	for _, t := range []bus.Topic{
		"kit.ai.request.started",
		"kit.ai.request.created",
		"kit.ai.request.start",
		"kit.ai.request.sent",
	} {
		fmt.Println(t, bus.ValidateTopic(t) == nil)
	}

	// Output:
	// kit.ai.request.started true
	// kit.ai.request.created true
	// kit.ai.request.start false
	// kit.ai.request.sent true
}

// ExamplePrefixTopics builds a TopicMap from a 3-segment prefix.
func ExamplePrefixTopics() {
	tm, err := bus.PrefixTopics("wsm.runtime.workspace", []string{"created", "updated"})
	fmt.Println(err)
	fmt.Println(tm["created"])
	fmt.Println(tm["updated"])

	// Output:
	// <nil>
	// wsm.runtime.workspace.created
	// wsm.runtime.workspace.updated
}
