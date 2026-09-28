package cmdsurface

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"sort"
	"strings"

	"github.com/spf13/cobra"
	"github.com/spf13/pflag"

	"hop.top/kit/go/core/redact"
)

// AnnotationSecretFlag is the pflag annotation that marks a flag's
// value secret. Audit sinks never receive the value of a flag that
// carries it: [SinkSet.Emit] replaces it with a mask, and every copy
// of the value elsewhere in the record (an echo on stdout, a quote in
// an error message) is replaced too. Set it with [MarkFlagSecret].
//
// The annotation lives on the flag, not the command, so a persistent
// flag marked once on the root is secret on every command that
// inherits it.
const AnnotationSecretFlag = "kit/secret"

// MarkFlagSecret marks the flag name in fs secret for audit (see
// [AnnotationSecretFlag]). Flags whose names already read as secret
// (token, password, api-key, …) are redacted without it; mark the
// ones whose names do not, such as a --dsn that embeds a password.
// Returns the error pflag reports when fs has no such flag.
func MarkFlagSecret(fs *pflag.FlagSet, name string) error {
	return fs.SetAnnotation(name, AnnotationSecretFlag, []string{"true"})
}

const (
	// auditRedacted replaces a value withheld from an audit record.
	// Same text as the redact package's Mask strategy, so a record
	// reads the same whichever layer caught the value.
	auditRedacted = "***REDACTED***"
	// auditMinLiteral is the shortest secret value substituted
	// wherever it recurs in the record. Shorter values ("1", "on")
	// would blank unrelated text.
	auditMinLiteral = 4
)

// DefaultAuditMaxFieldBytes is the longest field, in bytes, the
// content rules scan unless [AuditRedaction.MaxFieldBytes] says
// otherwise. A longer field is withheld from the record whole rather
// than shipped unscanned; Result.Data counts as one field, the total
// of its strings.
const DefaultAuditMaxFieldBytes = 4 << 10

// AuditRedaction adds to the redaction every audit record receives
// (see [SinkSet.Emit]). It can only add: the annotation, the name
// check and the default content rules stay in force whatever it
// holds, and a zero AuditRedaction adds nothing.
type AuditRedaction struct {
	// SecretFlags names flags whose values are masked, on top of
	// those marked [AnnotationSecretFlag] or named like a secret.
	SecretFlags []string
	// Rules are content rules applied after the redact package's
	// default corpus, for credentials shaped in ways it does not
	// know (an in-house token prefix). Build them with
	// [redact.NewRule].
	Rules []redact.Rule
	// MaxFieldBytes is the longest field the content rules scan;
	// a longer one is withheld whole, never shipped unscanned.
	// Zero or negative keeps [DefaultAuditMaxFieldBytes]. Raising
	// it keeps more of long outputs at the cost of scan time on
	// the invocation path.
	MaxFieldBytes int
}

// WithAuditRedaction installs extra audit redaction on the bridge.
// It applies to every record the bridge emits and to SinkSet.Emit
// calls made under the context the bridge hands its Runner. Repeated
// options accumulate flags and rules; the last positive MaxFieldBytes
// wins.
func WithAuditRedaction(r AuditRedaction) Option {
	return func(c *bridgeConfig) {
		c.redaction.SecretFlags = append(c.redaction.SecretFlags, r.SecretFlags...)
		c.redaction.Rules = append(c.redaction.Rules, r.Rules...)
		if r.MaxFieldBytes > 0 {
			c.redaction.MaxFieldBytes = r.MaxFieldBytes
		}
	}
}

// auditExtra is an AuditRedaction resolved for use: the flag names
// as a set, the rules compiled into one Redactor, and the field
// limit (zero for the default).
type auditExtra struct {
	secretFlags map[string]bool
	rules       *redact.Redactor
	maxField    int
}

// newAuditExtra resolves r, or returns nil when it changes nothing.
func newAuditExtra(r AuditRedaction) *auditExtra {
	if len(r.SecretFlags) == 0 && len(r.Rules) == 0 && r.MaxFieldBytes <= 0 {
		return nil
	}
	x := &auditExtra{maxField: max(r.MaxFieldBytes, 0)}
	for _, f := range r.SecretFlags {
		if x.secretFlags == nil {
			x.secretFlags = map[string]bool{}
		}
		x.secretFlags[strings.TrimLeft(f, "-")] = true
	}
	if len(r.Rules) > 0 {
		x.rules = redact.New().AddRules(r.Rules...)
	}
	return x
}

