package memory_test

import (
	"context"
	"fmt"
	"time"

	"hop.top/kit/go/storage/kv/memory"
)

func Example() {
	store := memory.New(memory.WithMaxBytes(1 << 20))
	defer store.Close()

	ctx := context.Background()
	_ = store.PutWithTTL(ctx, "session", []byte("abc"), time.Hour)
	v, ok, _ := store.Get(ctx, "session")
	fmt.Println(string(v), ok)
	// Output: abc true
}
