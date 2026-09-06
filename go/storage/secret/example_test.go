package secret_test

import (
	"context"
	"fmt"

	"hop.top/kit/go/storage/secret"
	_ "hop.top/kit/go/storage/secret/memory"
)

func Example() {
	store, err := secret.Open(secret.Config{Backend: "memory"})
	if err != nil {
		panic(err)
	}

	ctx := context.Background()
	_ = store.Set(ctx, "api-token", []byte("t0k3n"))
	s, _ := store.Get(ctx, "api-token")
	fmt.Println(s.Key, string(s.Value))
	// Output: api-token t0k3n
}
