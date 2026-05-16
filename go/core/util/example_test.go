package util_test

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"time"

	"hop.top/kit/go/core/util"
)

func ExampleRetry() {
	ctx := context.Background()
	attempt := 0

	err := util.Retry(ctx, util.RetryConfig{MaxAttempts: 3, BaseDelay: time.Millisecond}, func() error {
		attempt++
		if attempt < 3 {
			return errors.New("not ready")
		}
		return nil
	})

	fmt.Println(err)
	fmt.Println(attempt)
	// Output:
	// <nil>
	// 3
}

func ExampleRetryWithBackoff() {
	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()

	err := util.RetryWithBackoff(ctx, func() error {
		return errors.New("always fails")
	})

	fmt.Println(err)
	// Output: context deadline exceeded
}

func ExampleTo() {
	p := util.To(42)
	fmt.Println(*p)
	// Output: 42
}

func ExampleOr() {
	var p *string
	fmt.Println(util.Or(p, "default"))

	v := "hello"
	fmt.Println(util.Or(&v, "default"))
	// Output:
	// default
	// hello
}

func ExampleNewWriter() {
	var buf bytes.Buffer
	w := util.NewWriter(&buf)

	_ = w.Write(map[string]string{"name": "alice"})
	_ = w.Write(map[string]string{"name": "bob"})

	fmt.Print(buf.String())
	// Output:
	// {"name":"alice"}
	// {"name":"bob"}
}

func ExampleEach() {
	input := bytes.NewBufferString("{\"n\":1}\n{\"n\":2}\n{\"n\":3}\n")

	type rec struct {
		N int `json:"n"`
	}

	var sum int
	_ = util.Each[rec](input, func(r rec) error {
		sum += r.N
		return nil
	})

	fmt.Println(sum)
	// Output: 6
}

func ExampleEnvString() {
	os.Setenv("EXAMPLE_HOST", "prod.local")
	defer os.Unsetenv("EXAMPLE_HOST")

	fmt.Println(util.EnvString("EXAMPLE_HOST", "localhost"))
	fmt.Println(util.EnvString("EXAMPLE_MISSING", "localhost"))
	// Output:
	// prod.local
	// localhost
}

func ExampleSlug() {
	fmt.Println(util.Slug("Hello World!"))
	fmt.Println(util.Slug("  --foo BAR baz-- "))
	// Output:
	// hello-world
	// foo-bar-baz
}

func ExampleHumanDuration() {
	fmt.Println(util.HumanDuration(500 * time.Millisecond))
	fmt.Println(util.HumanDuration(90 * time.Second))
	fmt.Println(util.HumanDuration(3 * time.Hour))
	fmt.Println(util.HumanDuration(48 * time.Hour))
	// Output:
	// 0s
	// 1m
	// 3h
	// 2d
}

func ExampleShort() {
	fmt.Println(util.Short([]byte("hello"), 8))
	// Output: 2cf24dba
}
