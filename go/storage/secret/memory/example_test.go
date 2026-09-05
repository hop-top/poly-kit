package memory_test

import (
	"context"
	"fmt"

	"hop.top/kit/go/storage/secret/memory"
)

func Example() {
	store := memory.New()
	ctx := context.Background()
	_ = store.Set(ctx, "api-token", []byte("t0k3n"))
	ok, _ := store.Exists(ctx, "api-token")
	fmt.Println(ok)
	// Output: true
}
