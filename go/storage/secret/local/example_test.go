package local_test

import (
	"context"
	"fmt"
	"os"

	"hop.top/kit/go/core/identity"
	"hop.top/kit/go/storage/secret/file"
	"hop.top/kit/go/storage/secret/local"
)

func Example() {
	dir, _ := os.MkdirTemp("", "secret-local")
	defer os.RemoveAll(dir)

	kp, err := identity.Generate()
	if err != nil {
		panic(err)
	}
	store := file.New(dir, local.NewKeeper(kp))
	ctx := context.Background()
	_ = store.Set(ctx, "api-token", []byte("t0k3n"))
	s, _ := store.Get(ctx, "api-token")
	fmt.Println(string(s.Value))
	// Output: t0k3n
}