// auditScope is what the bridge hands SinkSet.Emit through the
// context: the resolved leaf's annotated secret flags and the
// bridge's extra redaction.
type auditScope struct {
	secretFlags map[string]bool
	extra       *auditExtra
}

// auditScopeKey is the context key for auditScope.
type auditScopeKey struct{}

// secretFlagSet returns the names of cmd's flags, own and inherited,
// that carry AnnotationSecretFlag. Nil when there are none.
func secretFlagSet(cmd *cobra.Command) map[string]bool {
	if cmd == nil {
		return nil
	}
	var out map[string]bool
	visit := func(f *pflag.Flag) {
		v, ok := f.Annotations[AnnotationSecretFlag]
		if !ok || (len(v) == 1 && v[0] == "false") {
			return
		}
		if out == nil {
			out = map[string]bool{}
		}
		out[f.Name] = true
	}
	cmd.InheritedFlags().VisitAll(visit)
	cmd.PersistentFlags().VisitAll(visit)
	cmd.Flags().VisitAll(visit)
	return out
}

// auditContext returns ctx carrying leaf's annotated secret flags and
// the bridge's extra redaction, for the SinkSet.Emit calls made under
// it. leaf may be nil (an unresolved path). The bridge stamps it
// before running the command, so a Runner that emits to its own
// SinkSet (the sinkRunner pattern) redacts the same way.
func (b *Bridge) auditContext(ctx context.Context, leaf *Leaf) context.Context {
	scope := auditScope{extra: b.audit}
	if leaf != nil {
		scope.secretFlags = leaf.secretFlags
	}
	if scope.secretFlags == nil && scope.extra == nil {
		return ctx
	}
	return context.WithValue(ctx, auditScopeKey{}, scope)
}

// secretName reports whether a flag, header, or field name reads as
// secret. It errs toward redaction: --max-tokens is masked along
// with --token.
func secretName(name string) bool {
	words := strings.FieldsFunc(strings.ToLower(name), func(r rune) bool {
		return (r < 'a' || r > 'z') && (r < '0' || r > '9')
	})
	if len(words) == 0 {
		return false
	}
	joined := strings.Join(words, "")
	if secretWholeNames[joined] {
		return true
	}
	for _, w := range words {
		if secretNameWords[w] {
			return true
		}
	}
	for _, p := range secretNameParts {
		if strings.Contains(joined, p) {
			return true
		}
	}
	return false
}

// secretNameParts mark a name secret wherever they occur, once
// separators are dropped: "api_key", "x-api-key" and "apiKey" all
// contain "apikey".
var secretNameParts = []string{
	"token", "passw", "passphrase", "secret", "credential", "bearer", "cookie",
	"apikey", "accesskey", "privatekey", "signingkey", "encryptionkey",
	"masterkey", "sessionkey", "clientkey",
}

// secretNameWords mark a name secret when they are one of its words:
// "auth-header" and "session-id", but not "author".
var secretNameWords = map[string]bool{
	"auth": true, "authorization": true, "pwd": true, "otp": true,
	"jwt": true, "session": true, "creds": true,
}

// secretWholeNames are secret only as the entire name: --key is,
// --sort-key and --idempotency-key are not.
var secretWholeNames = map[string]bool{"key": true, "pass": true, "pin": true}

// auditProvenanceExtra are the Meta.Extra keys kit's own transports
// stamp. They describe the request, not the command's input, and the
// PII rules would otherwise mask the remote address an audit exists
// to record. Known secret values are still substituted in them.
var auditProvenanceExtra = map[string]bool{
	"remote_addr": true, "peer_addr": true, "http_method": true, "http_path": true,
	"scopes": true, "oauth_issuer": true, "mcp_spec_version": true,
	"mcp_client_name": true, "mcp_client_version": true,
	"mcp_confirm_rejection": true, cacheExtraKey: true,
}

// auditOutputBlind is implemented by shipped sinks that never read
// Result.Stdout, Result.Stderr or Result.Data. When every matching
// sink is blind, SinkSet.Emit drops those fields instead of paying to
// scan them.
type auditOutputBlind interface {
	auditIgnoresOutput() bool
}

func (f *FileSink) auditIgnoresOutput() bool      { return f.Format == nil }
func (l *LogSink) auditIgnoresOutput() bool       { return l.Level > slog.LevelDebug }
func (s *TelemetrySink) auditIgnoresOutput() bool { return true }

// auditError carries a redacted message while still answering
// errors.Is for the original chain. It deliberately has no Unwrap: a
// sink that unwrapped it would reach the unredacted message.
type auditError struct {
	msg   string
	cause error
}

