// Package notify_test holds compile-tested examples of the notify
// API. The point is: if the documented shapes drift from reality,
// this file fails to build, breaking CI.
//
// Examples that depend on time / network / random behavior omit the
// // Output: comment but still execute (the test framework runs every
// ExampleXxx). Examples with deterministic output assert on it.
package notify_test

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"time"

	"hop.top/kit/go/core/breaker"
	"hop.top/kit/go/core/redact"
	"hop.top/kit/go/runtime/bus"
	"hop.top/kit/go/runtime/notify"
	emailsink "hop.top/kit/go/runtime/notify/sinks/email"
	webhooksink "hop.top/kit/go/runtime/notify/sinks/webhook"
)

// nopSink swallows every event without error. Used by examples that
// want to demonstrate composition without requiring a real downstream
// transport.
type nopSink struct{}

func (nopSink) Drain(_ context.Context, _ bus.Event) error { return nil }
func (nopSink) Close() error                               { return nil }

// failingSink returns the configured error on every Drain. Used by
// retry / dead-letter examples.
type failingSink struct{ err error }

func (f failingSink) Drain(_ context.Context, _ bus.Event) error { return f.err }
func (failingSink) Close() error                                 { return nil }

// ExampleSeverity demonstrates the Severity type and its String form.
func ExampleSeverity() {
	for _, s := range []notify.Severity{
		notify.SeverityDebug,
		notify.SeverityInfo,
		notify.SeverityWarn,
		notify.SeverityError,
		notify.SeverityCritical,
	} {
		fmt.Println(s)
	}
	// Output:
	// debug
	// info
	// warn
	// error
	// critical
}

// typedPayload demonstrates the WithSeverity optional interface.
// SeverityOf checks for this interface
// before falling back to map[string]any lookup.
type typedPayload struct{ sev notify.Severity }

func (t typedPayload) Severity() notify.Severity { return t.sev }

// ExampleSeverityOf shows the resolution order: WithSeverity
// interface first, then map[string]any "severity" key, otherwise
// SeverityInfo.
func ExampleSeverityOf() {
	// 1. Typed payload implementing WithSeverity.
	typed := bus.NewEvent("alerts.system.outage", "test", typedPayload{sev: notify.SeverityError})
	fmt.Println(notify.SeverityOf(typed))

	// 2. Cross-process JSON-decoded shape: map[string]any with a
	// "severity" string key.
	decoded := bus.NewEvent("alerts.system.outage", "test", map[string]any{
		"severity": "warn",
	})
	fmt.Println(notify.SeverityOf(decoded))

	// 3. Anything else defaults to SeverityInfo.
	noSev := bus.NewEvent("alerts.system.outage", "test", "just a string")
	fmt.Println(notify.SeverityOf(noSev))

	// Output:
	// error
	// warn
	// info
}

// ExampleNewFilterSink shows the composition pattern. Production
// wiring calls webhooksink.New with a Slack URL; this example
// substitutes httptest + nopSink so it runs without external state.
// The shape (filter wrapping a sink, severity + topic pattern
// options) is the production one.
func ExampleNewFilterSink() {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	alerts := notify.NewFilterSink(
		webhooksink.New(srv.URL),
		notify.WithTopicPattern("kit.runtime.breaker.#"),
		notify.WithMinSeverity(notify.SeverityWarn),
	)
	audit := notify.NewFilterSink(
		nopSink{}, // stand-in for bus.NewJSONLSinkFile
		notify.WithTopicPattern("kit.#"),
	)

	// In production: bus.NewTeeBus(bus.New(), []bus.Sink{alerts, audit}, onErr).
	// Show that both compose as bus.Sink without a TeeBus to keep the example
	// runnable in isolation.
	_ = alerts
	_ = audit
	fmt.Println("ok")

	// Output: ok
}

// ExampleWithPredicate shows the third FilterOption.
func ExampleWithPredicate() {
	sink := notify.NewFilterSink(
		nopSink{},
		notify.WithPredicate(func(e bus.Event) bool {
			return e.Source == "ops"
		}),
	)
	// Source matches → forwarded.
	if err := sink.Drain(context.Background(), bus.NewEvent("kit.test", "ops", nil)); err != nil {
		fmt.Println("unexpected:", err)
	}
	// Source mismatch → silently dropped (Drain returns nil).
	if err := sink.Drain(context.Background(), bus.NewEvent("kit.test", "other", nil)); err != nil {
		fmt.Println("unexpected:", err)
	}
	fmt.Println("ok")
	// Output: ok
}

