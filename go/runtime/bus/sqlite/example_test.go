package sqlite_test

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"hop.top/kit/go/runtime/bus"
	"hop.top/kit/go/runtime/bus/sqlite"
)

func ExampleNewAdapter() {
	dir, _ := os.MkdirTemp("", "bus-sqlite")
	defer os.RemoveAll(dir)

	a, err := sqlite.NewAdapter(filepath.Join(dir, "events.db"), sqlite.WithPollInterval(10*time.Millisecond))
	if err != nil {
		fmt.Println("open:", err)
		return
	}
	b := bus.New(bus.WithAdapter(a))
	defer b.Close(context.Background())

	got := make(chan bus.Event, 1)
	b.Subscribe("shop.order.item.created", func(_ context.Context, e bus.Event) error {
		got <- e
		return nil
	})
	_ = b.Publish(context.Background(), bus.NewEvent("shop.order.item.created", "checkout", "item-42"))

	e := <-got
	fmt.Println(e.Topic, e.Source, e.Payload)
	// Output: shop.order.item.created checkout item-42
}
