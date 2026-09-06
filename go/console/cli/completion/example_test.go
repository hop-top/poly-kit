package completion_test

import (
	"context"
	"fmt"

	"hop.top/kit/go/console/cli/completion"
)

// ExampleStatic shows that the prefix filter is case-insensitive and
// matches against the item Value, never the Description.
func ExampleStatic() {
	c := completion.Static(
		completion.Item{Value: "leo", Description: "Low Earth Orbit"},
		completion.Item{Value: "geo", Description: "Geostationary"},
	)

	items, _ := c.Complete(context.Background(), "L")
	fmt.Println(items)

	// The description is not searched.
	items, _ = c.Complete(context.Background(), "Geostationary")
	fmt.Println(items)

	// An empty prefix returns everything.
	items, _ = c.Complete(context.Background(), "")
	fmt.Println(len(items))

	// Output:
	// [{leo Low Earth Orbit}]
	// []
	// 2
}

// ExamplePrefixed shows dimension:value completion. Before the colon
// the dimension itself is offered; after it the inner completer runs
// against the remainder.
func ExamplePrefixed() {
	c := completion.Prefixed(
		"env",
		completion.StaticValues("prod", "staging", "dev"),
	)

	items, _ := c.Complete(context.Background(), "en")
	fmt.Println(items)

	items, _ = c.Complete(context.Background(), "env:st")
	fmt.Println(items)

	// Output:
	// [{env: env values}]
	// [{env:staging }]
}