// ExampleExponentialBackoff shows the BackoffFunc + ExponentialBackoff
// signatures:
//
//	BackoffFunc func(attempt int) time.Duration
//	ExponentialBackoff(base, factor, jitter) BackoffFunc
//
// The non-jittered shape grows as base * factor^attempt; the example
// asserts the deterministic sequence without depending on math/rand.
func ExampleExponentialBackoff() {
	b := notify.ExponentialBackoff(100*time.Millisecond, 2.0, false)
	for i := 0; i < 4; i++ {
		fmt.Println(b(i))
	}
	// Output:
	// 100ms
	// 200ms
	// 400ms
	// 800ms
}

// ExampleNewRetrySink shows RetrySink usage. Three
// options compose: WithMaxAttempts, WithBackoff, WithDeadLetter. The
// example uses a nopSink as dead-letter so RetrySink.Drain returns
// nil when retries exhaust.
func ExampleNewRetrySink() {
	r := notify.NewRetrySink(
		failingSink{err: errors.New("transient")},
		notify.WithMaxAttempts(2),
		notify.WithBackoff(func(attempt int) time.Duration { return 0 }),
		notify.WithDeadLetter(nopSink{}),
	)
	defer r.Close()

	if err := r.Drain(context.Background(), bus.NewEvent("kit.test", "ex", nil)); err != nil {
		fmt.Println("unexpected:", err)
		return
	}
	fmt.Println("ok")
	// Output: ok
}

// ExampleNewRetrySink_openCircuit shows the open-circuit-is-terminal
// rule: an inner sink returning breaker.ErrBrokenCircuit
// stops further retries immediately and routes to the dead-letter sink.
func ExampleNewRetrySink_openCircuit() {
	r := notify.NewRetrySink(
		failingSink{err: breaker.ErrBrokenCircuit},
		notify.WithMaxAttempts(5), // would attempt 5 times for transient errors
		notify.WithBackoff(func(attempt int) time.Duration { return 0 }),
	)
	defer r.Close()

	err := r.Drain(context.Background(), bus.NewEvent("kit.test", "ex", nil))
	fmt.Println(errors.Is(err, breaker.ErrBrokenCircuit))
	// Output: true
}

// ExampleNewTeeBus_endToEnd shows the full wiring. Production wiring
// calls breaker.New(name, /* policies */); webhooksink.New(os.Getenv(...));
// emailsink.New(emailsink.NewSMTPMailer(...)); osnotifysink.New(...);
// bus.NewJSONLSinkFile(...). All of those are real shipped APIs.
//
// This example substitutes httptest + MailerFunc + a nopSink standing
// in for the file-backed JSONL sink and the OS sink, so it runs
// hermetically in `go test` (production wiring needs a real SMTP
// host, a real OS notify-send / osascript, and writable /var/log).
// The COMPOSITION shape — filter → retry → dead-letter, multiple sinks
// teed off one bus — is preserved verbatim.
//
// NOTE on osnotify: omitted from this example because osnotifysink.New
// can fail at construction (platform probe). The
// per-package example_test.go in sinks/osnotify covers the
// constructor.
func ExampleNewTeeBus_endToEnd() {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	red := redact.Default()
	webBreaker := breaker.New("notify-webhook-example")
	defer breaker.Unregister("notify-webhook-example")
	smtpBreaker := breaker.New("notify-email-example")
	defer breaker.Unregister("notify-email-example")

	deadLetter := nopSink{} // production uses bus.NewJSONLSinkFile("/var/log/kit/dl.jsonl")

	pages := notify.NewRetrySink(
		notify.NewFilterSink(
			webhooksink.New(
				srv.URL,
				webhooksink.WithRedactor(red),
				webhooksink.WithBreaker(webBreaker),
			),
			notify.WithMinSeverity(notify.SeverityCritical),
		),
		notify.WithMaxAttempts(5),
		notify.WithDeadLetter(deadLetter),
	)

	subj := emailsink.LiteralTemplate("alert")
	body := emailsink.LiteralTemplate("body")
	mailer := emailsink.MailerFunc(func(_ context.Context, _ emailsink.Message) error { return nil })
	summaries := notify.NewFilterSink(
		emailsink.New(
			mailer, // production uses emailsink.NewSMTPMailer("smtp.local", 25)
			emailsink.WithRecipients("ops@example.com"),
			emailsink.WithSubject(subj),
			emailsink.WithBody(body),
			emailsink.WithRedactor(red),
			emailsink.WithBreaker(smtpBreaker),
		),
		notify.WithMinSeverity(notify.SeverityWarn),
		notify.WithTopicPattern("billing.#"),
	)

	audit := nopSink{} // production uses bus.NewJSONLSinkFile("/var/log/kit/audit.jsonl")

	teed := bus.NewTeeBus(bus.New(), []bus.Sink{pages, summaries, audit}, nil)
	defer teed.Close(context.Background())

	fmt.Println("wired")
	// Output: wired
}
