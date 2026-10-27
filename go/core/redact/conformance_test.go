package redact_test

import (
	"bytes"
	"encoding/json"
	"flag"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"hop.top/kit/go/core/redact"
)

// Runs the shared corpus in contracts/redact-v1. The vectors are the
// source of truth: a failure means this implementation drifted from
// the contract, not that a vector should be edited to fit.

const (
	redactConfDir = "../../../contracts/redact-v1"
	// redactRustRules is the kit-rs copy of rules.json, embedded by the
	// Rust redactor; it must stay byte-identical.
	redactRustRules = "../../../sdk/experimental/rs/src/redact/rules.json"
)

var updateRedactRules = flag.Bool("update", false, "rewrite contracts/redact-v1/rules.json and the kit-rs copy from EmbeddedPolicy")

var redactConfFiles = map[string]bool{"README.md": true, "rules.json": true, "vectors.json": true}

type confSpan struct {
	Rule  string `json:"rule"`
	Start int    `json:"start"`
	End   int    `json:"end"`
}

type confVector struct {
	Name           string                 `json:"name"`
	Note           string                 `json:"note,omitempty"`
	Rules          []redact.RuleSpec      `json:"rules,omitempty"`
	Allowlists     []redact.AllowlistSpec `json:"allowlists,omitempty"`
	AllowlistExact []string               `json:"allowlist_exact,omitempty"`
	Strategy       string                 `json:"strategy"`
	Input          string                 `json:"input"`
	Pad            *struct {
		Text  string `json:"text"`
		Times int    `json:"times"`
	} `json:"pad,omitempty"`
	Findings *[]confSpan `json:"findings,omitempty"`
	Spans    []confSpan  `json:"spans"`
	Output   string      `json:"output"`
}

type confFile struct {
	Version     string       `json:"version"`
	Description string       `json:"description"`
	Vectors     []confVector `json:"vectors"`
}

func marshalPolicy(t *testing.T, p redact.Policy) []byte {
	t.Helper()
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false)
	enc.SetIndent("", "  ")
	if err := enc.Encode(p); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

func strictDecode(t *testing.T, path string, v any) {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	dec := json.NewDecoder(bytes.NewReader(b))
	dec.DisallowUnknownFields()
	if err := dec.Decode(v); err != nil {
		t.Fatalf("%s: %v", path, err)
	}
}

// rules.json is EmbeddedPolicy, byte for byte, and so is the copy the
// Rust port embeds.
func TestConformanceRedactRules(t *testing.T) {
	entries, err := os.ReadDir(filepath.FromSlash(redactConfDir))
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range entries {
		if !redactConfFiles[e.Name()] {
			t.Errorf("corpus file %s has no runner", e.Name())
		}
	}
	p, err := redact.EmbeddedPolicy()
	if err != nil {
		t.Fatal(err)
	}
	want := marshalPolicy(t, p)
	paths := []string{filepath.Join(redactConfDir, "rules.json"), redactRustRules}
	for _, path := range paths {
		path = filepath.FromSlash(path)
		if *updateRedactRules {
			if err := os.WriteFile(path, want, 0o644); err != nil {
				t.Fatal(err)
			}
			continue
		}
		got, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		if !bytes.Equal(got, want) {
			t.Errorf("%s is stale; run go test ./go/core/redact/ -run TestConformanceRedactRules -update", path)
		}
	}
	var dumped redact.Policy
	strictDecode(t, filepath.Join(redactConfDir, "rules.json"), &dumped)
	if _, err := redact.NewFromPolicy(dumped); err != nil {
		t.Fatal(err)
	}
}

func TestConformanceRedactVectors(t *testing.T) {
	var f confFile
	strictDecode(t, filepath.Join(redactConfDir, "vectors.json"), &f)
	if f.Version != "v1" {
		t.Fatalf("version %q, want v1", f.Version)
	}
	def, err := redact.EmbeddedPolicy()
	if err != nil {
		t.Fatal(err)
	}
	names := map[string]bool{}
	for _, v := range f.Vectors {
		if names[v.Name] {
			t.Fatalf("duplicate vector %q", v.Name)
		}
		names[v.Name] = true
		t.Run(v.Name, func(t *testing.T) {
			p := def
			if v.Rules != nil {
				p = redact.Policy{Rules: v.Rules, Allowlists: v.Allowlists, AllowlistExact: v.AllowlistExact}
			}
			r, err := redact.NewFromPolicy(p)
			if err != nil {
				t.Fatal(err)
			}
			strategy := map[string]redact.Replacement{"mask": redact.Mask, "tag": redact.Tag, "hash": redact.Hash}
			s, ok := strategy[v.Strategy]
			if !ok {
				t.Fatalf("unknown strategy %q", v.Strategy)
			}
			if _, err := r.SetReplacement(s); err != nil {
				t.Fatal(err)
			}
			in, want, off := v.Input, v.Output, 0
			if v.Pad != nil {
				pad := strings.Repeat(v.Pad.Text, v.Pad.Times)
				in, want, off = pad+in+pad, pad+want+pad, len(pad)
			}
			if v.Findings != nil {
				var got []confSpan
				for _, m := range r.Scan(in) {
					got = append(got, confSpan{m.RuleID, m.Start - off, m.End - off})
				}
				assertSpans(t, "findings", got, *v.Findings)
			}
			var spans []confSpan
			for _, sp := range r.Spans(in) {
				spans = append(spans, confSpan{sp.RuleID, sp.Start - off, sp.End - off})
			}
			assertSpans(t, "spans", spans, v.Spans)
			if got := r.Apply(in); got != want {
				t.Errorf("output\n got %q\nwant %q", trim(got, off), trim(want, off))
			}
			if got := string(r.ApplyBytes([]byte(in))); got != want {
				t.Errorf("ApplyBytes output %q", trim(got, off))
			}
		})
	}
}

func trim(s string, off int) string {
	if off == 0 || len(s) < 2*off {
		return s
	}
	return s[off : len(s)-off]
}

func assertSpans(t *testing.T, what string, got, want []confSpan) {
	t.Helper()
	if len(got) == 0 && len(want) == 0 {
		return
	}
	g, _ := json.Marshal(got)
	w, _ := json.Marshal(want)
	if !bytes.Equal(g, w) {
		t.Errorf("%s\n got %s\nwant %s", what, g, w)
	}
}
