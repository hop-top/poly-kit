// Package webhooksink_test mirrors the webhook code blocks in
// docs/specs/notifications.md §8.1 into compile-tested examples.
package webhooksink_test

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"time"

	"hop.top/kit/go/core/breaker"
	"hop.top/kit/go/core/redact"
	"hop.top/kit/go/runtime/bus"
	webhooksink "hop.top/kit/go/runtime/notify/sinks/webhook"
)

// ExampleNew demonstrates the §8.1 constructor signature
// `func New(url string, opts ...Option) bus.Sink` along with every
// Option from the spec table:
//   - WithHeader(k, v)
//   - WithAuthBearer(token)
//   - WithTemplate(t Template)
//   - WithHTTPClient(c)
//   - WithTimeout(d)
//   - WithRedactor(r)
//   - WithBreaker(b)
//
// Construction has no IO and cannot fail (spec decision #9 + §8.1).
func ExampleNew() {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	red := redact.Default()
	b := breaker.New("webhook-example-new")
	defer breaker.Unregister("webhook-example-new")

	sink := webhooksink.New(
		srv.URL,
		webhooksink.WithHeader("X-Source", "kit-example"),
		webhooksink.WithAuthBearer("test-token"),
		webhooksink.WithTemplate(webhooksink.DefaultJSONTemplate()),
		webhooksink.WithHTTPClient(&http.Client{Timeout: 2 * time.Second}),
		webhooksink.WithTimeout(2*time.Second), // ignored when WithHTTPClient also set
		webhooksink.WithRedactor(red),
		webhooksink.WithBreaker(b),
	)
	defer sink.Close()

	if err := sink.Drain(context.Background(), bus.NewEvent("kit.test.thing.created", "example", nil)); err != nil {
		fmt.Println("unexpected:", err)
		return
	}
	fmt.Println("ok")
	// Output: ok
}

// ExampleSlackTemplate covers the SlackTemplate helper named in §8.1:
// "A SlackTemplate(msg string) helper produces Slack's text: JSON."
func ExampleSlackTemplate() {
	tmpl, err := webhooksink.SlackTemplate(`alert: {{.Topic}}`)
	if err != nil {
		fmt.Println("parse:", err)
		return
	}
	body, ct, err := tmpl.Render(bus.Event{Topic: "kit.runtime.breaker.tripped"})
	if err != nil {
		fmt.Println("render:", err)
		return
	}
	fmt.Println(string(body))
	fmt.Println(ct)
	// Output:
	// {"text":"alert: kit.runtime.breaker.tripped"}
	// application/json
}

// ExampleDefaultJSONTemplate confirms the default Template documented
// in §8.1: marshal the bus.Event as JSON with application/json.
func ExampleDefaultJSONTemplate() {
	tmpl := webhooksink.DefaultJSONTemplate()
	_, ct, err := tmpl.Render(bus.Event{Topic: "kit.test", Source: "ex"})
	if err != nil {
		fmt.Println("render:", err)
		return
	}
	fmt.Println(ct)
	// Output: application/json
}
