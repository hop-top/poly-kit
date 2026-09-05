package durabletask_test

import (
	"context"
	"fmt"

	"hop.top/kit/go/runtime/job"
	"hop.top/kit/go/runtime/job/durabletask"
)

func Example() {
	ctx := context.Background()
	svc, err := durabletask.New("") // "" = in-memory SQLite; pass a file path for durability
	if err != nil {
		panic(err)
	}
	defer svc.Close()

	id, err := svc.Enqueue(ctx, job.EnqueueOpts{Queue: "default", Type: "report.build"})
	if err != nil {
		panic(err)
	}
	j, err := svc.Claim(ctx, "default", "worker-1")
	if err != nil {
		panic(err)
	}
	fmt.Println(j.ID == id, j.Status)

	if err := svc.Complete(ctx, id, nil); err != nil {
		panic(err)
	}
	got, _ := svc.Get(ctx, id)
	fmt.Println(got.Status)

	// Output:
	// true active
	// succeeded
}
