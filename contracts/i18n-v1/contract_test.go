package i18nv1

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"strings"
	"testing"

	"github.com/santhosh-tekuri/jsonschema/v5"
)

// Self-check of the i18n-v1 files. It validates structure only: catalogs,
// plural vectors and locale vectors against their schemas, the vendored
// MessageFormat 2 suite against upstream's test schema and a recorded hash.
// It does not parse MessageFormat 2, compute plural categories or build
// fallback chains; ports replay the files against their platform libraries.

func readFile(t *testing.T, name string) []byte {
	t.Helper()
	b, err := os.ReadFile(name)
	if err != nil {
		t.Fatal(err)
	}
	return b
}

func readJSON(t *testing.T, name string, v any) {
	t.Helper()
	dec := json.NewDecoder(bytes.NewReader(readFile(t, name)))
	dec.DisallowUnknownFields()
	if err := dec.Decode(v); err != nil {
		t.Fatalf("%s: %v", name, err)
	}
}

func compile(t *testing.T, name string) *jsonschema.Schema {
	t.Helper()
	s, err := jsonschema.NewCompiler().Compile(name)
	if err != nil {
		t.Fatalf("%s: %v", name, err)
	}
	return s
}

func validateFile(t *testing.T, s *jsonschema.Schema, name string) {
	t.Helper()
	var doc any
	if err := json.Unmarshal(readFile(t, name), &doc); err != nil {
		t.Fatalf("%s: %v", name, err)
	}
	if err := s.Validate(doc); err != nil {
		t.Errorf("%s: %v", name, err)
	}
}

type message struct {
	Msg  string `json:"msg"`
	Desc string `json:"desc,omitempty"`
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

// flatten walks the catalog tree into dotted keys. Structure only: a string
// is a message, an object with $msg is a leaf, anything else a namespace.
func flatten(prefix string, node any, out map[string]message) {
	join := func(k string) string {
		if prefix == "" {
			return k
		}
		return prefix + "." + k
	}
	switch v := node.(type) {
	case string:
		out[prefix] = message{Msg: v}
	case map[string]any:
		if m, ok := v["$msg"]; ok {
			d, _ := v["$desc"].(string)
			out[prefix] = message{Msg: m.(string), Desc: d}
			return
		}
		for k, c := range v {
			flatten(join(k), c, out)
		}
	}
}

var invalidCodes = map[string]bool{
	"invalid_json": true, "duplicate_key": true, "invalid_document": true,
	"unsupported_version": true, "invalid_locale": true, "invalid_key": true,
	"unknown_reserved_key": true, "empty_namespace": true, "invalid_node": true,
	"invalid_message": true,
}

func TestCatalogCorpus(t *testing.T) {
	var c catalogCorpus
	readJSON(t, "catalog-corpus.json", &c)
	if len(c.Cases) == 0 {
		t.Fatal("empty corpus")
	}
	schema := compile(t, "catalog.schema.json")
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
			if tc.Valid && (tc.Code != "" || tc.Messages == nil || tc.SchemaGap) {
				t.Fatalf("valid case needs messages, no code, no schema_gap")
			}
			if !tc.Valid && (tc.Code == "" || tc.Messages != nil) {
				t.Fatalf("invalid case needs code and no messages")
			}
			if !tc.Valid && !invalidCodes[tc.Code] {
				t.Fatalf("unknown code %q", tc.Code)
			}
			if tc.Code == "invalid_message" && !tc.SchemaGap {
				t.Fatalf("invalid_message is a schema gap by definition")
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
			if !tc.Valid {
				return
			}
			got := map[string]message{}
			flatten("", doc.(map[string]any)["messages"], got)
			if !reflect.DeepEqual(got, tc.Messages) {
				t.Fatalf("flat messages differ:\n got %v\nwant %v", got, tc.Messages)
			}
		})
	}
}

type pluralFile struct {
	CLDRMin string `json:"cldr_min"`
	Cases   []struct {
		Locale   string `json:"locale"`
		Type     string `json:"type"`
		N        string `json:"n"`
		Category string `json:"category"`
		Since    string `json:"since"`
	} `json:"cases"`
}

func TestPluralVectors(t *testing.T) {
	validateFile(t, compile(t, "plural.schema.json"), "plural.json")
	var p pluralFile
	var raw map[string]json.RawMessage
	if err := json.Unmarshal(readFile(t, "plural.json"), &raw); err != nil {
		t.Fatal(err)
	}
	delete(raw, "_comment")
	b, _ := json.Marshal(raw)
	if err := json.Unmarshal(b, &p); err != nil {
		t.Fatal(err)
	}
	seen := map[string]bool{}
	cats := map[string]bool{}
	types := map[string]bool{}
	for _, c := range p.Cases {
		k := c.Type + "/" + c.Locale + "/" + c.N
		if seen[k] {
			t.Errorf("duplicate %s", k)
		}
		seen[k] = true
		cats[c.Type+"/"+c.Category] = true
		types[c.Type] = true
		if c.Since != "" && c.Since <= p.CLDRMin {
			t.Errorf("%s: since %s not above cldr_min %s", k, c.Since, p.CLDRMin)
		}
	}
	for _, want := range []string{"cardinal/many", "cardinal/zero", "cardinal/few", "cardinal/two", "ordinal/one", "ordinal/two", "ordinal/few", "ordinal/other"} {
		if !cats[want] {
			t.Errorf("no vector for %s", want)
		}
	}
	if len(types) != 2 {
		t.Errorf("want cardinal and ordinal vectors, got %v", types)
	}
}

