package local_test

import (
	"context"
	"fmt"
	"os"
	"strings"

	"hop.top/kit/go/storage/blob/local"
)

func Example() {
	dir, _ := os.MkdirTemp("", "blob-local")
	defer os.RemoveAll(dir)

	store, err := local.New(dir)
	if err != nil {
		panic(err)
	}

	ctx := context.Background()
	_ = store.Put(ctx, "reports/q1.txt", strings.NewReader("ok"), "text/plain")
	objs, _ := store.List(ctx, "reports/")
	fmt.Println(objs[0].Key, objs[0].Size)
	// Output: reports/q1.txt 2
}
