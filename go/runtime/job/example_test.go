package job_test

import (
	"context"
	"fmt"

	"hop.top/kit/go/runtime/job"
	"hop.top/kit/go/runtime/job/mock"
)

func Example() {
	ctx := context.Background()
	svc := mock.New()

	// Enqueue a job.
	id, err := svc.Enqueue(ctx, job.EnqueueOpts{
		Queue: "default",
		Type:  "email.send",
		Payload: map[string]string{
			"to":      "user@example.com",
			"subject": "Welcome",
		},
	})
	if err != nil {
		panic(err)
	}

	// Claim and process.
	j, err := svc.Claim(ctx, "default", "worker-1")
	if err != nil {
		panic(err)
	}

	fmt.Printf("claimed job %s (type=%s)\n", j.ID, j.Type)

	// Complete.
	if err := svc.Complete(ctx, id, map[string]string{"status": "sent"}); err != nil {
		panic(err)
	}

	got, _ := svc.Get(ctx, id)
	fmt.Printf("final status: %s\n", got.Status)

	// Output:
	// claimed job job_1 (type=email.send)
	// final status: succeeded
}
