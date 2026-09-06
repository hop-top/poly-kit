package composite_test

import (
	"context"
	"fmt"

	"hop.top/kit/go/storage/secret/composite"
	"hop.top/kit/go/storage/secret/memory"
)

func Example() {
	ci, dev := memory.New(), memory.New()
	store := composite.New(
		composite.Member{Name: "ci", Store: ci, Owns: composite.HasPrefix("ci/")},
		composite.Member{Name: "dev", Store: dev},
	)

	ctx := context.Background()
	_ = store.Set(ctx, "ci/token", []byte("a"))
	_ = store.Set(ctx, "db/password", []byte("b"))
	ciKeys, _ := ci.List(ctx, "")
	devKeys, _ := dev.List(ctx, "")
	fmt.Println(ciKeys, devKeys)
	// Output: [ci/token] [db/password]
}
