package llm

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"regexp"
	"strings"

	"github.com/invopop/jsonschema"
)

// DefaultResponseFormatName names a [ResponseFormat] that sets none.
const DefaultResponseFormatName = "output"

// defaultMaxRepairs is how many times [CompleteStructured] re-asks.
const defaultMaxRepairs = 2

// ResponseFormat constrains a completion to a JSON Schema.
//
// Name labels the schema on providers that take one (OpenAI); it must
// be 1-64 characters of letters, digits, '_' or '-', and defaults to
// [DefaultResponseFormatName]. Schema is a JSON Schema document whose
// root is an object. Strict asks the provider for exact adherence where
// it has such a mode (OpenAI strict, which also requires every property
// listed in required and additionalProperties false); it is ignored
// elsewhere.
//
// # Schema subset
//
// Local validation ([ValidateJSON]) implements JSON Schema 2020-12 in
// full, but providers constrain decoding with a smaller subset, and
// reject a schema that steps outside it. The portable core, accepted
// by every adapter:
//
//   - root: type object
//   - type: string, number, integer, boolean, null, object, array
//   - properties, required, additionalProperties false, items
//   - enum, description, anyOf
//   - nesting of the above
//
// Keywords outside the core (pattern, format, minLength, minimum,
// minItems, oneOf, allOf, $ref and $defs, ...) are still checked
// locally, but the provider may refuse them or ignore them while
// decoding; a refusal reaches the caller as the adapter's own error. A
// schema using them depends on the repair loop, not the provider, to
// hold. See the adapter table in the reference docs for each mapping.
type ResponseFormat struct {
	Name   string
	Schema json.RawMessage
	Strict bool
}

var responseFormatName = regexp.MustCompile(`^[A-Za-z0-9_-]{1,64}$`)

// EffectiveName is Name, or [DefaultResponseFormatName] when unset.
func (rf ResponseFormat) EffectiveName() string {
	if rf.Name == "" {
		return DefaultResponseFormatName
	}
	return rf.Name
}

// CheckResponseFormat reports why rf cannot go on a request: a name
// that is not 1-64 letters, digits, '_' or '-', or a schema that is not
// a JSON object. A nil rf is valid. Adapters call it before sending; it
// does not compile the schema, [ValidateJSON] does.
func CheckResponseFormat(rf *ResponseFormat) error {
	if rf == nil {
		return nil
	}
	if !responseFormatName.MatchString(rf.EffectiveName()) {
		return fmt.Errorf("response_format name %q must be 1-64 letters, digits, '_' or '-'", rf.Name)
	}
	var obj map[string]json.RawMessage
	if err := json.Unmarshal(rf.Schema, &obj); err != nil || obj == nil {
		return errors.New("response_format schema must be a JSON object")
	}
	return nil
}

// StructuredResult is a validated structured completion.
//
// Response is the final attempt's response; Usage sums every attempt,
// repairs included (the embedded Response.Usage is the last attempt
// alone). Data is the output as JSON, validated against the schema.
type StructuredResult struct {
	Response
	Data     json.RawMessage
	Attempts int
	Usage    Usage
}

// ErrSchemaInvalid is what a [*SchemaError] matches under errors.Is.
var ErrSchemaInvalid = errors.New("llm: output does not match schema")

// SchemaError reports output that still broke the schema after the
// allowed repairs. It is not fallbackable: another provider is not
// tried, since the failure is the model's output, not the transport.
type SchemaError struct {
	// Attempts is the number of completions made.
	Attempts int
	// Violations are the last attempt's schema violations, each
	// "<json pointer>: <message>".
	Violations []string
	// Last is the last attempt's output, unmodified.
	Last string
}

func (e *SchemaError) Error() string {
	last := e.Last
	if len(last) > 200 {
		last = last[:200] + "..."
	}
	return fmt.Sprintf("llm: output does not match schema after %d attempt(s): %s; last output: %q",
		e.Attempts, strings.Join(e.Violations, "; "), last)
}

// Is makes errors.Is(err, ErrSchemaInvalid) hold.
func (e *SchemaError) Is(target error) bool { return target == ErrSchemaInvalid }

// StructuredOption tunes [CompleteStructured] and [CompleteInto].
type StructuredOption func(*structuredConfig)

type structuredConfig struct {
	maxRepairs int
	repairsSet bool
	lenient    bool
	strict     bool
}

// MaxRepairs sets how many times invalid output is sent back for
// correction, after the first completion; default 2. Zero makes one
// attempt.
func MaxRepairs(n int) StructuredOption {
	return func(c *structuredConfig) {
		if n < 0 {
			n = 0
		}
		c.maxRepairs, c.repairsSet = n, true
	}
}

// Lenient first strips a Markdown code fence and any prose around the
// JSON value, which models add despite instructions, before
// validating. It also turns the repair loop off (one attempt) unless
// [MaxRepairs] is given too.
func Lenient() StructuredOption {
	return func(c *structuredConfig) { c.lenient = true }
}

// Strict sets [ResponseFormat.Strict] in [CompleteInto]; a
// [CompleteStructured] caller sets it on the format itself.
func Strict() StructuredOption {
	return func(c *structuredConfig) { c.strict = true }
}

func newStructuredConfig(opts []StructuredOption) structuredConfig {
	c := structuredConfig{maxRepairs: defaultMaxRepairs}
	for _, o := range opts {
		o(&c)
	}
	if c.lenient && !c.repairsSet {
		c.maxRepairs = 0
	}
	return c
}

