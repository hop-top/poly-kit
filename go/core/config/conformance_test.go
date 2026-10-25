package config

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strconv"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
)

// Runs the shared corpus in contracts/config-v1. The vectors are the
// source of truth: a failure here means this implementation drifted
// from the contract, not that the vector should be edited to fit.

var confFiles = map[string]bool{
	"parse_scalar.json": true,
	"resolve.json":      true,
	"overrides.json":    true,
	"precedence.json":   true,
	"load.json":         true,
	"edit.json":         true,
}

const confDir = "../../../contracts/config-v1"

type confHeader struct {
	Schema      string `json:"$schema"`
	ID          string `json:"$id"`
	Version     string `json:"version"`
	Description string `json:"description"`
}

func (h confHeader) check() error {
	if h.Version != "v1" {
		return fmt.Errorf("version = %q, want v1", h.Version)
	}
	return nil
}

type confChecker interface{ check() error }

func confLoad(t *testing.T, name string, v confChecker) {
	t.Helper()
	if !confFiles[name] {
		t.Fatalf("%s is not registered in confFiles", name)
	}
	p := filepath.Join(filepath.FromSlash(confDir), name)
	raw, err := os.ReadFile(p)
	if err != nil {
		t.Fatalf("read %s: %v", p, err)
	}
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.DisallowUnknownFields()
	if err := dec.Decode(v); err != nil {
		t.Fatalf("parse %s: %v", p, err)
	}
	if err := v.check(); err != nil {
		t.Fatalf("%s: %v", name, err)
	}
}

func TestConformanceAllFilesConsumed(t *testing.T) {
	entries, err := os.ReadDir(filepath.FromSlash(confDir))
	if err != nil {
		t.Fatal(err)
	}
	seen := map[string]bool{}
	for _, e := range entries {
		if filepath.Ext(e.Name()) != ".json" {
			continue
		}
		seen[e.Name()] = true
		if !confFiles[e.Name()] {
			t.Errorf("%s has no runner", e.Name())
		}
	}
	for n := range confFiles {
		if !seen[n] {
			t.Errorf("%s registered but missing from the corpus", n)
		}
	}
}

// confEncode renders v in the corpus encoding, as canonical JSON.
func confEncode(v any) any {
	switch x := v.(type) {
	case nil:
		return map[string]any{"kind": "null", "repr": "<nil>"}
	case bool:
		return map[string]any{"kind": "bool", "repr": fmt.Sprint(x)}
	case int:
		return map[string]any{"kind": "int", "repr": fmt.Sprint(x)}
	case uint64:
		return map[string]any{"kind": "uint", "repr": fmt.Sprint(x)}
	case float64:
		return map[string]any{"kind": "float", "repr": fmt.Sprint(x)}
	case string:
		return map[string]any{"kind": "string", "repr": x}
	case []any:
		out := make([]any, len(x))
		for i, e := range x {
			out[i] = confEncode(e)
		}
		return map[string]any{"seq": out}
	case map[string]any:
		out := map[string]any{}
		for k, e := range x {
			out[k] = confEncode(e)
		}
		return map[string]any{"map": out}
	}
	return map[string]any{"kind": fmt.Sprintf("%T", v), "repr": fmt.Sprint(v)}
}

// confDecode turns a corpus value into the Go value it names.
func confDecode(t *testing.T, raw json.RawMessage) any {
	t.Helper()
	var v struct {
		Kind string                     `json:"kind"`
		Repr string                     `json:"repr"`
		Seq  []json.RawMessage          `json:"seq"`
		Map  map[string]json.RawMessage `json:"map"`
	}
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&v); err != nil {
		t.Fatalf("value %s: %v", raw, err)
	}
	switch {
	case v.Seq != nil:
		out := make([]any, len(v.Seq))
		for i, e := range v.Seq {
			out[i] = confDecode(t, e)
		}
		return out
	case v.Map != nil:
		out := map[string]any{}
		for k, e := range v.Map {
			out[k] = confDecode(t, e)
		}
		return out
	}
	switch v.Kind {
	case "null":
		return nil
	case "bool":
		return v.Repr == "true"
	case "int":
		i, err := strconv.Atoi(v.Repr)
		if err != nil {
			t.Fatalf("int %q: %v", v.Repr, err)
		}
		return i
	case "float":
		f, err := strconv.ParseFloat(v.Repr, 64)
		if err != nil {
			t.Fatalf("float %q: %v", v.Repr, err)
		}
		return f
	case "string":
		return v.Repr
	}
	t.Fatalf("unknown kind %q", v.Kind)
	return nil
}

// confSame compares a got value with a corpus value by encoding.
func confSame(t *testing.T, got any, want json.RawMessage) (string, bool) {
	t.Helper()
	g, _ := json.Marshal(confEncode(got))
	var w any
	if err := json.Unmarshal(want, &w); err != nil {
		t.Fatal(err)
	}
	wb, _ := json.Marshal(w)
	return string(g), bytes.Equal(g, wb)
}

