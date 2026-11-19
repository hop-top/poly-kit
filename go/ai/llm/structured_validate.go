package llm

import (
	"bytes"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"regexp"
	"sort"
	"strings"
	"sync"

	"github.com/santhosh-tekuri/jsonschema/v5"
)

// maxCompiled bounds the compiled-schema cache; a full cache is
// dropped whole, which only costs a recompile.
const maxCompiled = 256

var schemaCache = struct {
	sync.Mutex
	m map[[sha256.Size]byte]*jsonschema.Schema
}{m: map[[sha256.Size]byte]*jsonschema.Schema{}}

// compileSchema compiles schema, from a cache keyed by its bytes. The
// schema is self-contained: no $ref reaches the network or the disk.
func compileSchema(schema json.RawMessage) (*jsonschema.Schema, error) {
	key := sha256.Sum256(schema)
	schemaCache.Lock()
	s, ok := schemaCache.m[key]
	schemaCache.Unlock()
	if ok {
		return s, nil
	}

	c := jsonschema.NewCompiler()
	c.LoadURL = func(u string) (io.ReadCloser, error) {
		return nil, fmt.Errorf("remote reference %q is not loaded", u)
	}
	const url = "kit-llm://response-format.json"
	if err := c.AddResource(url, bytes.NewReader(schema)); err != nil {
		return nil, fmt.Errorf("llm: invalid response schema: %w", err)
	}
	s, err := c.Compile(url)
	if err != nil {
		return nil, fmt.Errorf("llm: invalid response schema: %w", err)
	}

	schemaCache.Lock()
	if len(schemaCache.m) >= maxCompiled {
		schemaCache.m = map[[sha256.Size]byte]*jsonschema.Schema{}
	}
	schemaCache.m[key] = s
	schemaCache.Unlock()
	return s, nil
}

// ValidateJSON checks data against schema (JSON Schema, 2020-12 unless
// the schema names a $schema) and returns the violations, each
// "<json pointer>: <message>", sorted; none means data conforms. Data
// that is not one JSON value is a violation. The error is for a schema
// that does not compile. Compiled schemas are cached by content.
func ValidateJSON(schema, data json.RawMessage) ([]string, error) {
	s, err := compileSchema(schema)
	if err != nil {
		return nil, err
	}
	dec := json.NewDecoder(bytes.NewReader(data))
	dec.UseNumber()
	var v any
	if err := dec.Decode(&v); err != nil {
		return []string{"output is not valid JSON: " + err.Error()}, nil
	}
	if _, err := dec.Token(); !errors.Is(err, io.EOF) {
		return []string{"output is not valid JSON: extra content after the first value"}, nil
	}
	err = s.Validate(v)
	if err == nil {
		return nil, nil
	}
	var ve *jsonschema.ValidationError
	if !errors.As(err, &ve) {
		return nil, err
	}
	seen := map[string]bool{}
	var out []string
	collectViolations(ve, seen, &out)
	sort.Strings(out)
	return out, nil
}

// collectViolations walks ve down to its leaves, the specific causes.
func collectViolations(ve *jsonschema.ValidationError, seen map[string]bool, out *[]string) {
	if len(ve.Causes) == 0 {
		loc := ve.InstanceLocation
		if loc == "" {
			loc = "/"
		}
		msg := loc + ": " + ve.Message
		if !seen[msg] {
			seen[msg] = true
			*out = append(*out, msg)
		}
		return
	}
	for _, c := range ve.Causes {
		collectViolations(c, seen, out)
	}
}

var fenceRe = regexp.MustCompile("(?s)```[A-Za-z0-9_-]*[ \t]*\r?\n?(.*?)```")

// extractJSON returns the JSON value inside text: the first Markdown
// code fence's content if there is one, then the first object or array
// that decodes, skipping prose around it. text is returned unchanged
// when no value is found.
func extractJSON(text string) string {
	body := text
	if m := fenceRe.FindStringSubmatch(text); m != nil {
		body = strings.TrimSpace(m[1])
	}
	if json.Valid([]byte(body)) {
		return body
	}
	for i := 0; i < len(body); i++ {
		if body[i] != '{' && body[i] != '[' {
			continue
		}
		var rm json.RawMessage
		if json.NewDecoder(strings.NewReader(body[i:])).Decode(&rm) == nil {
			return string(rm)
		}
	}
	return text
}
