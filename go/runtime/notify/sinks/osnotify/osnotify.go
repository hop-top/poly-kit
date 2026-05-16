// Package osnotifysink delivers bus events as OS-native desktop
// notifications. darwin uses osascript (`display notification`);
// linux uses notify-send. Windows is not supported in MVP — the
// constructor returns an error.
//
// Pipeline order on Drain (per
// go/runtime/notify/guardrails.go):
//
//	template render (title + text) → redactor.Apply → breaker.WrapCtx → runner.Run
//
// Constructor signature is the spec exception case (decision #9):
// New(opts...) (bus.Sink, error) — because we probe platform tooling
// at construction (notify-send on linux). The other reference sinks
// (webhook, email) follow the no-error constructor convention.
//
// See:
//   - docs/specs/notifications.md §3 decisions #9, #10, #11
//   - docs/specs/notifications.md §7.5 Guardrails
//   - docs/specs/notifications.md §8.3 osnotify sink
//   - go/runtime/notify/guardrails.go (package-wide convention)
package osnotifysink

import (
	"context"
	"fmt"
	"os/exec"
	"runtime"
	"strings"
	"text/template"

	"hop.top/kit/go/core/breaker"
	"hop.top/kit/go/core/redact"
	"hop.top/kit/go/runtime/bus"
)

// Template renders a string against a bus.Event. Rendering is the
// first stage of the sink pipeline; the rendered title and text are
// then handed to the redactor and the runner.
type Template interface {
	Render(e bus.Event) (string, error)
}

// textTemplateImpl wraps text/template.Template for bus.Event input.
type textTemplateImpl struct {
	tmpl *template.Template
}

func (t *textTemplateImpl) Render(e bus.Event) (string, error) {
	var b strings.Builder
	if err := t.tmpl.Execute(&b, e); err != nil {
		return "", err
	}
	return b.String(), nil
}

// TextTemplate parses src as a text/template referencing bus.Event
// fields ({{.Topic}}, {{.Source}}, {{.Payload}}, etc.). Returns an
// error on parse failure.
func TextTemplate(src string) (Template, error) {
	t, err := template.New("osnotify").Parse(src)
	if err != nil {
		return nil, fmt.Errorf("osnotify: parse template: %w", err)
	}
	return &textTemplateImpl{tmpl: t}, nil
}

// literalTemplate is a Template that always renders the same string.
type literalTemplate string

func (l literalTemplate) Render(bus.Event) (string, error) {
	return string(l), nil
}

// LiteralTemplate returns a Template that always renders s, regardless
// of the event. Useful for fixed titles like "kit alert".
func LiteralTemplate(s string) Template {
	return literalTemplate(s)
}

// platform identifies the target OS notification mechanism. Set once
// at construction by New based on runtime.GOOS.
type platform int

const (
	platformDarwin platform = iota
	platformLinux
)

// Sink delivers a bus.Event as an OS-native desktop notification.
// Constructed via New; do not zero-value.
type Sink struct {
	plat      platform
	runner    runner
	titleTmpl Template
	textTmpl  Template
	redactor  *redact.Redactor
	breaker   breaker.Breaker
}

// opts carries construction parameters before they are sealed into a
// *Sink. The zero value has all fields nil; defaultOpts seeds the
// production runner.
type opts struct {
	title    Template
	text     Template
	runner   runner
	redactor *redact.Redactor
	breaker  breaker.Breaker
}

func defaultOpts() opts {
	return opts{runner: execRunner{}}
}

// Option configures a Sink at construction.
type Option func(*opts)

// WithTitle sets the Template used to render the notification title.
// Required: New does not validate, but Drain returns an error if
// either title or text is unset at delivery time.
func WithTitle(t Template) Option {
	return func(o *opts) { o.title = t }
}

// WithText sets the Template used to render the notification body
// text. Required (see WithTitle).
func WithText(t Template) Option {
	return func(o *opts) { o.text = t }
}

// WithRedactor wraps rendered title + text through r before egress.
// Default nil = no redaction (per the guardrails convention). Ops opt
// in via construction.
func WithRedactor(r *redact.Redactor) Option {
	return func(o *opts) { o.redactor = r }
}

// WithBreaker gates runner.Run through b. When the circuit is open
// Drain returns breaker.ErrBrokenCircuit and the runner is not
// invoked. Default nil = no breaker.
func WithBreaker(b breaker.Breaker) Option {
	return func(o *opts) { o.breaker = b }
}

