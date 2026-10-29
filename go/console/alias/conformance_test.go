package alias_test

import (
	"bytes"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"testing"

	"hop.top/kit/go/console/alias"
)

// Runs contracts/alias-v1 validate.json and cycles.json. The vectors
// are the source of truth: a failure means this implementation drifted
// from the contract, not that a vector should be edited to fit.
// bridge.json runs in go/console/cli (TestConformanceAliasBridge).

const aliasConfDir = "../../../contracts/alias-v1"

// aliasConfFiles is every vector file of the corpus, with the package
// that runs it; the directory must match it exactly.
var aliasConfFiles = map[string]string{
	"validate.json": "go/console/alias",
	"cycles.json":   "go/console/alias",
	"bridge.json":   "go/console/cli",
}

func readAliasConf(t *testing.T, name string, into any) {
	t.Helper()
	dir := filepath.FromSlash(aliasConfDir)
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range entries {
		if filepath.Ext(e.Name()) == ".json" && aliasConfFiles[e.Name()] == "" {
			t.Errorf("%s has no runner", e.Name())
		}
	}
	raw, err := os.ReadFile(filepath.Join(dir, name))
	if err != nil {
		t.Fatal(err)
	}
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.DisallowUnknownFields()
	if err := dec.Decode(into); err != nil {
		t.Fatalf("%s: %v", name, err)
	}
}

func TestConformanceAliasValidate(t *testing.T) {
	var f struct {
		Version     string `json:"version"`
		Description string `json:"description"`
		Cases       []struct {
			Name     string            `json:"name"`
			Alias    string            `json:"alias"`
			Builtins []string          `json:"builtins"`
			Existing map[string]string `json:"existing"`
			Expect   string            `json:"expect"`
		} `json:"cases"`
	}
	readAliasConf(t, "validate.json", &f)
	if f.Version != "v1" {
		t.Fatalf("version %q, want v1", f.Version)
	}
	if len(f.Cases) == 0 {
		t.Fatal("no cases")
	}
	for _, c := range f.Cases {
		t.Run(c.Name, func(t *testing.T) {
			err := alias.ValidateName(c.Alias, alias.NameRules{Builtins: c.Builtins, Existing: c.Existing})
			got := "ok"
			if err != nil {
				var ne *alias.NameError
				if !errors.As(err, &ne) {
					t.Fatalf("error %v is not a *NameError", err)
				}
				got = string(ne.Kind)
			}
			if got != c.Expect {
				t.Errorf("ValidateName(%q) = %s, want %s (err: %v)", c.Alias, got, c.Expect, err)
			}
		})
	}
}

func TestConformanceAliasCycles(t *testing.T) {
	var f struct {
		Version     string `json:"version"`
		Description string `json:"description"`
		Cases       []struct {
			Name    string            `json:"name"`
			Aliases map[string]string `json:"aliases"`
			Cycles  [][]string        `json:"cycles"`
		} `json:"cases"`
	}
	readAliasConf(t, "cycles.json", &f)
	if f.Version != "v1" {
		t.Fatalf("version %q, want v1", f.Version)
	}
	if len(f.Cases) == 0 {
		t.Fatal("no cases")
	}
	for _, c := range f.Cases {
		t.Run(c.Name, func(t *testing.T) {
			got := alias.DetectCycles(c.Aliases)
			if got == nil {
				got = [][]string{}
			}
			if !reflect.DeepEqual(got, c.Cycles) {
				t.Errorf("DetectCycles(%v) = %v, want %v", c.Aliases, got, c.Cycles)
			}
		})
	}
}
