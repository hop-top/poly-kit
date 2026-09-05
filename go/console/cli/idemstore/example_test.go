package idemstore_test

import (
	"context"
	"fmt"

	"hop.top/kit/go/console/cli/idemstore"
)

func ExampleOpenSQLite() {
	store, err := idemstore.OpenSQLite(":memory:", idemstore.DefaultTTL)
	if err != nil {
		fmt.Println("open:", err)
		return
	}
	defer store.Close()
	ctx := context.Background()

	_, hit, _ := store.Lookup(ctx, "deploy-42")
	fmt.Println("hit before record:", hit)

	_ = store.Record(ctx, "deploy-42", idemstore.Result{
		ExitCode: 0,
		Output:   []byte("{\"deployed\":true}\n"),
	})
	r, hit, _ := store.Lookup(ctx, "deploy-42")
	fmt.Println("hit after record:", hit)
	fmt.Print(string(r.Output))
	// Output:
	// hit before record: false
	// hit after record: true
	// {"deployed":true}
}
