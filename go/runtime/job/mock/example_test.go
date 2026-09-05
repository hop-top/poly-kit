package mock_test

import (
	"context"
	"fmt"

	"hop.top/kit/go/runtime/job"
	"hop.top/kit/go/runtime/job/mock"
)

func Example() {
	ctx := context.Background()
	svc := mock.New()

	id, err := svc.Enqueue(ctx, job.EnqueueOpts{Queue: "default", Type: "email.send"})
	if err != nil {
		panic(err)
	}
	j, err := svc.Claim(ctx, "default", "worker-1")
	if err != nil {
		panic(err)
	}
	fmt.Println(j.ID, j.Status)

	if err := svc.Complete(ctx, id, nil); err != nil {
		panic(err)
	}
	got, _ := svc.Get(ctx, id)
	fmt.Println(got.Status)

	// Output:
	// job_1 active
	// succeeded
}
