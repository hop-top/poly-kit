package i18nv1

import (
	"bytes"
	"encoding/json"
	"os"
	"reflect"
	"regexp"
	"sort"
	"strings"
	"testing"

	"github.com/santhosh-tekuri/jsonschema/v5"
)

// Self-check of the i18n-v1 corpus: every catalog vector is validated against
// catalog.schema.json, and a minimal reference of the placeholder scanner and
// the RFC 4647 lookup proves the recorded expectations are consistent. Ports
// replay the same files; they do not import this package.

func readJSON(t *testing.T, name string, v any) {
	t.Helper()
	b, err := os.ReadFile(name)
	if err != nil {
		t.Fatal(err)
	}
	dec := json.NewDecoder(bytes.NewReader(b))
	dec.DisallowUnknownFields()
	if err := dec.Decode(v); err != nil {
		t.Fatalf("%s: %v", name, err)
	}
}

type segment struct {
	T *string `json:"t,omitempty"`
	P *string `json:"p,omitempty"`
}

type message struct {
	Params map[string]string `json:"params"`
	Text   []segment         `json:"text,omitempty"`
	Plural *struct {
		On       string               `json:"on"`
		Branches map[string][]segment `json:"branches"`
	} `json:"plural,omitempty"`
}

type catalogCase struct {
	Name      string             `json:"name"`
	Valid     bool               `json:"valid"`
	Doc       json.RawMessage    `json:"doc"`
	Raw       *string            `json:"raw"`
	Messages  map[string]message `json:"messages"`
	Code      string             `json:"code"`
	Key       string             `json:"key"`
	SchemaGap bool               `json:"schema_gap"`
}

type catalogCorpus struct {
	Comment []string      `json:"_comment"`
	Cases   []catalogCase `json:"cases"`
}