func (e *auditError) Error() string        { return e.msg }
func (e *auditError) Is(target error) bool { return errors.Is(e.cause, target) }

// auditRedactor redacts one audit record. It is built per record: the
// literals are the secret values that record carries.
type auditRedactor struct {
	rules    *redact.Redactor
	scope    auditScope
	literals []string
	// limit is the longest field scanned; longer ones are withheld.
	limit int
}

// redactForAudit returns copies of inv, res and err with every secret
// replaced. Three checks find them:
//
//   - the flag's kit/secret annotation, or a name listed in
//     AuditRedaction.SecretFlags (both carried on ctx by the bridge);
//   - a name that reads as secret: flags, Extra keys, --name=value
//     and --name value positional pairs, and map keys in Data;
//   - the content rules of redact.Default(), then any
//     AuditRedaction.Rules, for values shaped like a credential or
//     PII whatever they are called.
//
// Values found by the first two are also replaced wherever they recur
// in the record. withOutput false drops Stdout, Stderr and Data.
// The caller's values are never mutated.
func redactForAudit(ctx context.Context, inv Invocation, res Result, err error, withOutput bool) (Invocation, Result, error) {
	a := &auditRedactor{rules: redact.Default(), limit: DefaultAuditMaxFieldBytes}
	if ctx != nil {
		a.scope, _ = ctx.Value(auditScopeKey{}).(auditScope)
	}
	if x := a.scope.extra; x != nil && x.maxField > 0 {
		a.limit = x.maxField
	}
	secretArgs := a.collect(inv)

	out := inv
	out.Path = append([]string(nil), inv.Path...)
	if inv.Flags != nil {
		out.Flags = make(map[string]any, len(inv.Flags))
		for k, v := range inv.Flags {
			if a.secretFlag(k) {
				out.Flags[k] = maskValue(v)
				continue
			}
			out.Flags[k] = a.value(v)
		}
	}
	if inv.Args != nil {
		out.Args = make([]string, len(inv.Args))
		for i, s := range inv.Args {
			if secretArgs[i] {
				out.Args[i] = auditRedacted
				continue
			}
			out.Args[i] = a.scrub(s)
		}
	}
	if inv.Meta.Extra != nil {
		out.Meta.Extra = make(map[string]string, len(inv.Meta.Extra))
		for k, v := range inv.Meta.Extra {
			switch {
			case secretName(k):
				out.Meta.Extra[k] = auditRedacted
			case auditProvenanceExtra[k]:
				out.Meta.Extra[k] = a.substitute(v)
			default:
				out.Meta.Extra[k] = a.scrub(v)
			}
		}
	}

	rres := Result{ExitCode: res.ExitCode}
	if withOutput {
		rres.Stdout = a.scrub(res.Stdout)
		rres.Stderr = a.scrub(res.Stderr)
		rres.Data = a.data(res.Data)
	}

	rerr := err
	if err != nil {
		if msg := a.scrub(err.Error()); msg != err.Error() {
			rerr = &auditError{msg: msg, cause: err}
		}
	}
	return out, rres, rerr
}

// secretFlag reports whether flag name is annotated, listed, or named
// secret.
func (a *auditRedactor) secretFlag(name string) bool {
	if a.scope.secretFlags[name] || secretName(name) {
		return true
	}
	return a.scope.extra != nil && a.scope.extra.secretFlags[name]
}

// collect records the values of secret flags, secret positional
// pairs and secret Extra entries as literals, and returns the indexes
// of positional args that are secret values.
func (a *auditRedactor) collect(inv Invocation) map[int]bool {
	for k, v := range inv.Flags {
		if a.secretFlag(k) {
			a.addLiteralValue(v)
		}
	}
	var secretArgs map[int]bool
	markArg := func(i int, v string) {
		if secretArgs == nil {
			secretArgs = map[int]bool{}
		}
		secretArgs[i] = true
		a.addLiteral(v)
	}
	for i, s := range inv.Args {
		if !strings.HasPrefix(s, "-") {
			continue
		}
		name, val, hasVal := strings.Cut(strings.TrimLeft(s, "-"), "=")
		if !a.secretFlag(name) {
			continue
		}
		if hasVal {
			markArg(i, val)
		} else if i+1 < len(inv.Args) && !strings.HasPrefix(inv.Args[i+1], "-") {
			markArg(i+1, inv.Args[i+1])
		}
	}
	for k, v := range inv.Meta.Extra {
		if !secretName(k) {
			continue
		}
		a.addLiteral(v)
		// "Bearer <token>": the credential alone may recur elsewhere.
		if _, cred, ok := strings.Cut(v, " "); ok {
			a.addLiteral(strings.TrimSpace(cred))
		}
	}
	// Longest first, so a literal containing another is replaced whole.
	sort.Slice(a.literals, func(i, j int) bool { return len(a.literals[i]) > len(a.literals[j]) })
	return secretArgs
}

