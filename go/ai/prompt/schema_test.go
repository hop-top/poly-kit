package prompt_test

import (
	"encoding/json"
	"errors"
	"reflect"
	"testing"

	"hop.top/kit/go/ai/prompt"
)

func TestCompileDSL(t *testing.T) {
	got, err := prompt.CompileDSL(" name, age int, email , tags []str, n []int, score float, ok bool,, ")
	if err != nil {
		t.Fatal(err)
	}
	var want map[string]any
	if err := json.Unmarshal([]byte(`{
		"type": "object",
		"properties": {
			"name": {"type": "string"},
			"age": {"type": "integer"},
			"email": {"type": "string"},
			"tags": {"type": "array", "items": {"type": "string"}},
			"n": {"type": "array", "items": {"type": "integer"}},
			"score": {"type": "number"},
			"ok": {"type": "boolean"}
		},
		"required": ["name", "age", "email", "tags", "n", "score", "ok"],
		"additionalProperties": false
	}`), &want); err != nil {
		t.Fatal(err)
	}
	// round-trip got through JSON so []string/map types compare as decoded
	b, _ := json.Marshal(got)
	var norm map[string]any
	if err := json.Unmarshal(b, &norm); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(norm, want) {
		t.Fatalf("got %s", b)
	}
}

func TestCompileDSLErrors(t *testing.T) {
	for name, dsl := range map[string]string{
		"empty":       "",
		"blank":       "  ",
		"only commas": ", ,",
		"bad type":    "a wat",
		"extra token": "a int b",
		"duplicate":   "a, a int",
	} {
		if _, err := prompt.CompileDSL(dsl); !errors.Is(err, prompt.ErrInvalid) {
			t.Errorf("%s: %v", name, err)
		}
	}
}

func TestCompileDSLResultsAreIndependent(t *testing.T) {
	a, _ := prompt.CompileDSL("x")
	a["properties"].(map[string]any)["x"].(map[string]any)["type"] = "mutated"
	b, _ := prompt.CompileDSL("x")
	if b["properties"].(map[string]any)["x"].(map[string]any)["type"] != "string" {
		t.Fatal("results share type maps")
	}
}
