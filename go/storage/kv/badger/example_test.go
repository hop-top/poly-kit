package badger_test

import (
	"context"
	"fmt"
	"os"

	"hop.top/kit/go/storage/kv/badger"
)

func Example() {
	dir, _ := os.MkdirTemp("", "kv-badger")
	defer os.RemoveAll(dir)

	store, err := badger.New(dir)
	if err != nil {
		panic(err)
	}
	defer store.Close()

	ctx := context.Background()
	_ = store.Put(ctx, "greeting", []byte("hello"))
	v, ok, _ := store.Get(ctx, "greeting")
	fmt.Println(string(v), ok)
	// Output: hello true
}
