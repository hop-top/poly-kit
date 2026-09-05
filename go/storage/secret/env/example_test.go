package env_test

import (
	"context"
	"fmt"
	"os"

	"hop.top/kit/go/storage/secret/env"
)

func Example() {
	os.Setenv("APP_DB_PASSWORD", "hunter2")
	defer os.Unsetenv("APP_DB_PASSWORD")

	store := env.New("APP_")
	s, err := store.Get(context.Background(), "db/password")
	if err != nil {
		panic(err)
	}
	fmt.Println(string(s.Value))
	// Output: hunter2
}
