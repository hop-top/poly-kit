package avatar_test

import (
	"context"
	"fmt"

	"hop.top/kit/go/core/avatar"
)

// Generate builds a URL; no request is made.
func ExampleGenerate() {
	u, err := avatar.Generate(context.Background(), avatar.Options{
		Seed: "noor",
		Size: 256,
	})
	if err != nil {
		fmt.Println("err:", err)
		return
	}
	fmt.Println(u)
	// Output: https://api.dicebear.com/9.x/shapes/svg?seed=noor&size=256
}