func TestConformanceParseScalar(t *testing.T) {
	var f struct {
		confHeader
		Cases []struct {
			Name  string          `json:"name"`
			Input string          `json:"input"`
			Want  json.RawMessage `json:"want"`
		} `json:"cases"`
	}
	confLoad(t, "parse_scalar.json", &f)
	if len(f.Cases) == 0 {
		t.Fatal("no cases")
	}
	for _, c := range f.Cases {
		got := ParseScalar(c.Input)
		if g, ok := confSame(t, got, c.Want); !ok {
			t.Errorf("%s: ParseScalar(%q) = %s, want %s", c.Name, c.Input, g, c.Want)
		}
	}
}

func TestConformanceResolve(t *testing.T) {
	var f struct {
		confHeader
		Cases []struct {
			Name string          `json:"name"`
			YAML string          `json:"yaml"`
			Want json.RawMessage `json:"want"`
		} `json:"cases"`
	}
	confLoad(t, "resolve.json", &f)
	for _, c := range f.Cases {
		var doc yaml.Node
		if err := yaml.Unmarshal([]byte("v: "+c.YAML), &doc); err != nil {
			t.Fatalf("%s: %v", c.Name, err)
		}
		got := nodeToValue(walkPath(&doc, "v"))
		if g, ok := confSame(t, got, c.Want); !ok {
			t.Errorf("%s: %q = %s, want %s", c.Name, c.YAML, g, c.Want)
		}
	}
}

func TestConformanceOverrides(t *testing.T) {
	var f struct {
		confHeader
		Cases []struct {
			Name  string          `json:"name"`
			Pairs []string        `json:"pairs"`
			OK    bool            `json:"ok"`
			Want  json.RawMessage `json:"want"`
			Error string          `json:"error"`
		} `json:"cases"`
	}
	confLoad(t, "overrides.json", &f)
	for _, c := range f.Cases {
		got, err := ParseOverrides(c.Pairs)
		if !c.OK {
			if err == nil || err.Error() != c.Error {
				t.Errorf("%s: err = %v, want %q", c.Name, err, c.Error)
			}
			continue
		}
		if err != nil {
			t.Errorf("%s: %v", c.Name, err)
			continue
		}
		if g, ok := confSame(t, got, c.Want); !ok {
			t.Errorf("%s: got %s, want %s", c.Name, g, c.Want)
		}
	}
}

type confLayer struct {
	Type     string            `json:"type"`
	Flags    map[string]string `json:"flags"`
	Changed  map[string]bool   `json:"changed"`
	Prefix   string            `json:"prefix"`
	Env      map[string]string `json:"env"`
	Origin   string            `json:"origin"`
	YAML     string            `json:"yaml"`
	Path     string            `json:"path"`
	Defaults map[string]string `json:"defaults"`
}

var confOrigins = map[string]Origin{
	"default": OriginDefault, "global": OriginGlobal, "project": OriginProject,
	"env": OriginEnv, "flag": OriginFlag,
}

func TestConformancePrecedence(t *testing.T) {
	var f struct {
		confHeader
		EnvNames []struct {
			Prefix string `json:"prefix"`
			Key    string `json:"key"`
			Env    string `json:"env"`
		} `json:"env_names"`
		Cases []struct {
			Name   string      `json:"name"`
			Key    string      `json:"key"`
			Layers []confLayer `json:"layers"`
			Want   struct {
				Value  string `json:"value"`
				Origin string `json:"origin"`
				Detail string `json:"detail"`
			} `json:"want"`
		} `json:"cases"`
	}
	confLoad(t, "precedence.json", &f)
	for _, c := range f.EnvNames {
		if got := (EnvLayer{Prefix: c.Prefix}).EnvName(c.Key); got != c.Env {
			t.Errorf("EnvName(%q, %q) = %q, want %q", c.Prefix, c.Key, got, c.Env)
		}
	}
	for _, c := range f.Cases {
		t.Run(c.Name, func(t *testing.T) {
			var layers []Layer
			for _, l := range c.Layers {
				switch l.Type {
				case "flag":
					layers = append(layers, FlagLayer{Flags: l.Flags, Changed: l.Changed})
				case "env":
					for k, v := range l.Env {
						t.Setenv(k, v)
					}
					layers = append(layers, EnvLayer{Prefix: l.Prefix})
				case "yaml":
					o, ok := confOrigins[l.Origin]
					if !ok {
						t.Fatalf("origin %q", l.Origin)
					}
					yl := YAMLLayer{Data: map[string]any{}, OriginV: o, Path: l.Path}
					if err := yaml.Unmarshal([]byte(l.YAML), &yl.Data); err != nil {
						t.Fatal(err)
					}
					layers = append(layers, yl)
				case "default":
					layers = append(layers, DefaultLayer{Defaults: l.Defaults})
				default:
					t.Fatalf("layer type %q", l.Type)
				}
			}
			got := ResolveString(c.Key, layers...)
			if got.Value != c.Want.Value || got.Origin.String() != c.Want.Origin || got.Detail != c.Want.Detail {
				t.Errorf("got {%q %s %q}, want %+v", got.Value, got.Origin, got.Detail, c.Want)
			}
		})
	}
}

