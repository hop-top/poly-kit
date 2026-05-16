package sync_test

import (
	"context"
	"fmt"

	"hop.top/kit/go/runtime/sync"
)

func ExampleClock() {
	clk := sync.NewClock("node-a")
	ts := clk.Now()

	fmt.Println(ts.NodeID)
	fmt.Println(ts.Physical > 0)
	fmt.Println(ts.Logical == 0)
	// Output:
	// node-a
	// true
	// true
}

func ExampleComputeDiff() {
	clk := sync.NewClock("node-a")
	ts := clk.Now()

	type Item struct {
		Name string `json:"name"`
	}

	d, err := sync.ComputeDiff("item-1", "item", nil, Item{Name: "hat"}, ts)
	if err != nil {
		panic(err)
	}

	fmt.Println(d.EntityID)
	fmt.Println(d.Operation == sync.OpCreate)
	fmt.Println(string(d.After))
	// Output:
	// item-1
	// true
	// {"name":"hat"}
}

func ExampleLastWriteWins() {
	clk := sync.NewClock("node-a")
	ts1 := clk.Now()
	ts2 := clk.Now() // later

	local := sync.Diff{EntityID: "e1", Timestamp: ts1, NodeID: "node-a"}
	remote := sync.Diff{EntityID: "e1", Timestamp: ts2, NodeID: "node-a"}

	winner := sync.LastWriteWins(local, remote)
	fmt.Println(winner.Timestamp.Equal(ts2))
	// Output:
	// true
}

func ExampleRemoteSet() {
	rs := sync.NewRemoteSet()

	mt := sync.NewMemoryTransport()
	_ = rs.Add(sync.Remote{Name: "origin", Transport: mt, Mode: sync.Bidirectional})
	_ = rs.Add(sync.Remote{Name: "backup", Transport: mt, Mode: sync.PushOnly})

	fmt.Println(rs.Len())

	_ = rs.Remove("backup")
	fmt.Println(rs.Len())
	// Output:
	// 2
	// 1
}

func ExampleNewReplicator() {
	mt := sync.NewMemoryTransport()
	rem := sync.Remote{Name: "origin", Transport: mt, Mode: sync.Bidirectional}
	clk := sync.NewClock("local")

	// NewReplicator accepts a nil repo for setup-only demonstration.
	rep := sync.NewReplicator[stubEntity](nil,
		sync.WithRemote[stubEntity](rem),
		sync.WithClock[stubEntity](clk),
		sync.WithInterval[stubEntity](0),
	)

	fmt.Println(rep != nil)
	// Output:
	// true
}

func ExampleMemoryTransport() {
	ctx := context.Background()
	clk := sync.NewClock("node-a")

	mt := sync.NewMemoryTransport()

	ts := clk.Now()
	d, _ := sync.ComputeDiff("x", "thing", nil, map[string]string{"k": "v"}, ts)
	_ = mt.Push(ctx, []sync.Diff{d})

	pulled, _ := mt.Pull(ctx, sync.Timestamp{})
	fmt.Println(len(pulled))
	fmt.Println(pulled[0].EntityID)
	// Output:
	// 1
	// x
}

// stubEntity satisfies domain.Entity for the replicator example.
type stubEntity struct {
	ID string `json:"id"`
}

func (s stubEntity) GetID() string { return s.ID }
