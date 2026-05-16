package wizard

import (
	"bytes"
	"context"
	"errors"
	"strings"
	"testing"
)

func TestRunLine_TextInput(t *testing.T) {
	w := mustNew(t, TextInput("name", "Project name"))
	out := run(t, w, "myapp\n")
	assertResult(t, w, "name", "myapp")
	_ = out
}

func TestRunLine_TextInput_Default(t *testing.T) {
	w := mustNew(t, TextInput("name", "Project name").WithDefault("untitled"))
	run(t, w, "\n")
	assertResult(t, w, "name", "untitled")
}

func TestRunLine_TextInput_Required(t *testing.T) {
	w := mustNew(t, TextInput("name", "Project name").WithRequired())
	out := run(t, w, "\nmyapp\n")
	assertResult(t, w, "name", "myapp")
	assertContains(t, out, "Error")
}

func TestRunLine_Select(t *testing.T) {
	opts := []Option{
		{Value: "go", Label: "Go"},
		{Value: "rust", Label: "Rust"},
		{Value: "ts", Label: "TypeScript"},
	}
	w := mustNew(t, Select("lang", "Language", opts))
	run(t, w, "2\n")
	assertResult(t, w, "lang", "rust")
}

func TestRunLine_Confirm_Yes(t *testing.T) {
	w := mustNew(t, Confirm("git", "Init git?"))
	run(t, w, "y\n")
	if got := w.Results()["git"]; got != true {
		t.Errorf("got %v, want true", got)
	}
}

func TestRunLine_Confirm_No(t *testing.T) {
	w := mustNew(t, Confirm("git", "Init git?"))
	run(t, w, "n\n")
	if got := w.Results()["git"]; got != false {
		t.Errorf("got %v, want false", got)
	}
}

func TestRunLine_Confirm_Default(t *testing.T) {
	w := mustNew(t, Confirm("git", "Init git?").WithDefault(true))
	out := run(t, w, "\n")
	if got := w.Results()["git"]; got != true {
		t.Errorf("got %v, want true", got)
	}
	assertContains(t, out, "[Y/n]")
}

func TestRunLine_Back(t *testing.T) {
	w := mustNew(t, TextInput("first", "First"), TextInput("second", "Second"))
	run(t, w, "alpha\nback\nbeta\ngamma\n")
	assertResult(t, w, "first", "beta")
	assertResult(t, w, "second", "gamma")
}

func TestRunLine_Action(t *testing.T) {
	called := false
	w := mustNew(t,
		TextInput("name", "Name"),
		Action("setup", "Setting up", func(_ context.Context, _ map[string]any) error {
			called = true
			return nil
		}),
	)
	out := run(t, w, "myapp\n")
	if !called {
		t.Error("action function was not called")
	}
	assertContains(t, out, "✓")
}

func TestRunLine_ActionError(t *testing.T) {
	w := mustNew(t,
		Action("fail", "Failing", func(_ context.Context, _ map[string]any) error {
			return errors.New("boom")
		}),
	)
	var out bytes.Buffer
	_ = RunLine(context.Background(), w, strings.NewReader(""), &out)
	assertContains(t, out.String(), "✗")
}

func TestRunLine_Validation(t *testing.T) {
	opts := []Option{{Value: "a", Label: "A"}, {Value: "b", Label: "B"}}
	w := mustNew(t, Select("pick", "Pick one", opts))
	out := run(t, w, "99\n1\n")
	assertResult(t, w, "pick", "a")
	assertContains(t, out, "Error")
}

func TestRunLine_Group_Headers(t *testing.T) {
	w := mustNew(t,
		TextInput("a", "A").WithGroup("Basics"),
		TextInput("b", "B").WithGroup("Basics"),
		TextInput("c", "C").WithGroup("Advanced"),
	)
	out := run(t, w, "1\n2\n3\n")
	assertContains(t, out, "── Basics ──")
	assertContains(t, out, "── Advanced ──")
	if strings.Count(out, "── Basics ──") != 1 {
		t.Error("Basics header should appear exactly once")
	}
}

func TestRunLine_ContextCancel(t *testing.T) {
	w := mustNew(t, TextInput("name", "Name"))
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	var out bytes.Buffer
	err := RunLine(ctx, w, strings.NewReader(""), &out)
	if err == nil {
		t.Fatal("expected error")
	}
	var ae *AbortError
	if !errors.As(err, &ae) {
		t.Errorf("expected AbortError, got %T: %v", err, err)
	}
}

func TestRunLine_MultiSelect(t *testing.T) {
	opts := []Option{
		{Value: "lint", Label: "Linter"},
		{Value: "test", Label: "Testing"},
		{Value: "ci", Label: "CI"},
	}
	w := mustNew(t, MultiSelect("tools", "Tools", opts))
	run(t, w, "1,3\n")
	got, ok := w.Results()["tools"].([]string)
	if !ok {
		t.Fatalf("expected []string, got %T", w.Results()["tools"])
	}
	if len(got) != 2 || got[0] != "lint" || got[1] != "ci" {
		t.Errorf("got %v, want [lint ci]", got)
	}
}

func TestRunLine_Summary(t *testing.T) {
	w := mustNew(t, TextInput("name", "Name"), Summary("Review"))
	out := run(t, w, "myapp\n")
	assertContains(t, out, "── Review ──")
	assertContains(t, out, "myapp")
}

func TestRunLine_SummaryWithFormatFn(t *testing.T) {
	w := mustNew(t,
		TextInput("name", "Name"),
		Summary("Review").WithFormat(func(r map[string]any) string {
			return "App: " + String(r, "name")
		}),
	)
	out := run(t, w, "myapp\n")
	assertContains(t, out, "App: myapp")
}

// --- helpers ---

func mustNew(t *testing.T, steps ...Step) *Wizard {
	t.Helper()
	w, err := New(steps...)
	if err != nil {
		t.Fatal(err)
	}
	return w
}

func run(t *testing.T, w *Wizard, input string) string {
	t.Helper()
	var out bytes.Buffer
	if err := RunLine(context.Background(), w, strings.NewReader(input), &out); err != nil {
		t.Fatal(err)
	}
	return out.String()
}

func assertResult(t *testing.T, w *Wizard, key string, want any) {
	t.Helper()
	if got := w.Results()[key]; got != want {
		t.Errorf("results[%q] = %v, want %v", key, got, want)
	}
}

func assertContains(t *testing.T, s, substr string) {
	t.Helper()
	if !strings.Contains(s, substr) {
		t.Errorf("output missing %q in:\n%s", substr, s)
	}
}
