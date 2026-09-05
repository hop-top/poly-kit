package kv_test

import (
	"context"
	"fmt"
	"os"
	"path/filepath"

	"hop.top/kit/go/storage/kv"
	_ "hop.top/kit/go/storage/kv/sqlite"
)

func Example() {
	dir, _ := os.MkdirTemp("", "kv")
	defer os.RemoveAll(dir)

	store, err := kv.OpenContext(context.Background(), kv.Config{
		Backend: "sqlite",
		Path:    filepath.Join(dir, "cache.db"),
	})
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