type confShape struct {
	Name   string            `yaml:"name" json:"name"`
	Port   int               `yaml:"port" json:"port"`
	Ratio  float64           `yaml:"ratio" json:"ratio"`
	Debug  bool              `yaml:"debug" json:"debug"`
	Tags   []string          `yaml:"tags" json:"tags"`
	Inner  confInner         `yaml:"inner" json:"inner"`
	Labels map[string]string `yaml:"labels" json:"labels"`
	Opt    *string           `yaml:"opt" json:"opt"`
}

type confInner struct {
	A string `yaml:"a" json:"a"`
	B int    `yaml:"b" json:"b"`
}

func TestConformanceLoad(t *testing.T) {
	var f struct {
		confHeader
		Cases []struct {
			Name          string          `json:"name"`
			Start         json.RawMessage `json:"start"`
			Files         []string        `json:"files"`
			Overrides     []string        `json:"overrides"`
			OK            bool            `json:"ok"`
			Want          json.RawMessage `json:"want"`
			ErrorContains []string        `json:"error_contains"`
		} `json:"cases"`
	}
	confLoad(t, "load.json", &f)
	for _, c := range f.Cases {
		t.Run(c.Name, func(t *testing.T) {
			var cfg confShape
			if len(c.Start) > 0 {
				if err := json.Unmarshal(c.Start, &cfg); err != nil {
					t.Fatal(err)
				}
			}
			dir := t.TempDir()
			opts := Options{}
			for i, body := range c.Files {
				p := filepath.Join(dir, fmt.Sprintf("f%d.yaml", i))
				if err := os.WriteFile(p, []byte(body), 0o600); err != nil {
					t.Fatal(err)
				}
				opts.ExtraConfigPaths = append(opts.ExtraConfigPaths, p)
			}
			if len(c.Overrides) > 0 {
				ov, err := ParseOverrides(c.Overrides)
				if err != nil {
					t.Fatal(err)
				}
				opts.Overrides = ov
			}
			err := Load(&cfg, opts)
			if !c.OK {
				if err == nil {
					t.Fatalf("want error %q", c.ErrorContains)
				}
				msg := err.Error()
				for _, p := range opts.ExtraConfigPaths {
					msg = strings.ReplaceAll(msg, p, "<path>")
				}
				for _, s := range c.ErrorContains {
					if !strings.Contains(msg, s) {
						t.Errorf("error %q lacks %q", msg, s)
					}
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			got, _ := json.Marshal(cfg)
			var g, w any
			_ = json.Unmarshal(got, &g)
			if err := json.Unmarshal(c.Want, &w); err != nil {
				t.Fatal(err)
			}
			if !reflect.DeepEqual(g, w) {
				t.Errorf("got %s, want %s", got, c.Want)
			}
		})
	}
}

func TestConformanceEdit(t *testing.T) {
	var f struct {
		confHeader
		Cases []struct {
			Name     string          `json:"name"`
			Original string          `json:"original"`
			Op       string          `json:"op"`
			Key      string          `json:"key"`
			Value    json.RawMessage `json:"value"`
			OK       bool            `json:"ok"`
			Want     json.RawMessage `json:"want"`
			Keeps    []string        `json:"keeps"`
			Error    string          `json:"error"`
		} `json:"cases"`
	}
	confLoad(t, "edit.json", &f)
	for _, c := range f.Cases {
		t.Run(c.Name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "config.yaml")
			if c.Original != "" {
				if err := os.WriteFile(path, []byte(c.Original), 0o600); err != nil {
					t.Fatal(err)
				}
			}
			opts := Options{ProjectConfigPath: path}
			var err error
			switch c.Op {
			case "set":
				var s string
				if err := json.Unmarshal(c.Value, &s); err != nil {
					t.Fatal(err)
				}
				err = Set(c.Key, s, ScopeProject, opts)
			case "set_value":
				err = SetValue(c.Key, confDecode(t, c.Value), ScopeProject, opts)
			case "unset":
				err = Unset(c.Key, ScopeProject, opts)
			default:
				t.Fatalf("op %q", c.Op)
			}
			if !c.OK {
				if err == nil || err.Error() != c.Error {
					t.Fatalf("err = %v, want %q", err, c.Error)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			data, err := os.ReadFile(path)
			if err != nil {
				t.Fatal(err)
			}
			var doc yaml.Node
			if err := yaml.Unmarshal(data, &doc); err != nil {
				t.Fatalf("written file does not parse: %v\n%s", err, data)
			}
			var got any = map[string]any{}
			if len(doc.Content) > 0 {
				got = nodeToValue(doc.Content[0])
			}
			if g, ok := confSame(t, got, c.Want); !ok {
				t.Errorf("got %s, want %s\n%s", g, c.Want, data)
			}
			for _, k := range c.Keeps {
				if !strings.Contains(string(data), k) {
					t.Errorf("file lost %q:\n%s", k, data)
				}
			}
		})
	}
}
