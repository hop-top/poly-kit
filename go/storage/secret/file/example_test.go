package file_test

import (
	"context"
	"fmt"
	"os"

	"hop.top/kit/go/storage/secret/file"
)

func Example() {
	dir, _ := os.MkdirTemp("", "secret-file")
	defer os.RemoveAll(dir)

	store := file.New(dir, nil)
	ctx := context.Background()
	_ = store.Set(ctx, "db/password", []byte("hunter2"))
	keys, _ := store.List(ctx, "db/")
	fmt.Println(keys)
	// Output: [db/password]
}