var identRE = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*$`)

// scan implements SPEC.md "Parameters": a run of n '%' immediately before '{'
// yields n/2 literal '%'; an odd run opens a placeholder.
func scan(s string) (segs []segment, err string) {
	var lit strings.Builder
	flush := func() {
		if lit.Len() > 0 {
			l := lit.String()
			segs = append(segs, segment{T: &l})
			lit.Reset()
		}
	}
	for i := 0; i < len(s); {
		if s[i] != '%' {
			lit.WriteByte(s[i])
			i++
			continue
		}
		j := i
		for j < len(s) && s[j] == '%' {
			j++
		}
		n := j - i
		if j >= len(s) || s[j] != '{' {
			lit.WriteString(strings.Repeat("%", n))
			i = j
			continue
		}
		lit.WriteString(strings.Repeat("%", n/2))
		if n%2 == 0 {
			i = j // the '{' is literal; the main loop writes it
			continue
		}
		end := strings.IndexByte(s[j:], '}')
		if end < 0 {
			return nil, "placeholder_malformed"
		}
		name := s[j+1 : j+end]
		if !identRE.MatchString(name) {
			return nil, "placeholder_malformed"
		}
		flush()
		segs = append(segs, segment{P: &name})
		i = j + end + 1
	}
	flush()
	return segs, ""
}

func used(segs []segment) []string {
	var out []string
	for _, s := range segs {
		if s.P != nil {
			out = append(out, *s.P)
		}
	}
	return out
}

// flatten covers the placeholder rules only; structural faults are the
// schema's job and are skipped here.
func flatten(prefix string, node any, out map[string]message) (code, key string) {
	join := func(k string) string {
		if prefix == "" {
			return k
		}
		return prefix + "." + k
	}
	switch v := node.(type) {
	case string:
		segs, e := scan(v)
		if e != "" {
			return e, prefix
		}
		if len(used(segs)) > 0 {
			return "placeholder_undeclared", prefix
		}
		out[prefix] = message{Params: map[string]string{}, Text: segs}
	case map[string]any:
		_, isText := v["$text"]
		_, isPlural := v["$plural"]
		if !isText && !isPlural {
			keys := make([]string, 0, len(v))
			for k := range v {
				keys = append(keys, k)
			}
			sort.Strings(keys)
			for _, k := range keys {
				if c, kk := flatten(join(k), v[k], out); c != "" {
					return c, kk
				}
			}
			return "", ""
		}
		params := map[string]string{}
		if p, ok := v["$params"].(map[string]any); ok {
			for k, t := range p {
				params[k] = t.(string)
			}
		}
		check := func(segs []segment) (string, string) {
			for _, u := range used(segs) {
				if _, ok := params[u]; !ok {
					return "placeholder_undeclared", prefix
				}
			}
			return "", ""
		}
		if isText {
			segs, e := scan(v["$text"].(string))
			if e != "" {
				return e, prefix
			}
			if c, k := check(segs); c != "" {
				return c, k
			}
			out[prefix] = message{Params: params, Text: segs}
			return "", ""
		}
		pl := v["$plural"].(map[string]any)
		on := pl["on"].(string)
		if _, ok := params[on]; !ok {
			return "plural_param_undeclared", prefix
		}
		if params[on] != "number" && params[on] != "integer" {
			return "plural_param_type", prefix
		}
		m := message{Params: params}
		m.Plural = &struct {
			On       string               `json:"on"`
			Branches map[string][]segment `json:"branches"`
		}{On: on, Branches: map[string][]segment{}}
		for cat, txt := range pl["branches"].(map[string]any) {
			segs, e := scan(txt.(string))
			if e != "" {
				return e, prefix
			}
			if c, k := check(segs); c != "" {
				return c, k
			}
			m.Plural.Branches[cat] = segs
		}
		out[prefix] = m
	}
	return "", ""
}

func TestCatalogCorpus(t *testing.T) {
	var c catalogCorpus
	readJSON(t, "catalog-corpus.json", &c)
	if len(c.Cases) == 0 {
		t.Fatal("empty corpus")
	}
	schema, err := jsonschema.NewCompiler().Compile("catalog.schema.json")
	if err != nil {
		t.Fatal(err)
	}
	seen := map[string]bool{}
	for _, tc := range c.Cases {
		t.Run(tc.Name, func(t *testing.T) {
			if seen[tc.Name] {
				t.Fatalf("duplicate case name")
			}
			seen[tc.Name] = true
			if (tc.Doc == nil) == (tc.Raw == nil) {
				t.Fatalf("exactly one of doc and raw")
			}
			if tc.Valid && (tc.Code != "" || tc.Messages == nil) {
				t.Fatalf("valid case needs messages and no code")
			}
			if !tc.Valid && (tc.Code == "" || tc.Messages != nil) {
				t.Fatalf("invalid case needs code and no messages")
			}
			if tc.Raw != nil {
				if !tc.SchemaGap {
					t.Fatalf("raw case must be schema_gap")
				}
				return
			}
			var doc any
			if err := json.Unmarshal(tc.Doc, &doc); err != nil {
				t.Fatal(err)
			}
			verr := schema.Validate(doc)
			if tc.Valid && verr != nil {
				t.Fatalf("schema rejects valid doc: %v", verr)
			}
			if !tc.Valid && !tc.SchemaGap && verr == nil {
				t.Fatalf("schema accepts invalid doc %s", tc.Code)
			}
			if !tc.Valid && tc.SchemaGap && verr != nil {
				t.Fatalf("schema_gap but schema rejects: %v", verr)
			}
			if verr != nil {
				return // the schema owns structural faults
			}
			msgs := doc.(map[string]any)["messages"].(map[string]any)
			got := map[string]message{}
			code, key := flatten("", msgs, got)
			if tc.Valid {
				if code != "" {
					t.Fatalf("reference rejects valid doc: %s at %q", code, key)
				}
				if !reflect.DeepEqual(norm(t, got), norm(t, tc.Messages)) {
					t.Fatalf("flat messages differ:\n got %v\nwant %v", norm(t, got), norm(t, tc.Messages))
				}
				return
			}
			if strings.HasPrefix(tc.Code, "placeholder_") || strings.HasPrefix(tc.Code, "plural_param") {
				if code != tc.Code || key != tc.Key {
					t.Fatalf("reference: %s at %q, want %s at %q", code, key, tc.Code, tc.Key)
				}
			}
		})
	}
}

func norm(t *testing.T, v any) any {
	t.Helper()
	b, err := json.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	var out any
	if err := json.Unmarshal(b, &out); err != nil {
		t.Fatal(err)
	}
	return out
}

var (
	langRE   = regexp.MustCompile(`^[a-z]{2,3}$|^[a-z]{5,8}$`)
	scriptRE = regexp.MustCompile(`^[a-z]{4}$`)
	regionRE = regexp.MustCompile(`^[a-z]{2}$|^[0-9]{3}$`)
	varRE    = regexp.MustCompile(`^[a-z0-9]{5,8}$|^[0-9][a-z0-9]{3}$`)
	extRE    = regexp.MustCompile(`^[a-z0-9]{2,8}$`)
	privRE   = regexp.MustCompile(`^[a-z0-9]{1,8}$`)
)

// canon implements SPEC.md "Locale tags".
func canon(tag string) (string, bool) {
	if tag == "" || strings.ContainsAny(tag, "_ ") {
		return "", false
	}
	p := strings.Split(strings.ToLower(tag), "-")
	for _, s := range p {
		if s == "" {
			return "", false
		}
		for _, r := range s {
			if !(r >= 'a' && r <= 'z' || r >= '0' && r <= '9') {
				return "", false
			}
		}
	}
	if !langRE.MatchString(p[0]) {
		return "", false
	}
	out := []string{p[0]}
	i := 1
	if i < len(p) && scriptRE.MatchString(p[i]) {
		out = append(out, strings.ToUpper(p[i][:1])+p[i][1:])
		i++
	}
	if i < len(p) && regionRE.MatchString(p[i]) {
		out = append(out, strings.ToUpper(p[i]))
		i++
	}
	for i < len(p) && varRE.MatchString(p[i]) {
		out = append(out, p[i])
		i++
	}
	for i < len(p) && len(p[i]) == 1 && p[i] != "x" {
		out = append(out, p[i])
		i++
		n := 0
		for i < len(p) && extRE.MatchString(p[i]) && len(p[i]) >= 2 {
			out = append(out, p[i])
			i++
			n++
		}
		if n == 0 {
			return "", false
		}
	}
	if i < len(p) && p[i] == "x" {
		out = append(out, "x")
		i++
		n := 0
		for i < len(p) && privRE.MatchString(p[i]) {
			out = append(out, p[i])
			i++
			n++
		}
		if n == 0 {
			return "", false
		}
	}
	if i != len(p) {
		return "", false
	}
	return strings.Join(out, "-"), true
}

// lookup is RFC 4647 section 3.4 truncation.
func lookup(tag string) []string {
	p := strings.Split(tag, "-")
	var out []string
	for len(p) > 0 {
		out = append(out, strings.Join(p, "-"))
		p = p[:len(p)-1]
		if len(p) > 0 && len(p[len(p)-1]) == 1 {
			p = p[:len(p)-1]
		}
	}
	return out
}

func chain(tag, def string) ([]string, bool) {
	t, ok := canon(tag)
	if !ok {
		return nil, false
	}
	d, ok := canon(def)
	if !ok {
		return nil, false
	}
	var out []string
	seen := map[string]bool{}
	for _, c := range append(lookup(t), lookup(d)...) {
		if !seen[c] {
			seen[c] = true
			out = append(out, c)
		}
	}
	return out, true
}

type fallbackFile struct {
	Comment      []string `json:"_comment"`
	Canonicalize []struct {
		In    string `json:"in"`
		Out   string `json:"out"`
		Error string `json:"error"`
	} `json:"canonicalize"`
	Chain []struct {
		Tag     string   `json:"tag"`
		Default string   `json:"default"`
		Chain   []string `json:"chain"`
		Error   string   `json:"error"`
	} `json:"chain"`
	Resolve []struct {
		Tag       string   `json:"tag"`
		Available []string `json:"available"`
		Default   string   `json:"default"`
		Want      *string  `json:"want"`
		Error     string   `json:"error"`
	} `json:"resolve"`
	Lookup []struct {
		Tag      string              `json:"tag"`
		Default  string              `json:"default"`
		Catalogs map[string][]string `json:"catalogs"`
		Keys     map[string]*string  `json:"keys"`
	} `json:"lookup"`
}

func firstAvailable(ch []string, avail map[string]bool) *string {
	for _, c := range ch {
		if avail[c] {
			c := c
			return &c
		}
	}
	return nil
}

func TestFallback(t *testing.T) {
	var f fallbackFile
	readJSON(t, "fallback.json", &f)
	for _, c := range f.Canonicalize {
		got, ok := canon(c.In)
		if c.Error != "" {
			if ok {
				t.Errorf("canonicalize %q: accepted as %q, want %s", c.In, got, c.Error)
			}
			continue
		}
		if !ok || got != c.Out {
			t.Errorf("canonicalize %q = %q (ok=%v), want %q", c.In, got, ok, c.Out)
		}
	}
	for _, c := range f.Chain {
		got, ok := chain(c.Tag, c.Default)
		if c.Error != "" {
			if ok {
				t.Errorf("chain %q/%q: accepted, want %s", c.Tag, c.Default, c.Error)
			}
			continue
		}
		if !ok || !reflect.DeepEqual(got, c.Chain) {
			t.Errorf("chain %q/%q = %v, want %v", c.Tag, c.Default, got, c.Chain)
		}
	}
	for _, c := range f.Resolve {
		ch, ok := chain(c.Tag, c.Default)
		if c.Error != "" {
			if ok {
				t.Errorf("resolve %q: accepted, want %s", c.Tag, c.Error)
			}
			continue
		}
		avail := map[string]bool{}
		for _, a := range c.Available {
			ca, ok := canon(a)
			if !ok {
				t.Fatalf("available %q invalid", a)
			}
			avail[ca] = true
		}
		got := firstAvailable(ch, avail)
		if (got == nil) != (c.Want == nil) || (got != nil && *got != *c.Want) {
			t.Errorf("resolve %q in %v = %v, want %v", c.Tag, c.Available, got, c.Want)
		}
	}
	for _, c := range f.Lookup {
		ch, ok := chain(c.Tag, c.Default)
		if !ok {
			t.Fatalf("lookup %q: bad tag", c.Tag)
		}
		for key, want := range c.Keys {
			var got *string
			for _, loc := range ch {
				for _, k := range c.Catalogs[loc] {
					if k == key {
						loc := loc
						got = &loc
						break
					}
				}
				if got != nil {
					break
				}
			}
			if (got == nil) != (want == nil) || (got != nil && *got != *want) {
				t.Errorf("lookup %q key %q = %v, want %v", c.Tag, key, got, want)
			}
		}
	}
}

func TestPluralCorpus(t *testing.T) {
	var p struct {
		Comment []string `json:"_comment"`
		CLDRMin string   `json:"cldr_min"`
		Cases   []struct {
			Locale   string `json:"locale"`
			N        string `json:"n"`
			Category string `json:"category"`
		} `json:"cases"`
	}
	readJSON(t, "plural.json", &p)
	cats := map[string]bool{"zero": true, "one": true, "two": true, "few": true, "many": true, "other": true}
	num := regexp.MustCompile(`^(0|[1-9][0-9]*)(\.[0-9]*[1-9])?$`)
	seen := map[string]bool{}
	for _, c := range p.Cases {
		if _, ok := canon(c.Locale); !ok {
			t.Errorf("locale %q invalid", c.Locale)
		}
		if !cats[c.Category] {
			t.Errorf("%s %s: category %q", c.Locale, c.N, c.Category)
		}
		if !num.MatchString(c.N) {
			t.Errorf("%s: n %q not a plain decimal without trailing zeros", c.Locale, c.N)
		}
		k := c.Locale + "/" + c.N
		if seen[k] {
			t.Errorf("duplicate %s", k)
		}
		seen[k] = true
	}
}
