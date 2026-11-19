package prompt

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"regexp"
	"strconv"
	"strings"
	"text/template"

	"gopkg.in/yaml.v3"

	"hop.top/kit/go/ai/llm"
)

// Ref is a template reference `name@N`.
type Ref struct {
	Name    string
	Version int
}

var refRe = regexp.MustCompile(`^([a-z0-9][a-z0-9._-]*)@([1-9][0-9]*)$`)

// ParseRef parses `name@N`: a lowercase name with no path separator and a
// version from 1 with no leading zero.
func ParseRef(s string) (Ref, error) {
	m := refRe.FindStringSubmatch(s)
	if m == nil {
		return Ref{}, fmt.Errorf("%w: prompt %q is not name@N (lowercase name, version from 1)", ErrInvalid, s)
	}
	v, err := strconv.Atoi(m[2])
	if err != nil {
		return Ref{}, fmt.Errorf("%w: prompt %q: version: %v", ErrInvalid, s, err)
	}
	return Ref{Name: m[1], Version: v}, nil
}

// String is `name@N`.
func (r Ref) String() string { return fmt.Sprintf("%s@%d", r.Name, r.Version) }

// File is the file name of the template: `name@N.yaml`.
func (r Ref) File() string { return r.String() + ".yaml" }

// Template is a loaded template file.
type Template struct {
	Ref
	// System is the file's system text, used verbatim.
	System string
	// Body is the file's `template:` text, a Go text/template.
	Body string
	// Hash is the sha256 of the raw file bytes, hex. A recorded run names
	// the prompt text it used by it.
	Hash string

	tpl *template.Template
}

// file is the YAML shape of a template file.
type file struct {
	System   string `yaml:"system"`
	Template string `yaml:"template"`
}

// ParseTemplate decodes a template file strictly (unknown keys are an
// error) and compiles its body. The body is required; system may be absent.
// A body calling anything outside join, trunc and default fails here.
func ParseTemplate(ref Ref, data []byte) (*Template, error) {
	var f file
	dec := yaml.NewDecoder(bytes.NewReader(data))
	dec.KnownFields(true)
	if err := dec.Decode(&f); err != nil {
		return nil, fmt.Errorf("%w: prompt %s: %v", ErrInvalid, ref, err)
	}
	if strings.TrimSpace(f.Template) == "" {
		return nil, fmt.Errorf("%w: prompt %s: template is empty", ErrInvalid, ref)
	}
	tpl, err := template.New(ref.String()).Funcs(funcs).Option("missingkey=error").Parse(f.Template)
	if err != nil {
		return nil, fmt.Errorf("%w: prompt %s: %v", ErrInvalid, ref, err)
	}
	sum := sha256.Sum256(data)
	return &Template{Ref: ref, System: f.System, Body: f.Template, Hash: hex.EncodeToString(sum[:]), tpl: tpl}, nil
}

// Render executes the body over vars. A key the body reads and vars lacks
// is an error, not an empty string.
func (t *Template) Render(vars map[string]any) (string, error) {
	if vars == nil {
		vars = map[string]any{}
	}
	var b bytes.Buffer
	if err := t.tpl.Execute(&b, vars); err != nil {
		return "", fmt.Errorf("%w: prompt %s: %v", ErrInvalid, t.Ref, err)
	}
	return b.String(), nil
}

// Messages renders the body and returns the chat turn pair: a system
// message when System is non-empty, then a user message holding the render.
func (t *Template) Messages(vars map[string]any) ([]llm.Message, error) {
	body, err := t.Render(vars)
	if err != nil {
		return nil, err
	}
	var out []llm.Message
	if t.System != "" {
		out = append(out, llm.Message{Role: "system", Content: t.System})
	}
	return append(out, llm.Message{Role: "user", Content: body}), nil
}

// funcs is the whole function set of a template: text helpers only,
// nothing that reads the machine. Argument order follows sprig.
var funcs = template.FuncMap{
	"join": func(sep string, v any) string {
		var parts []string
		switch t := v.(type) {
		case []string:
			parts = t
		case []any:
			for _, e := range t {
				parts = append(parts, fmt.Sprint(e))
			}
		case nil:
		default:
			parts = []string{fmt.Sprint(t)}
		}
		return strings.Join(parts, sep)
	},
	// trunc cuts s to at most n runes.
	"trunc": func(n int, s string) string {
		if n < 0 {
			n = 0
		}
		r := []rune(s)
		if len(r) <= n {
			return s
		}
		return string(r[:n])
	},
	// default returns d when v is nil, "" or an empty list or map.
	"default": func(d, v any) any {
		switch t := v.(type) {
		case nil:
			return d
		case string:
			if t == "" {
				return d
			}
		case []any:
			if len(t) == 0 {
				return d
			}
		case map[string]any:
			if len(t) == 0 {
				return d
			}
		}
		return v
	},
}
