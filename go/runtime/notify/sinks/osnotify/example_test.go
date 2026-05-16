// Package osnotifysink_test mirrors the OS-native notification code
// blocks in docs/specs/notifications.md §8.3 into compile-tested
// examples.
//
// Spec drift exposed by these examples (reported, not fixed; the spec
// is locked):
//
//   - The shipped option for the body template is WithText; spec §8.3
//     names it WithBody. The shipped name wins because callers depend
//     on it. Spec needs renaming WithBody → WithText (or shipped code
//     needs renaming) at next revision; the examples below exercise
//     the shipped name.
//
// Constructor portability note: osnotifysink.New(opts...) probes the
// platform at construction time per §8.3 + decision #9. On darwin it
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

// ExampleNew demonstrates the §8.3 constructor signature
// `func New(opts ...Option) (bus.Sink, error)` and every Option:
//
//   - WithTitle(t Template)
//   - WithBody(t Template) — NOTE: shipped as WithText; spec §8.3
//     names this WithBody. Drift reported; example uses the shipped
//     name so future spec edits to "WithBody" will fail to compile
//     and prompt a real reconciliation.
//   - WithRedactor(r)
//   - WithBreaker(b)
//
// Output is suppressed because constructor success is platform-
// dependent (linux requires notify-send on PATH); the example proves
// the API compiles, which is the point of T-0376.
func ExampleNew() {
	red := redact.Default()
	b := breaker.New("osnotify-example-new")
	defer breaker.Unregister("osnotify-example-new")

	sink, err := osnotifysink.New(
		osnotifysink.WithTitle(osnotifysink.LiteralTemplate("kit alert")),
		// NOTE: spec §8.3 names this WithBody; shipped code is
		// WithText. Update spec at next revision.
		osnotifysink.WithText(osnotifysink.LiteralTemplate("queue depth high")),
		osnotifysink.WithRedactor(red),
		osnotifysink.WithBreaker(b),
	)
	if err != nil {
		// Linux without notify-send / windows / unsupported platform.
		// Surface the error path documented in §8.3 instead of failing.
		fmt.Println("init:", err != nil)
		return
	}
	defer sink.Close()
	fmt.Println("init:", false)
	// Output is platform-dependent; deliberately omitted so the
	// example runs green on darwin, linux+notify-send, linux without
	// notify-send, and windows alike.
}

// ExampleTextTemplate covers the helper §8.3 names for templating
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
// referenced from §8.3 ("Title and Body are templated against
// bus.Event") for the constant-string case.
func ExampleLiteralTemplate() {
	tmpl := osnotifysink.LiteralTemplate("constant title")
	_ = tmpl
	fmt.Println("ok")
	// Output: ok
}
