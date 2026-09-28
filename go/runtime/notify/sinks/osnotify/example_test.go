// Package osnotifysink_test holds compile-tested examples of the
// OS-native notification sink.
//
// Constructor portability note: osnotifysink.New(opts...) probes the
// platform at construction time. On darwin it
// always succeeds; on linux it requires notify-send on PATH; on
// windows it returns an error. The examples therefore tolerate a
// constructor error without asserting on output (running `go test` on
// CI with no notify-send must not fail). Per-platform deeper tests
// live in osnotify_test.go and integration_test.go.
package osnotifysink_test

import (
	"fmt"

	"hop.top/kit/go/core/breaker"
	"hop.top/kit/go/core/redact"
	osnotifysink "hop.top/kit/go/runtime/notify/sinks/osnotify"
)

// ExampleNew demonstrates the constructor signature
// `func New(opts ...Option) (bus.Sink, error)` and every Option:
//
//   - WithTitle(t Template)
//   - WithText(t Template)
//   - WithRedactor(r)
//   - WithBreaker(b)
//
// Output is suppressed because constructor success is platform-
// dependent (linux requires notify-send on PATH); the example proves
// the API compiles, which is the point of this example.
func ExampleNew() {
	red := redact.Default()
	b := breaker.New("osnotify-example-new")
	defer breaker.Unregister("osnotify-example-new")

	sink, err := osnotifysink.New(
		osnotifysink.WithTitle(osnotifysink.LiteralTemplate("kit alert")),
		osnotifysink.WithText(osnotifysink.LiteralTemplate("queue depth high")),
		osnotifysink.WithRedactor(red),
		osnotifysink.WithBreaker(b),
	)
	if err != nil {
		// Linux without notify-send / windows / unsupported platform.
		// Surface the documented error path instead of failing.
		fmt.Println("init:", err != nil)
		return
	}
	defer sink.Close()
	fmt.Println("init:", false)
	// Output is platform-dependent; deliberately omitted so the
	// example runs green on darwin, linux+notify-send, linux without
	// notify-send, and windows alike.
}

// ExampleTextTemplate covers the helper for templating
// title + text against bus.Event fields ({{.Topic}}, {{.Source}}, etc.).
func ExampleTextTemplate() {
	tmpl, err := osnotifysink.TextTemplate(`{{.Source}}/{{.Topic}}`)
	if err != nil {
		fmt.Println("parse:", err)
		return
	}
	_ = tmpl // construction-only example; full Render covered in osnotify_test.go.
	fmt.Println("parsed")
	// Output: parsed
}

// ExampleLiteralTemplate covers the static-string Template helper
// for the constant-string case (title and text are otherwise
// templated against bus.Event).
func ExampleLiteralTemplate() {
	tmpl := osnotifysink.LiteralTemplate("constant title")
	_ = tmpl
	fmt.Println("ok")
	// Output: ok
}