// CompleteStructured completes req with rf, validates the output
// against rf.Schema locally and, on a violation, re-asks: the bad
// output goes back as an assistant message, followed by a user message
// listing the violations, asking for corrected JSON only. After
// [MaxRepairs] re-asks it returns a [*SchemaError] with the result so
// far (Usage summed), so a caller still accounts for the spend.
//
// Validation always runs locally, whatever the provider enforced. A
// completion error is returned as is, with the usage spent so far.
func CompleteStructured(
	ctx context.Context, c Completer, req Request, rf ResponseFormat, opts ...StructuredOption,
) (StructuredResult, error) {
	cfg := newStructuredConfig(opts)
	return completeStructured(ctx, c, req, rf, cfg)
}

func completeStructured(
	ctx context.Context, c Completer, req Request, rf ResponseFormat, cfg structuredConfig,
) (StructuredResult, error) {
	if err := CheckResponseFormat(&rf); err != nil {
		return StructuredResult{}, err
	}
	if _, err := compileSchema(rf.Schema); err != nil {
		return StructuredResult{}, err
	}
	rf.Name = rf.EffectiveName()
	req = cloneRequest(req)
	req.ResponseFormat = &rf

	var out StructuredResult
	for attempt := 1; ; attempt++ {
		if err := ctx.Err(); err != nil {
			return out, err
		}
		resp, err := c.Complete(ctx, req)
		out.Attempts = attempt
		if err != nil {
			return out, err
		}
		out.Response = resp
		out.Usage = addUsage(out.Usage, resp.Usage)

		data, violations := checkOutput(rf.Schema, resp.Content, cfg.lenient)
		if len(violations) == 0 {
			out.Data = data
			return out, nil
		}
		if attempt > cfg.maxRepairs {
			return out, &SchemaError{Attempts: attempt, Violations: violations, Last: resp.Content}
		}
		req.Messages = append(req.Messages,
			Message{Role: "assistant", Content: resp.Content},
			Message{Role: "user", Content: repairPrompt(violations)})
	}
}

// checkOutput extracts the JSON value of text (lenient) or takes text
// whole, and returns it with its schema violations.
func checkOutput(schema json.RawMessage, text string, lenient bool) (json.RawMessage, []string) {
	raw := strings.TrimSpace(text)
	if lenient {
		raw = extractJSON(raw)
	}
	data := json.RawMessage(raw)
	v, err := ValidateJSON(schema, data)
	if err != nil {
		return nil, []string{err.Error()}
	}
	return data, v
}

const maxPromptViolations = 20

func repairPrompt(violations []string) string {
	var b strings.Builder
	b.WriteString("Your previous reply does not match the required JSON Schema:\n")
	for i, v := range violations {
		if i == maxPromptViolations {
			fmt.Fprintf(&b, "- ... and %d more\n", len(violations)-i)
			break
		}
		b.WriteString("- " + v + "\n")
	}
	b.WriteString("Reply again with the corrected JSON only: no code fence, no explanation.")
	return b.String()
}

func addUsage(a, b Usage) Usage {
	return Usage{
		PromptTokens:        a.PromptTokens + b.PromptTokens,
		CompletionTokens:    a.CompletionTokens + b.CompletionTokens,
		CacheReadTokens:     a.CacheReadTokens + b.CacheReadTokens,
		CacheCreationTokens: a.CacheCreationTokens + b.CacheCreationTokens,
		TotalTokens:         a.TotalTokens + b.TotalTokens,
	}
}

// CompleteInto is [CompleteStructured] for a Go type: the schema is
// reflected from T (invopop/jsonschema: json tags, "required" unless
// omitempty, additionalProperties false, no $ref) and the validated
// output is decoded into T. T is a struct, or a pointer to one.
func CompleteInto[T any](
	ctx context.Context, c Completer, req Request, opts ...StructuredOption,
) (T, StructuredResult, error) {
	var zero T
	cfg := newStructuredConfig(opts)
	rf, err := formatFor(reflect.TypeOf(zero), cfg.strict)
	if err != nil {
		return zero, StructuredResult{}, err
	}
	res, err := completeStructured(ctx, c, req, rf, cfg)
	if err != nil {
		return zero, res, err
	}
	var v T
	if err := json.Unmarshal(res.Data, &v); err != nil {
		return zero, res, fmt.Errorf("llm: decode structured output into %T: %w", v, err)
	}
	return v, res, nil
}

func formatFor(t reflect.Type, strict bool) (ResponseFormat, error) {
	if t == nil {
		return ResponseFormat{}, errors.New("llm: CompleteInto needs a struct type, got an interface")
	}
	if t.Kind() == reflect.Ptr {
		t = t.Elem()
	}
	if t.Kind() != reflect.Struct {
		return ResponseFormat{}, fmt.Errorf("llm: CompleteInto needs a struct type, got %s", t)
	}
	r := jsonschema.Reflector{DoNotReference: true, ExpandedStruct: true}
	raw, err := json.Marshal(r.ReflectFromType(t))
	if err != nil {
		return ResponseFormat{}, fmt.Errorf("llm: reflect schema of %s: %w", t, err)
	}
	// Providers take the schema body only, not its draft or id.
	var doc map[string]json.RawMessage
	if err := json.Unmarshal(raw, &doc); err != nil {
		return ResponseFormat{}, fmt.Errorf("llm: reflect schema of %s: %w", t, err)
	}
	delete(doc, "$schema")
	delete(doc, "$id")
	schema, err := json.Marshal(doc)
	if err != nil {
		return ResponseFormat{}, fmt.Errorf("llm: reflect schema of %s: %w", t, err)
	}
	name := t.Name()
	if !responseFormatName.MatchString(name) {
		name = DefaultResponseFormatName
	}
	return ResponseFormat{Name: name, Schema: schema, Strict: strict}, nil
}
