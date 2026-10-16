# sqlite

## What it answers

How do several processes on one machine share one bus? Events are
written to a shared SQLite file and delivered to each process's
subscribers by polling. Wrong package for in-process events (the
default `bus` adapter) and for other machines (`bus.WithNetwork`).

## Use it when

- processes on one host must see each other's events → `sqlite.NewAdapter(path)`, passed to `bus.WithAdapter`
- delivery latency matters → `sqlite.WithPollInterval(d)` (default 100ms)

## Quick start

```go
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
```

Verified by `example_test.go` in this directory.

## Contract

- A subscriber sees only events published after it subscribed.
- Payloads cross the file as JSON: a subscriber receives the decoded
  form (`map[string]any`, `[]any`, `float64`), not the publisher's type.
- A sync handler's error does not veto: the event is already stored.
- After `Close`, `Publish` returns `bus.ErrBusClosed`; `Close` waits for
  in-flight async handlers.

## Neighbours

- `hop.top/kit/go/runtime/bus`: the bus, `Event`, topics, `WithAdapter`.
- `hop.top/kit/go/storage/sqlstore`: the SQLite store the adapter opens (WAL mode).

## See also

- [Bus API](../../../../docs/adopters/reference/bus-api.md)
