package blob_test

import (
	"context"
	"fmt"
	"io"
	"os"
	"strings"

	"hop.top/kit/go/storage/blob"
	"hop.top/kit/go/storage/blob/local"
)

func Example() {
	dir, _ := os.MkdirTemp("", "blob")
	defer os.RemoveAll(dir)

	var store blob.Store
	store, err := local.New(dir)
	if err != nil {
		panic(err)
	}

	ctx := context.Background()
	_ = store.Put(ctx, "reports/q1.txt", strings.NewReader("ok"), "text/plain")
	rc, _ := store.Get(ctx, "reports/q1.txt")
	defer rc.Close()
	b, _ := io.ReadAll(rc)
	fmt.Println(string(b))
	// Output: ok
}