// withRunner injects a fake runner for unit tests. Unexported because
// production callers should never need to override the production
// execRunner. Tests in the same package use it via package-internal
// access.
func withRunner(r runner) Option {
	return func(o *opts) { o.runner = r }
}

// New returns a Sink configured for the host platform.
//
// Platform probe (decision #9):
//   - darwin:  osascript ships with macOS; not probed. Runtime exec
//     failure is informative enough for the rare stripped-down case.
//   - linux:   notify-send is probed via exec.LookPath. Missing →
//     returns an error wrapped with the exec.LookPath error.
//   - windows: returns an error ("not supported on windows in MVP").
//   - other:   returns an error ("unsupported platform").
//
// notify-send is probed once at construction; a notify-send installed
// after construction will not be picked up. This matches kit's
// fail-fast-on-misconfiguration pattern.
func New(opts ...Option) (bus.Sink, error) {
	o := defaultOpts()
	for _, opt := range opts {
		opt(&o)
	}

	var plat platform
	switch runtime.GOOS {
	case "darwin":
		plat = platformDarwin
	case "linux":
		plat = platformLinux
		if _, err := exec.LookPath("notify-send"); err != nil {
			return nil, fmt.Errorf("osnotify: notify-send not on PATH: %w", err)
		}
	case "windows":
		return nil, fmt.Errorf("osnotify: not supported on windows in MVP")
	default:
		return nil, fmt.Errorf("osnotify: unsupported platform %q", runtime.GOOS)
	}

	if o.runner == nil {
		o.runner = execRunner{}
	}

	return &Sink{
		plat:      plat,
		runner:    o.runner,
		titleTmpl: o.title,
		textTmpl:  o.text,
		redactor:  o.redactor,
		breaker:   o.breaker,
	}, nil
}

// Drain renders the title + text templates against e, applies the
// optional redactor, then dispatches to runner.Run wrapped by the
// optional breaker.
func (s *Sink) Drain(ctx context.Context, e bus.Event) error {
	if s.titleTmpl == nil || s.textTmpl == nil {
		return fmt.Errorf("osnotify: title and text templates required")
	}
	title, err := s.titleTmpl.Render(e)
	if err != nil {
		return fmt.Errorf("osnotify: render title: %w", err)
	}
	text, err := s.textTmpl.Render(e)
	if err != nil {
		return fmt.Errorf("osnotify: render text: %w", err)
	}
	if s.redactor != nil {
		title = s.redactor.Apply(title)
		text = s.redactor.Apply(text)
	}
	name, args := s.command(title, text)

	call := func(ctx context.Context) error {
		if err := s.runner.Run(ctx, name, args...); err != nil {
			return fmt.Errorf("osnotify: run %s: %w", name, err)
		}
		return nil
	}
	if s.breaker != nil {
		return breaker.WrapCtx(s.breaker, ctx, call)
	}
	return call(ctx)
}

// Close is a no-op; the sink owns no long-lived resources.
func (s *Sink) Close() error { return nil }

// command returns the platform-specific shell command + args for the
// rendered title/text. Exported via tests through package-internal
// access.
func (s *Sink) command(title, text string) (string, []string) {
	switch s.plat {
	case platformDarwin:
		// AppleScript: display notification "<text>" with title "<title>"
		// Both literals must be properly quoted/escaped because they
		// flow through `osascript -e` as a single script argument.
		script := fmt.Sprintf(
			`display notification %s with title %s`,
			escapeAppleScript(text),
			escapeAppleScript(title),
		)
		return "osascript", []string{"-e", script}
	case platformLinux:
		// notify-send takes title + body as positional args. notify-send
		// itself does not interpret shell metacharacters in argv (we
		// invoke via exec, not a shell), so no further escaping is
		// required.
		return "notify-send", []string{title, text}
	}
	panic("osnotify: unreachable platform")
}

// escapeAppleScript wraps s in AppleScript double-quote string syntax
// with " and \ escaped. AppleScript string literals use C-style
// escapes for these two characters; other byte values pass through
// unchanged.
func escapeAppleScript(s string) string {
	var b strings.Builder
	b.Grow(len(s) + 2)
	b.WriteByte('"')
	for _, r := range s {
		switch r {
		case '"':
			b.WriteString(`\"`)
		case '\\':
			b.WriteString(`\\`)
		default:
			b.WriteRune(r)
		}
	}
	b.WriteByte('"')
	return b.String()
}