func (a *auditRedactor) addLiteral(s string) {
	if len(s) < auditMinLiteral || s == "true" || s == "false" {
		return
	}
	for _, l := range a.literals {
		if l == s {
			return
		}
	}
	a.literals = append(a.literals, s)
}

func (a *auditRedactor) addLiteralValue(v any) {
	switch t := v.(type) {
	case nil:
	case string:
		a.addLiteral(t)
	case []string:
		for _, s := range t {
			a.addLiteral(s)
		}
	case []any:
		for _, x := range t {
			a.addLiteralValue(x)
		}
	default:
		a.addLiteral(fmt.Sprintf("%v", t))
	}
}

// substitute replaces every known secret value in s.
func (a *auditRedactor) substitute(s string) string {
	for _, l := range a.literals {
		if strings.Contains(s, l) {
			s = strings.ReplaceAll(s, l, auditRedacted)
		}
	}
	return s
}

// scrub substitutes known secret values, then applies the content
// rules. A field longer than the limit is withheld whole: an
// unscanned field could carry anything.
func (a *auditRedactor) scrub(s string) string {
	if s == "" {
		return s
	}
	if len(s) > a.limit {
		return a.withheld(len(s))
	}
	if s = a.substitute(s); s == auditRedacted {
		return s
	}
	s = a.rules.Apply(s)
	if a.scope.extra != nil && a.scope.extra.rules != nil {
		s = a.scope.extra.rules.Apply(s)
	}
	return s
}

func (a *auditRedactor) withheld(n int) string {
	return fmt.Sprintf("[withheld from audit: %d bytes exceed the %d-byte redaction scan limit]", n, a.limit)
}

// maskValue masks a secret flag's value whatever its type.
func maskValue(v any) any {
	if v == nil {
		return nil
	}
	return auditRedacted
}

// value redacts a non-secret flag value: strings and string lists are
// scrubbed, other scalars pass through.
func (a *auditRedactor) value(v any) any {
	switch t := v.(type) {
	case string:
		return a.scrub(t)
	case []string:
		out := make([]string, len(t))
		for i, s := range t {
			out[i] = a.scrub(s)
		}
		return out
	case []any, map[string]any:
		return a.walk(t, new(int))
	default:
		return v
	}
}

// data redacts Result.Data. Values that are not already the generic
// JSON shape are round-tripped through JSON first, which is the form
// every shipped sink serializes anyway. When its strings total more
// than the field limit the whole payload is withheld.
func (a *auditRedactor) data(v any) any {
	if v == nil {
		return nil
	}
	switch v.(type) {
	case map[string]any, []any, string, bool, json.Number, float64:
	default:
		raw, err := json.Marshal(v)
		if err != nil {
			return a.withheld(0)
		}
		dec := json.NewDecoder(strings.NewReader(string(raw)))
		dec.UseNumber()
		if err := dec.Decode(&v); err != nil {
			return a.withheld(len(raw))
		}
	}
	scanned := 0
	out := a.walk(v, &scanned)
	if scanned > a.limit {
		return a.withheld(scanned)
	}
	return out
}

// walk redacts a generic JSON value. Map entries under a secret-named
// key are masked unless they are numbers or booleans; strings are
// scrubbed. scanned accumulates the string bytes seen; once it passes
// the field limit scanning stops, as the caller withholds the value.
func (a *auditRedactor) walk(v any, scanned *int) any {
	switch t := v.(type) {
	case string:
		*scanned += len(t)
		if *scanned > a.limit {
			return auditRedacted
		}
		return a.scrub(t)
	case map[string]any:
		out := make(map[string]any, len(t))
		for k, x := range t {
			if secretName(k) && !scalarNonString(x) {
				out[k] = auditRedacted
				continue
			}
			out[k] = a.walk(x, scanned)
		}
		return out
	case []any:
		out := make([]any, len(t))
		for i, x := range t {
			out[i] = a.walk(x, scanned)
		}
		return out
	default:
		return v
	}
}

func scalarNonString(v any) bool {
	switch v.(type) {
	case nil, bool, json.Number, float64, float32, int, int64, int32, uint, uint64, uint32:
		return true
	}
	return false
}
