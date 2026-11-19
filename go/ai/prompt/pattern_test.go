package prompt_test

import (
	"errors"
	"reflect"
	"strings"
	"testing"

	"hop.top/kit/go/ai/prompt"
)

func TestPatterns(t *testing.T) {
	a, b := t.TempDir(), t.TempDir()
	write(t, a, "summarize/system.md", "from-a")
	write(t, b, "summarize/system.md", "from-b")
	write(t, b, "extract/system.md", "extract it")
	write(t, b, "stray.txt", "not a pattern")
	src := prompt.DirPatterns(a, b, t.TempDir()+"/missing")

	p, err := src.Load("summarize")
	if err != nil || p.Name != "summarize" || p.System != "from-a" {
		t.Fatalf("first dir must win: %+v %v", p, err)
	}
	if p, err = src.Load("extract"); err != nil || p.System != "extract it" {
		t.Fatalf("%+v %v", p, err)
	}
	if _, err = src.Load("nope"); !errors.Is(err, prompt.ErrNotFound) {
		t.Fatalf("%v", err)
	}
	names, err := src.List()
	if err != nil || !reflect.DeepEqual(names, []string{"extract", "summarize"}) {
		t.Fatalf("%v %v", names, err)
	}
}

func TestPatternNameValidation(t *testing.T) {
	src := prompt.DirPatterns(t.TempDir())
	for _, bad := range []string{"", "  ", ".", "..", "../x", "a/b", `a\b`, "/abs", "a..b"} {
		if _, err := src.Load(bad); !errors.Is(err, prompt.ErrInvalid) {
			t.Errorf("%q: %v", bad, err)
		}
	}
}

func TestBuiltinTechniques(t *testing.T) {
	var names []string
	for _, b := range prompt.Builtins() {
		names = append(names, b.Name)
		if b.Description == "" || (b.Prefix == "" && b.Suffix == "") {
			t.Errorf("%s incomplete", b.Name)
		}
	}
	if !reflect.DeepEqual(names, []string{"cot", "tot", "aot", "reflexion", "step-back"}) {
		t.Fatalf("%v", names)
	}
	prompt.Builtins()[0].Name = "mutated"
	if prompt.Builtins()[0].Name != "cot" {
		t.Fatal("Builtins shares its backing array")
	}
}

func TestWrap(t *testing.T) {
	tc := prompt.Technique{Prefix: "PRE", Suffix: "SUF"}
	if got := prompt.Wrap(tc, "SYS"); got != "PRE\n\nSYS\n\nSUF" {
		t.Errorf("%q", got)
	}
	if got := prompt.Wrap(prompt.Technique{Prefix: "PRE"}, ""); got != "PRE" {
		t.Errorf("%q", got)
	}
	if got := prompt.Wrap(prompt.Technique{Suffix: "SUF"}, "SYS"); got != "SYS\n\nSUF" {
		t.Errorf("%q", got)
	}
}

func TestDirTechniques(t *testing.T) {
	a, b := t.TempDir(), t.TempDir()
	write(t, a, "cot.md", "# mine\n\nthink hard\n---\nthen stop\n")
	write(t, a, "plain.md", "just a prefix\n")
	write(t, b, "plain.md", "shadowed\n")
	src := prompt.DirTechniques(a, b)

	got, err := src.Load("cot")
	want := prompt.Technique{Name: "cot", Description: "mine", Prefix: "think hard", Suffix: "then stop"}
	if err != nil || *got != want {
		t.Fatalf("file must shadow builtin: %+v %v", got, err)
	}
	if got, err = src.Load("plain"); err != nil || got.Prefix != "just a prefix" || got.Suffix != "" || got.Description != "" {
		t.Fatalf("%+v %v", got, err)
	}
	if got, err = src.Load("tot"); err != nil || !strings.Contains(got.Prefix, "multiple solution paths") {
		t.Fatalf("builtin fallback: %+v %v", got, err)
	}
	if _, err = src.Load("nope"); !errors.Is(err, prompt.ErrNotFound) {
		t.Fatalf("%v", err)
	}
	if _, err = src.Load("../x"); !errors.Is(err, prompt.ErrInvalid) {
		t.Fatalf("%v", err)
	}
	list, err := src.List()
	if err != nil || len(list) != 6 {
		t.Fatalf("%d %v", len(list), err)
	}
	for i := 1; i < len(list); i++ {
		if list[i-1].Name >= list[i].Name {
			t.Fatalf("unsorted or duplicate: %v", list)
		}
	}
}

func TestDirTechniquesNoDirsIsBuiltins(t *testing.T) {
	list, err := prompt.DirTechniques().List()
	if err != nil || len(list) != len(prompt.Builtins()) {
		t.Fatalf("%d %v", len(list), err)
	}
}