func TestFallbackVectors(t *testing.T) {
	validateFile(t, compile(t, "fallback.schema.json"), "fallback.json")
	var f struct {
		Lookup []struct {
			Tag      string              `json:"tag"`
			Catalogs map[string][]string `json:"catalogs"`
			Keys     map[string]*string  `json:"keys"`
		} `json:"lookup"`
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
	}
	var raw map[string]json.RawMessage
	if err := json.Unmarshal(readFile(t, "fallback.json"), &raw); err != nil {
		t.Fatal(err)
	}
	delete(raw, "_comment")
	b, _ := json.Marshal(raw)
	dec := json.NewDecoder(bytes.NewReader(b))
	if err := dec.Decode(&f); err != nil {
		t.Fatal(err)
	}
	// A lookup answer must name a catalog that defines the key.
	for _, l := range f.Lookup {
		for key, loc := range l.Keys {
			if loc == nil {
				continue
			}
			ok := false
			for _, k := range l.Catalogs[*loc] {
				ok = ok || k == key
			}
			if !ok {
				t.Errorf("lookup %q: key %q answered by %q, which does not define it", l.Tag, key, *loc)
			}
		}
	}
	// A chain row starts with the canonical request and a resolve answer for
	// the same request is a chain member (recorded data agreeing with itself,
	// not a chain computation).
	chains := map[string][]string{}
	for _, c := range f.Chain {
		if c.Error == "" {
			if len(c.Chain) == 0 {
				t.Errorf("chain %q: empty", c.Tag)
			}
			chains[c.Tag+"\x00"+c.Default] = c.Chain
		}
	}
	for _, r := range f.Resolve {
		ch, ok := chains[r.Tag+"\x00"+r.Default]
		if !ok || r.Want == nil || r.Error != "" {
			continue
		}
		in := false
		for _, c := range ch {
			in = in || c == *r.Want
		}
		if !in {
			t.Errorf("resolve %q: want %q not in recorded chain %v", r.Tag, *r.Want, ch)
		}
	}
}

type suiteSource struct {
	Repo     string            `json:"repo"`
	Tag      string            `json:"tag"`
	Commit   string            `json:"commit"`
	License  string            `json:"license"`
	SHA256   map[string]string `json:"sha256"`
	Upstream string            `json:"upstream_path"`
	Comment  []string          `json:"_comment"`
}

func TestMF2SuiteVendored(t *testing.T) {
	const dir = "mf2-suite"
	var src suiteSource
	readJSON(t, filepath.Join(dir, "source.json"), &src)
	if src.Tag != "LDML48.2" || len(src.Commit) != 40 || src.License == "" || src.Repo == "" {
		t.Fatalf("source.json incomplete: %+v", src)
	}
	if !strings.Contains(string(readFile(t, filepath.Join(dir, "LICENSE"))), "UNICODE LICENSE V3") {
		t.Errorf("mf2-suite/LICENSE is not the Unicode License")
	}

	// Every vendored file matches its recorded hash, and nothing else lives
	// under tests/ or schemas/.
	var onDisk []string
	for _, sub := range []string{"tests", "schemas"} {
		err := filepath.WalkDir(filepath.Join(dir, sub), func(p string, d os.DirEntry, err error) error {
			if err != nil || d.IsDir() {
				return err
			}
			rel, _ := filepath.Rel(dir, p)
			onDisk = append(onDisk, filepath.ToSlash(rel))
			return nil
		})
		if err != nil {
			t.Fatal(err)
		}
	}
	sort.Strings(onDisk)
	var recorded []string
	for f := range src.SHA256 {
		recorded = append(recorded, f)
	}
	sort.Strings(recorded)
	if !reflect.DeepEqual(onDisk, recorded) {
		t.Fatalf("files on disk %v differ from source.json %v", onDisk, recorded)
	}
	for _, f := range onDisk {
		sum := sha256.Sum256(readFile(t, filepath.Join(dir, f)))
		if got := hex.EncodeToString(sum[:]); got != src.SHA256[f] {
			t.Errorf("%s: sha256 %s, recorded %s (vendored files are not edited; see mf2-suite/README.md)", f, got, src.SHA256[f])
		}
	}

	// Every test file parses against upstream's schema.
	schema := compile(t, filepath.Join(dir, "schemas/v0/tests.schema.json"))
	n := 0
	for _, f := range onDisk {
		if strings.HasPrefix(f, "tests/") {
			validateFile(t, schema, filepath.Join(dir, f))
			n++
		}
	}
	if n == 0 {
		t.Fatal("no suite files")
	}
}
