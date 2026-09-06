package sqlite_test

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"hop.top/kit/go/storage/kv/sqlite"
)

func Example() {
	dir, _ := os.MkdirTemp("", "kv-sqlite")
	defer os.RemoveAll(dir)

	store, err := sqlite.New(filepath.Join(dir, "cache.db"))
	if err != nil {
		panic(err)
	}
	defer store.Close()

	ctx := context.Background()
	_ = store.PutWithTTL(ctx, "session", []byte("abc"), time.Hour)
	keys, _ := store.List(ctx, "sess")
	fmt.Println(keys)
	// Output: [session]
}
